// Package monitor runs the dependency monitor's passes (dependency-monitor.md §6.1): each pass
// classifies every monitored dependency once with the procedure of §3 and records its class and
// the alerts of §6.2. It asks the provider through the metadata identity only (§4), so nothing it
// records or logs can carry a value.
package monitor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

// Metadata is the metadata identity's by-name requests (§3 step 1). An error is only an object
// the client cannot ask for; a request that fails is an answer the classifier judges.
type Metadata interface {
	KV(ctx context.Context, p provider.GenerationPath) (classify.Answer, error)
	Transit(ctx context.Context, key string) (classify.Answer, error)
}

// Monitor runs passes, the watchdog and the logger over one database.
type Monitor struct {
	db   *sql.DB
	meta Metadata
	t    Timings
	logf func(string, ...any)
	out  io.Writer
	// writer is held by the one write to out in progress (logger.go).
	writer chan struct{}
	// reporter is held by the one call to logf in progress.
	reporter chan struct{}
}

// New is a monitor asking meta, with the timings t. It reports its own failures through logf and
// writes each alert's log line (§7.1) to out.
func New(db *sql.DB, meta Metadata, t Timings, logf func(string, ...any), out io.Writer) *Monitor {
	return &Monitor{db: db, meta: meta, t: t, logf: logf, out: out, writer: make(chan struct{}, 1),
		reporter: make(chan struct{}, 1)}
}

// report reports a failure through logf, one report at a time. In serve logf writes to the same
// stderr as the alert lines, and a call to it cannot be interrupted, so a pass or the watchdog
// waits for a report at most the lock-holder timeout, once to start it and once for it to return;
// a report that cannot start in time is dropped, and one that outlasts its wait finishes alone.
func (m *Monitor) report(ctx context.Context, format string, args ...any) {
	t := time.NewTimer(m.t.LockHolder)
	defer t.Stop()
	select {
	case m.reporter <- struct{}{}:
	case <-ctx.Done():
		return
	case <-t.C:
		return
	}
	done := make(chan struct{})
	go func() {
		defer func() { <-m.reporter }()
		m.logf(format, args...)
		close(done)
	}()
	t.Reset(m.t.LockHolder)
	select {
	case <-done:
	case <-ctx.Done():
	case <-t.C:
	}
}

// Run passes until ctx ends: each starts one interval after the previous one started, or at once
// if that one took longer (§6.1, choice §11.4). The watchdog runs beside them on its own schedule
// (§6.3), and Run returns once both have stopped.
func (m *Monitor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.watch(ctx)
	}()
	defer wg.Wait()
	for {
		start := time.Now()
		if err := m.Pass(ctx); err != nil && ctx.Err() == nil {
			m.report(ctx, "dependency monitor: %v", err)
		}
		w := time.NewTimer(next(start, time.Now(), m.t.Interval))
		select {
		case <-ctx.Done():
			w.Stop()
			return
		case <-w.C:
		}
	}
}

// dependency is a monitored dependency: its DependencyStatus row's identity (§2).
type dependency struct {
	id, provider, object, created string
	version                       int64
}

// Pass classifies every monitored dependency once, then records the pass as completed. A
// dependency that fails is logged and left for the next pass; the others are still classified.
// It logs the alerts not yet logged at its start and after each dependency that raised one, once
// that dependency's advisory lock is released (§7.1).
func (m *Monitor) Pass(ctx context.Context) error {
	m.logAlerts(ctx)
	deps, err := m.monitored(ctx)
	if err != nil {
		return err
	}
	for _, d := range deps {
		if err := ctx.Err(); err != nil {
			return err
		}
		alerted, err := m.classifyOne(ctx, d)
		if err != nil && ctx.Err() == nil {
			m.report(ctx, "dependency monitor: %s not classified this pass: %v", d.id, err)
		}
		if alerted {
			m.logAlerts(ctx)
		}
	}
	return m.completed(ctx)
}

func (m *Monitor) monitored(ctx context.Context) ([]dependency, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id, provider, object, version, created FROM dependency_status ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("monitored dependencies: %w", err)
	}
	defer rows.Close()
	var deps []dependency
	for rows.Next() {
		var d dependency
		if err := rows.Scan(&d.id, &d.provider, &d.object, &d.version, &d.created); err != nil {
			return nil, fmt.Errorf("monitored dependencies: %w", err)
		}
		deps = append(deps, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("monitored dependencies: %w", err)
	}
	return deps, nil
}

// classifyOne runs §6.1's steps 1 to 5 for one dependency on one session. Every exit releases the
// advisory lock, or ends the session: a session-level lock outlives a rolled-back transaction, so
// a session returned to the pool while holding it would keep every pass from the dependency. It
// reports whether step 5 committed an alert.
func (m *Monitor) classifyOne(ctx context.Context, d dependency) (alerted bool, err error) {
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() {
		// A session that cannot be shown released is ended instead.
		if m.release(conn) != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}()
	// Step 1: the server ends a session whose process stopped, and its lock with it.
	if _, err := conn.ExecContext(ctx, `SELECT set_config('idle_session_timeout', $1, false)`, millis(m.t.IdleSession)); err != nil {
		return false, err
	}
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, d.id).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		// Another instance is classifying it.
		return false, nil
	}
	// Step 2.
	var from time.Time
	if err := conn.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&from); err != nil {
		return false, err
	}
	// Steps 3 and 4, holding no transaction (PA §5 rule 1).
	r, err := m.classify(ctx, d)
	if err != nil {
		return false, err
	}
	// Step 5.
	return m.record(ctx, conn, d, from, r)
}

// release ends classifyOne's hold on the session, held or not: its advisory lock and its setting.
// It runs after ctx may have ended, so it has a context of its own.
func (m *Monitor) release(conn *sql.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), m.t.LockHolder)
	defer cancel()
	_, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock_all()`)
	if err == nil {
		_, err = conn.ExecContext(ctx, `RESET idle_session_timeout`)
	}
	return err
}

// classify asks for the dependency by name within the request timeout and classifies the answer
// (§3, §6.1 steps 3 and 4). A failed, refused or timed-out request is an answer, classified
// unknown; only a dependency the client cannot ask for is an error.
func (m *Monitor) classify(ctx context.Context, d dependency) (classify.Result, error) {
	created, err := time.Parse(time.RFC3339Nano, d.created)
	if err != nil {
		return classify.Result{}, fmt.Errorf("creation time: %w", err)
	}
	dep := classify.Dependency{Provider: classify.Provider(d.provider), Object: d.object, Version: d.version, Created: created}
	rctx, cancel := context.WithTimeout(ctx, m.t.Request)
	defer cancel()
	var a classify.Answer
	switch dep.Provider {
	case classify.KV:
		p, err := generationPath(d.object)
		if err != nil {
			return classify.Result{}, err
		}
		a, err = m.meta.KV(rctx, p)
		if err != nil {
			return classify.Result{}, err
		}
	case classify.Transit:
		if a, err = m.meta.Transit(rctx, d.object); err != nil {
			return classify.Result{}, err
		}
	default:
		return classify.Result{}, errors.New("unknown provider")
	}
	return classify.Classify(dep, a), nil
}

// generationPath reads a KV dependency's object, gen/<cluster>/<claim>/<value>.
func generationPath(object string) (provider.GenerationPath, error) {
	parts := strings.Split(object, "/")
	if len(parts) != 4 || parts[0] != "gen" {
		return provider.GenerationPath{}, errors.New("a KV dependency's object is not a generation path")
	}
	return provider.NewGenerationPath(parts[1], parts[2], parts[3])
}

// record is §6.1 step 5: the class recorded under the row lock at clock_timestamp() read after it,
// the releases read after it, and the alerts of §6.2 inserted under the DependencyMonitor row
// lock, which allocates their recording sequence and is held to commit.
func (m *Monitor) record(ctx context.Context, conn *sql.Conn, d dependency, from time.Time, r classify.Result) (bool, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.holderTimeouts(ctx, tx); err != nil {
		return false, err
	}
	var old status
	var reason sql.NullString
	var first, since, persistent, observed, warned sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT class, reason, first_retained_at, unknown_since, persistent_alerted_at,
		deletion_observed, deletion_warned FROM dependency_status WHERE id = $1 FOR UPDATE`, d.id).
		Scan(&old.class, &reason, &first, &since, &persistent, &observed, &warned); err != nil {
		return false, fmt.Errorf("status: %w", err)
	}
	old.reason = classify.Reason(reason.String)
	old.firstRetained, old.unknownSince, old.persistentAt = first.Time, since.Time, persistent.Time
	old.deletionObserved, old.deletionWarned = observed.Time, warned.Time
	// The time the class is recorded: after the row lock, never the transaction's start.
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return false, err
	}
	s, kinds := transition(old, r, at, m.t)
	releases, err := strings1(ctx, tx, `SELECT DISTINCT release FROM dependency
		WHERE provider = $1 AND object = $2 AND version = $3 AND created = $4 ORDER BY release`,
		d.provider, d.object, d.version, d.created)
	if err != nil {
		return false, fmt.Errorf("releases: %w", err)
	}
	type alert struct {
		kind     kind
		releases []string
	}
	var alerts []alert
	for _, k := range kinds {
		alerts = append(alerts, alert{k, releases})
	}
	if s.reason == classify.DeletionScheduled {
		// Earlier warnings of this time name releases already warned (§6.2).
		warned, err := strings1(ctx, tx, `SELECT r FROM dependency_alert, unnest(releases) r
			WHERE dependency = $1 AND kind = 'deletion-scheduled' AND deletion = $2`, d.id, s.deletionObserved)
		if err != nil {
			return false, fmt.Errorf("deletion warnings: %w", err)
		}
		if names := unwarned(releases, warned); len(names) > 0 {
			alerts = append(alerts, alert{kindDeletion, names})
			s.deletionWarned = s.deletionObserved
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dependency_status SET class = $2, reason = $3, first_retained_at = $4,
		unknown_since = $5, persistent_alerted_at = $6, deletion_observed = $7, deletion_warned = $8, observed_from = $9,
		recorded_at = $10, answer_date = $11 WHERE id = $1`, d.id, s.class, nullString(string(s.reason)),
		nullTime(s.firstRetained), nullTime(s.unknownSince), nullTime(s.persistentAt), nullTime(s.deletionObserved),
		nullTime(s.deletionWarned), from, at, nullTime(r.Date)); err != nil {
		return false, fmt.Errorf("status: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		return false, fmt.Errorf("monitor row: %w", err)
	}
	for _, a := range alerts {
		var deletion any
		if a.kind == kindDeletion {
			deletion = s.deletionObserved
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dependency_alert (id, kind, dependency, provider, object, version,
			created, class, reason, releases, deletion, observed_from, answer_date, epoch, recorded_at)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, epoch, $14 FROM installation_state`,
			id.New(id.DependencyAlert), string(a.kind), d.id, d.provider, d.object, d.version, d.created, s.class,
			nullString(string(s.reason)), textArray(a.releases), deletion, from, nullTime(r.Date), at); err != nil {
			return false, fmt.Errorf("%s alert: %w", a.kind, err)
		}
	}
	if err := progress(ctx, tx, false); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return len(alerts) > 0, nil
}

// completed records a completed pass: its progress and its completion time (§6.3).
func (m *Monitor) completed(ctx context.Context) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.holderTimeouts(ctx, tx); err != nil {
		return err
	}
	if err := progress(ctx, tx, true); err != nil {
		return err
	}
	return tx.Commit()
}

// progress records the monitor's progress, and a completed pass's time, at clock_timestamp() read
// under the DependencyMonitor row lock; a transaction that began before another recorded keeps the
// later time (§6.3). The lock is taken first: an UPDATE that waits for it keeps the time it read
// before the wait.
func progress(ctx context.Context, tx *sql.Tx, pass bool) error {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		return fmt.Errorf("monitor row: %w", err)
	}
	q := `UPDATE dependency_monitor SET progress = GREATEST(progress, clock_timestamp())`
	if pass {
		q += `, last_pass = GREATEST(last_pass, clock_timestamp())`
	}
	if _, err := tx.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("progress: %w", err)
	}
	return nil
}

// holderTimeouts bounds a transaction that locks the DependencyMonitor row, so a stuck holder's
// transaction is ended and the lock released (§6.3).
func (m *Monitor) holderTimeouts(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('statement_timeout', $1, true),
		set_config('idle_in_transaction_session_timeout', $1, true)`, millis(m.t.LockHolder))
	return err
}

func millis(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// textArray is a PostgreSQL text[] literal of identifiers (no quoting needed: [a-z0-9_]).
func textArray(ss []string) string { return "{" + strings.Join(ss, ",") + "}" }

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// strings1 reads a one-column text result.
func strings1(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

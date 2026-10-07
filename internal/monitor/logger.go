package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The transaction-level advisory lock only loggers take (dependency monitor §7.1 step 1). Its
// two-key form never meets the passes' one-key dependency locks (§6.1 step 1).
const (
	loggerLockClass  = 0x62776d6c // "bwml"
	loggerLockObject = 1
)

// batch bounds the alerts one logger transaction writes (§7.1 step 2).
const batch = 100

// line is an alert's log line (§7.1 step 3): the row's fields under the stable event name. It
// carries reference names, versions and times, never a value (§4).
type line struct {
	Event      string   `json:"event"`
	Dal        string   `json:"dal"`
	Dep        string   `json:"dep,omitempty"`
	Kind       string   `json:"kind"`
	Class      string   `json:"class,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	Object     string   `json:"object,omitempty"`
	Version    *int64   `json:"version,omitempty"`
	Created    string   `json:"created,omitempty"`
	Releases   []string `json:"releases,omitempty"`
	Deletion   string   `json:"deletion,omitempty"`
	RecordedAt string   `json:"recordedAt"`
	seq        int64
}

// errBlocked is a log write that outlasted its batch, or an earlier one still blocked.
var errBlocked = errors.New("log sink blocked")

// logSoon logs the alerts not yet logged. Once Run has started it only wakes Run's logger, so a
// backlog of many batches holds up neither a pass nor the watchdog; a wake while the logger is
// logging makes it log once more, which covers alerts committed meanwhile. Outside Run the caller
// logs itself.
func (m *Monitor) logSoon(ctx context.Context) {
	if m.wake == nil {
		m.logAlerts(ctx)
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// drain is Run's logger: it logs each time it is woken, until ctx ends.
func (m *Monitor) drain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		}
		m.logAlerts(ctx)
	}
}

// logAlerts writes the log lines of the alerts not yet logged, one bounded batch at a time, while
// a batch is full (§7.1). A failure is reported and leaves the rest to the next logger: delivery
// is at least once. A blocked sink is not reported: the report would go to the same blocked stderr
// in serve and take the one report in progress; the alerts stay above the last logged sequence,
// where the record shows them.
func (m *Monitor) logAlerts(ctx context.Context) {
	for {
		n, err := m.logBatch(ctx)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, errBlocked) {
				m.report(ctx, "dependency monitor logger: %v", err)
			}
			return
		}
		if n < batch {
			return
		}
	}
}

// logBatch is §7.1 steps 1 to 4 once. The logger's own transaction holds the loggers' lock and
// the cursor across the write; it holds no DependencyMonitor lock until it advances the cursor,
// so a write that blocks delays only other loggers, until its timeout ends the transaction.
//
// A write to out cannot be interrupted, so the instance writes through one writer at a time, and
// its caller, Run's logger or a pass outside Run, waits for it at most the lock-holder timeout:
// once to take the writer and once for the write. A write that outlasts its batch ends the logger, and writes no
// further line of that batch once it returns. Run does not wait for such a write: waiting would
// let a blocked sink hold up shutdown, so at most that one write outlives Run.
func (m *Monitor) logBatch(ctx context.Context) (int, error) {
	if err := m.takeWriter(ctx); err != nil {
		return 0, err
	}
	handed := false
	defer func() {
		if !handed {
			<-m.writer
		}
	}()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.holderTimeouts(ctx, tx); err != nil {
		return 0, err
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1, $2)`, loggerLockClass, loggerLockObject).
		Scan(&locked); err != nil {
		return 0, err
	}
	if !locked {
		// Another logger is writing.
		return 0, nil
	}
	var last int64
	if err := tx.QueryRowContext(ctx, `SELECT last_logged FROM dependency_monitor`).Scan(&last); err != nil {
		return 0, fmt.Errorf("last logged: %w", err)
	}
	lines, err := m.unlogged(ctx, last)
	if err != nil {
		return 0, err
	}
	if len(lines) == 0 {
		return 0, nil
	}
	encoded := make([][]byte, 0, len(lines))
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			return 0, err
		}
		encoded = append(encoded, append(b, '\n'))
	}
	wctx, cancel := context.WithTimeout(ctx, m.t.LockHolder)
	defer cancel()
	done := make(chan error, 1)
	handed = true
	go func() {
		defer func() { <-m.writer }()
		done <- m.write(wctx, encoded)
	}()
	select {
	case err := <-done:
		if err != nil {
			return 0, err
		}
	case <-wctx.Done():
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, errBlocked
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dependency_monitor SET last_logged = $1`, lines[len(lines)-1].seq); err != nil {
		return 0, fmt.Errorf("last logged: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(lines), nil
}

// takeWriter waits at most the lock-holder timeout for the instance's writer.
func (m *Monitor) takeWriter(ctx context.Context) error {
	t := time.NewTimer(m.t.LockHolder)
	defer t.Stop()
	select {
	case m.writer <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return errBlocked
	}
}

// write writes the lines in order, one Write each, until ctx ends.
func (m *Monitor) write(ctx context.Context, lines [][]byte) error {
	for _, b := range lines {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := m.out.Write(b); err != nil {
			return fmt.Errorf("log write: %w", err)
		}
	}
	return nil
}

// unlogged is §7.1 step 2: a short transaction that locks the DependencyMonitor row FOR SHARE,
// which waits for every transaction inserting an alert, then reads at most one batch of the
// alerts above the cursor in recording order.
func (m *Monitor) unlogged(ctx context.Context, after int64) ([]line, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := m.holderTimeouts(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM dependency_monitor FOR SHARE`); err != nil {
		return nil, fmt.Errorf("monitor row: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq, id, kind, dependency, class, reason, provider, object, version,
		created, array_to_string(releases, ','), deletion, recorded_at
		FROM dependency_alert WHERE seq > $1 ORDER BY seq LIMIT $2`, after, batch)
	if err != nil {
		return nil, fmt.Errorf("alerts: %w", err)
	}
	defer rows.Close()
	var out []line
	for rows.Next() {
		l := line{Event: "dependency-alert"}
		var dep, class, reason, prov, object, created, releases sql.NullString
		var version sql.NullInt64
		var deletion sql.NullTime
		var recorded time.Time
		if err := rows.Scan(&l.seq, &l.Dal, &l.Kind, &dep, &class, &reason, &prov, &object, &version, &created,
			&releases, &deletion, &recorded); err != nil {
			return nil, fmt.Errorf("alerts: %w", err)
		}
		l.Dep, l.Class, l.Reason, l.Provider = dep.String, class.String, reason.String, prov.String
		l.Object, l.Created = object.String, created.String
		if version.Valid {
			l.Version = &version.Int64
		}
		if releases.String != "" {
			l.Releases = strings.Split(releases.String, ",")
		}
		if deletion.Valid {
			l.Deletion = deletion.Time.UTC().Format(time.RFC3339Nano)
		}
		l.RecordedAt = recorded.UTC().Format(time.RFC3339Nano)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("alerts: %w", err)
	}
	return out, tx.Commit()
}

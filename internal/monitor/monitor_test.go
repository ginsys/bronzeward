package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// fake is the metadata identity's provider: one answer per provider, an optional delay, and an
// optional gate each answer waits for.
type fake struct {
	mu          sync.Mutex
	kv, transit classify.Answer
	delay       time.Duration
	gate        chan struct{}
}

func (p *fake) KV(ctx context.Context, _ provider.GenerationPath) (classify.Answer, error) {
	return p.answer(ctx, true), nil
}

func (p *fake) Transit(ctx context.Context, _ string) (classify.Answer, error) {
	return p.answer(ctx, false), nil
}

func (p *fake) answer(ctx context.Context, kv bool) classify.Answer {
	p.mu.Lock()
	a, delay, gate := p.transit, p.delay, p.gate
	if kv {
		a = p.kv
	}
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return classify.Answer{Unreachable: true}
		}
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return classify.Answer{Unreachable: true}
		}
	}
	return a
}

func (p *fake) set(kv, transit classify.Answer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kv, p.transit = kv, transit
}

func body(t *testing.T, data map[string]any) classify.Answer {
	t.Helper()
	b, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatal(err)
	}
	return classify.Answer{Status: http.StatusOK, Date: time.Now().UTC().Format(http.TimeFormat), Body: classify.NewBody(b)}
}

// kvAnswer is the fixture's KV version 1 with its recorded creation time, edited by edit.
func kvAnswer(t *testing.T, f *fixture, edit func(v map[string]any)) classify.Answer {
	v := map[string]any{"created_time": f.kvCreated, "deletion_time": "", "destroyed": false}
	if edit != nil {
		edit(v)
	}
	return body(t, map[string]any{"current_version": 1, "oldest_version": 0, "versions": map[string]any{"1": v}})
}

func transitAnswer(t *testing.T, f *fixture) classify.Answer {
	created, err := time.Parse(time.RFC3339, f.keyCreated)
	if err != nil {
		t.Fatal(err)
	}
	return body(t, map[string]any{"keys": map[string]any{"1": created.Unix()}, "latest_version": 1,
		"min_available_version": 0, "min_decryption_version": 1, "soft_deleted": false})
}

func status404() classify.Answer { return classify.Answer{Status: http.StatusNotFound} }

// logs collects a monitor's log lines.
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func monitorFor(f *fixture, p *fake, tm Timings) (*Monitor, *logs) {
	l := &logs{}
	return New(f.db, p, tm, l.logf, io.Discard), l
}

func pass(t *testing.T, m *Monitor) {
	t.Helper()
	if err := m.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
}

type alertRow struct {
	seq                  int64
	kind, class          string
	reason               sql.NullString
	releases             []string
	deletion, answerDate sql.NullTime
	epochOK, timesOK     bool
}

// alerts is the dependency's alerts in recording order.
func alerts(t *testing.T, db *sql.DB, dep string) []alertRow {
	t.Helper()
	rows, err := db.Query(`SELECT a.seq, a.kind, a.class, a.reason, array_to_string(a.releases, ','), a.deletion, a.answer_date,
		a.epoch = s.epoch, a.recorded_at = d.recorded_at OR a.recorded_at <= d.recorded_at
		FROM dependency_alert a, installation_state s, dependency_status d
		WHERE a.dependency = $1 AND d.id = a.dependency ORDER BY a.seq`, dep)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []alertRow
	for rows.Next() {
		var a alertRow
		var rels string
		if err := rows.Scan(&a.seq, &a.kind, &a.class, &a.reason, &rels, &a.deletion, &a.answerDate, &a.epochOK, &a.timesOK); err != nil {
			t.Fatal(err)
		}
		a.releases = strings.Split(rels, ",")
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func kinds(as []alertRow) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.kind)
	}
	return out
}

type statusRow struct {
	class                  string
	reason                 sql.NullString
	since, persistent      sql.NullTime
	observed, warned       sql.NullTime
	observedFrom, recorded time.Time
}

func statusOf(t *testing.T, db *sql.DB, dep string) statusRow {
	t.Helper()
	var s statusRow
	if err := db.QueryRow(`SELECT class, reason, unknown_since, persistent_alerted_at, deletion_observed, deletion_warned,
		observed_from, recorded_at FROM dependency_status WHERE id = $1`, dep).
		Scan(&s.class, &s.reason, &s.since, &s.persistent, &s.observed, &s.warned, &s.observedFrom, &s.recorded); err != nil {
		t.Fatal(err)
	}
	return s
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func advisoryLocks(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Dependency monitor §6.1 step 5, §6.2: each transition records its class and raises its alert
// once, naming every release that references the version, in the installation's epoch.
func TestPassTransitions(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), transitAnswer(t, f))
	m, l := monitorFor(f, p, Defaults())
	pass(t, m)
	if s := statusOf(t, f.db, f.depKV); s.class != "retained" || len(alerts(t, f.db, f.depKV)) != 0 {
		t.Fatalf("live KV: %+v", s)
	}
	var lastPass sql.NullTime
	if err := f.db.QueryRow(`SELECT last_pass FROM dependency_monitor`).Scan(&lastPass); err != nil || !lastPass.Valid {
		t.Fatalf("last pass %v %v", lastPass, err)
	}
	// KV destroyed: lost, once. Transit 404: a regression from retained, unknown since now.
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), status404())
	pass(t, m)
	pass(t, m)
	kv, key := alerts(t, f.db, f.depKV), alerts(t, f.db, f.depKey)
	if !slices.Equal(kinds(kv), []string{"lost"}) || !slices.Equal(kinds(key), []string{"regression"}) {
		t.Fatalf("alerts kv %v key %v", kinds(kv), kinds(key))
	}
	for _, a := range append(kv, key...) {
		if !slices.Equal(a.releases, f.releases) || !a.epochOK || !a.timesOK {
			t.Fatalf("alert %+v, releases want %v", a, f.releases)
		}
	}
	if kv[0].class != "lost" || kv[0].reason.String != "destroyed" || !kv[0].answerDate.Valid {
		t.Fatalf("lost alert %+v", kv[0])
	}
	if s := statusOf(t, f.db, f.depKey); s.class != "unknown" || s.reason.String != "absent" || !s.since.Valid {
		t.Fatalf("transit status %+v", s)
	}
	// KV soft-deleted: blocked. Transit back: retained, unknown_since cleared, no alert.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	p.set(kvAnswer(t, f, func(v map[string]any) { v["deletion_time"] = past }), transitAnswer(t, f))
	pass(t, m)
	if got := kinds(alerts(t, f.db, f.depKV)); !slices.Equal(got, []string{"lost", "blocked"}) {
		t.Fatalf("kv alerts %v", got)
	}
	if s := statusOf(t, f.db, f.depKey); s.class != "retained" || s.since.Valid || len(alerts(t, f.db, f.depKey)) != 1 {
		t.Fatalf("transit back: %+v", s)
	}
	if l.count() != 0 {
		t.Fatalf("logged %v", l.lines)
	}
}

// §6.2: unknown alerts persistent 15 minutes after unknown_since, then 15 minutes after the last
// one, and not in between.
func TestPassPersistent(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), status404())
	m, _ := monitorFor(f, p, Defaults())
	pass(t, m)
	pass(t, m)
	if got := kinds(alerts(t, f.db, f.depKey)); !slices.Equal(got, []string{"regression"}) {
		t.Fatalf("alerts %v", got)
	}
	exec(t, f.db, `UPDATE dependency_status SET unknown_since = unknown_since - interval '15 minutes' WHERE id = $1`, f.depKey)
	pass(t, m)
	pass(t, m)
	if got := kinds(alerts(t, f.db, f.depKey)); !slices.Equal(got, []string{"regression", "persistent"}) {
		t.Fatalf("alerts %v", got)
	}
	exec(t, f.db, `UPDATE dependency_status SET persistent_alerted_at = persistent_alerted_at - interval '15 minutes' WHERE id = $1`, f.depKey)
	pass(t, m)
	if got := kinds(alerts(t, f.db, f.depKey)); !slices.Equal(got, []string{"regression", "persistent", "persistent"}) {
		t.Fatalf("alerts %v", got)
	}
}

// §6.2: a scheduled deletion is warned once per time; a release committed later is warned by the
// next pass, alone; another time warns every release.
func TestPassDeletionScheduled(t *testing.T) {
	f := seed(t)
	p := &fake{}
	first := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	at := func(s time.Time) classify.Answer {
		return kvAnswer(t, f, func(v map[string]any) { v["deletion_time"] = s.Format(time.RFC3339Nano) })
	}
	p.set(at(first), transitAnswer(t, f))
	m, _ := monitorFor(f, p, Defaults())
	pass(t, m)
	pass(t, m)
	as := alerts(t, f.db, f.depKV)
	if len(as) != 1 || as[0].kind != "deletion-scheduled" || !as[0].deletion.Time.Equal(first) || !slices.Equal(as[0].releases, f.releases) {
		t.Fatalf("alerts %+v", as)
	}
	if s := statusOf(t, f.db, f.depKV); s.class != "retained" || !s.observed.Time.Equal(first) || !s.warned.Time.Equal(first) {
		t.Fatalf("status %+v", s)
	}
	rel2 := f.publishAgain(t)
	pass(t, m)
	as = alerts(t, f.db, f.depKV)
	if len(as) != 2 || !slices.Equal(as[1].releases, []string{rel2}) {
		t.Fatalf("alerts %+v", as)
	}
	second := first.Add(time.Hour)
	p.set(at(second), transitAnswer(t, f))
	pass(t, m)
	as = alerts(t, f.db, f.depKV)
	if len(as) != 3 || !as[2].deletion.Time.Equal(second) || !slices.Equal(as[2].releases, slices.Sorted(slices.Values(f.releases))) {
		t.Fatalf("alerts %+v", as)
	}
}

// §6.1: a request that fails, is refused or times out is not an exit: it is recorded unknown
// with its reason and drives a regression.
func TestPassProviderFailure(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(classify.Answer{Status: http.StatusServiceUnavailable}, classify.Answer{Unreachable: true})
	m, l := monitorFor(f, p, Defaults())
	pass(t, m)
	if s := statusOf(t, f.db, f.depKV); s.class != "unknown" || s.reason.String != "unavailable" {
		t.Fatalf("kv %+v", s)
	}
	if s := statusOf(t, f.db, f.depKey); s.class != "unknown" || s.reason.String != "unreachable" {
		t.Fatalf("transit %+v", s)
	}
	if !slices.Equal(kinds(alerts(t, f.db, f.depKV)), []string{"regression"}) || l.count() != 0 {
		t.Fatalf("alerts %v logs %v", kinds(alerts(t, f.db, f.depKV)), l.lines)
	}
	// The request timeout bounds a provider that does not answer.
	tm := Defaults()
	tm.Request = 200 * time.Millisecond
	p.delay = 5 * time.Second
	m, _ = monitorFor(f, p, tm)
	start := time.Now()
	pass(t, m)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("pass took %v", d)
	}
	if s := statusOf(t, f.db, f.depKV); s.reason.String != "unreachable" {
		t.Fatalf("kv %+v", s)
	}
}

// §6.1 step 1: a dependency whose advisory lock another session holds is skipped this pass.
func TestPassSkipsLockedDependency(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), status404())
	m, _ := monitorFor(f, p, Defaults())
	holder, err := f.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtextextended($1, 0))`, f.depKV); err != nil {
		t.Fatal(err)
	}
	pass(t, m)
	if s := statusOf(t, f.db, f.depKV); s.class != "retained" {
		t.Fatalf("locked dependency classified: %+v", s)
	}
	if s := statusOf(t, f.db, f.depKey); s.class != "unknown" {
		t.Fatalf("other dependency skipped: %+v", s)
	}
	if _, err := holder.ExecContext(context.Background(), `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatal(err)
	}
	pass(t, m)
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" {
		t.Fatalf("after unlock: %+v", s)
	}
}

// §6.1: a step 5 that fails releases the advisory lock, so the next pass records.
func TestPassReleasesLockAfterFailedRecord(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	m, l := monitorFor(f, p, Defaults())
	exec(t, f.db, `CREATE FUNCTION refuse_status() RETURNS trigger LANGUAGE plpgsql AS
		$$BEGIN RAISE EXCEPTION 'refused for the test'; END$$`)
	exec(t, f.db, `CREATE TRIGGER refuse BEFORE UPDATE ON dependency_status FOR EACH ROW EXECUTE FUNCTION refuse_status()`)
	pass(t, m)
	if l.count() != 2 {
		t.Fatalf("logged %v", l.lines)
	}
	if n := advisoryLocks(t, f.db); n != 0 {
		t.Fatalf("%d advisory locks held after the pass", n)
	}
	exec(t, f.db, `DROP TRIGGER refuse ON dependency_status`)
	pass(t, m)
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" {
		t.Fatalf("after the failure: %+v", s)
	}
}

// §6.1 step 5: recorded_at is read after the row lock is held, so a wait for it does not date the
// transition before the holder's release.
func TestPassRecordedAfterRowLock(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	m, _ := monitorFor(f, p, Defaults())
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT 1 FROM dependency_status WHERE id = $1 FOR UPDATE`, f.depKV); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Pass(context.Background()) }()
	dbtest.WaitForLockWait(t, f.db)
	time.Sleep(100 * time.Millisecond)
	var released time.Time
	if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" || !s.recorded.After(released) {
		t.Fatalf("recorded at %v, the row lock released at %v", s.recorded, released)
	}
}

// §6.1 step 1: the session's idle-session timeout ends a pass session idle longer than it; its
// lock goes with it and nothing is recorded.
func TestPassIdleSessionTimeout(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	p.delay = 1500 * time.Millisecond
	tm := Defaults()
	tm.IdleSession = 300 * time.Millisecond
	m, l := monitorFor(f, p, tm)
	pass(t, m)
	if s := statusOf(t, f.db, f.depKV); s.class != "retained" {
		t.Fatalf("recorded on an ended session: %+v", s)
	}
	if l.count() != 2 || advisoryLocks(t, f.db) != 0 {
		t.Fatalf("logs %v, locks %d", l.lines, advisoryLocks(t, f.db))
	}
}

// §6.3: a transaction that locks the DependencyMonitor row runs with the lock-holder timeouts, so
// a stuck holder fails the step instead of hanging the pass.
func TestPassMonitorLockTimeout(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	tm := Defaults()
	tm.LockHolder = 300 * time.Millisecond
	m, _ := monitorFor(f, p, tm)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = m.Pass(context.Background())
	if d := time.Since(start); err == nil || d > 3*time.Second {
		t.Fatalf("pass returned %v after %v", err, d)
	}
	if s := statusOf(t, f.db, f.depKV); s.class != "retained" {
		t.Fatalf("recorded without the monitor lock: %+v", s)
	}
}

// §6.3: progress is never moved back; a completed pass records its time.
func TestPassProgressNeverBack(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	m, _ := monitorFor(f, p, Defaults())
	exec(t, f.db, `UPDATE dependency_monitor SET progress = now() + interval '1 hour', last_pass = now() + interval '1 hour'`)
	var before time.Time
	if err := f.db.QueryRow(`SELECT progress FROM dependency_monitor`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	pass(t, m)
	var after, lastPass time.Time
	if err := f.db.QueryRow(`SELECT progress, last_pass FROM dependency_monitor`).Scan(&after, &lastPass); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) || !lastPass.Equal(before) {
		t.Fatalf("progress %v last pass %v, want %v", after, lastPass, before)
	}
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" {
		t.Fatalf("status %+v", s)
	}
}

// §6.1, §6.3: each step 5 and each completed pass record progress.
func TestPassProgress(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), transitAnswer(t, f))
	m, _ := monitorFor(f, p, Defaults())
	var before time.Time
	if err := f.db.QueryRow(`SELECT progress FROM dependency_monitor`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	pass(t, m)
	var after, lastPass time.Time
	if err := f.db.QueryRow(`SELECT progress, last_pass FROM dependency_monitor`).Scan(&after, &lastPass); err != nil {
		t.Fatal(err)
	}
	if !after.After(before) || lastPass.Before(after) {
		t.Fatalf("progress %v → %v, last pass %v", before, after, lastPass)
	}
}

// §6.1: Run passes on its schedule until its context ends.
func TestRun(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), transitAnswer(t, f))
	tm := Defaults()
	tm.Interval = 100 * time.Millisecond
	m, _ := monitorFor(f, p, tm)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(450 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	var lastPass time.Time
	if err := f.db.QueryRow(`SELECT last_pass FROM dependency_monitor`).Scan(&lastPass); err != nil {
		t.Fatal(err)
	}
	if time.Since(lastPass) > 400*time.Millisecond {
		t.Fatalf("last pass %v ago", time.Since(lastPass))
	}
}

// §6.3: progress, from a step 5 and from a completed pass, is dated after the DependencyMonitor
// row lock is held, not at the transaction's start, which a wait for the lock leaves behind.
func TestProgressAfterMonitorLock(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), transitAnswer(t, f))
	m, _ := monitorFor(f, p, Defaults())
	deps, err := m.monitored(context.Background())
	if err != nil || len(deps) == 0 {
		t.Fatalf("monitored %v %v", deps, err)
	}
	steps := map[string]func(context.Context) error{
		"step 5":    func(ctx context.Context) error { _, err := m.classifyOne(ctx, deps[0]); return err },
		"completed": m.completed,
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- step(context.Background()) }()
			dbtest.WaitForLockWait(t, f.db)
			time.Sleep(100 * time.Millisecond)
			var released time.Time
			if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&released); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var progress time.Time
			if err := f.db.QueryRow(`SELECT progress FROM dependency_monitor`).Scan(&progress); err != nil {
				t.Fatal(err)
			}
			if !progress.After(released) {
				t.Fatalf("progress %v, the row lock released at %v", progress, released)
			}
		})
	}
}

// §6.3: a DependencyMonitor lock holder that stops after taking the row has its transaction ended
// by the server within the bound, so another session takes the row.
func TestHolderIdleTimeout(t *testing.T) {
	f := seed(t)
	tm := Defaults()
	tm.LockHolder = 300 * time.Millisecond
	m, _ := monitorFor(f, &fake{}, tm)
	ctx := context.Background()
	holder, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if err := m.holderTimeouts(ctx, holder); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	// The holder stops here; another session waits at most 5 seconds for the row.
	other, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback() }()
	if _, err := other.Exec(`SET LOCAL lock_timeout = '5s'`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := other.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		t.Fatalf("the row stayed held: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took the row after %v", d)
	}
}

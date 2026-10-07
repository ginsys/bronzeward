package monitor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// sink is a log sink that keeps every line, and can hold or slow each write.
type sink struct {
	mu    sync.Mutex
	lines []map[string]any
	delay time.Duration
	gate  chan struct{}
}

func (s *sink) Write(b []byte) (int, error) {
	s.mu.Lock()
	delay, gate := s.delay, s.gate
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	time.Sleep(delay)
	if bytes.Count(b, []byte("\n")) != 1 || b[len(b)-1] != '\n' {
		return 0, errors.New("not one line per write")
	}
	var l map[string]any
	if err := json.Unmarshal(b, &l); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.lines = append(s.lines, l)
	s.mu.Unlock()
	return len(b), nil
}

func (s *sink) snapshot() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lines)
}

// dals is the dal identifiers written, in order.
func (s *sink) dals() []string {
	var out []string
	for _, l := range s.snapshot() {
		d, _ := l["dal"].(string)
		out = append(out, d)
	}
	return out
}

func (s *sink) has(kind string) bool {
	for _, l := range s.snapshot() {
		if l["kind"] == kind {
			return true
		}
	}
	return false
}

func withSink(f *fixture, p *fake, tm Timings) (*Monitor, *sink) {
	s := &sink{}
	l := &logs{}
	return New(f.db, p, tm, l.logf, s), s
}

// stalledAlert records a monitor-stalled alert and returns its identifier.
func stalledAlert(t *testing.T, db *sql.DB) string {
	t.Helper()
	dal := id.New(id.DependencyAlert)
	exec(t, db, `INSERT INTO dependency_alert (id, kind, epoch, recorded_at)
		SELECT $1, 'monitor-stalled', epoch, clock_timestamp() FROM installation_state`, dal)
	return dal
}

func lastLogged(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT last_logged FROM dependency_monitor`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func maxSeq(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT max(seq) FROM dependency_alert`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Dependency monitor §7.1, §10.1 item 11: the alerts not yet logged are written in recording
// order and the last logged sequence advances; an alert committed before a process stopped is
// written by the next instance to log, and a written alert is not written again.
func TestLoggerOrderAndCursor(t *testing.T) {
	f := seed(t)
	want := []string{stalledAlert(t, f.db), stalledAlert(t, f.db), stalledAlert(t, f.db)}
	m, s := withSink(f, &fake{}, Defaults())
	m.logAlerts(context.Background())
	if !slices.Equal(s.dals(), want) || lastLogged(t, f.db) != maxSeq(t, f.db) {
		t.Fatalf("logged %v, cursor %d", s.dals(), lastLogged(t, f.db))
	}
	next := stalledAlert(t, f.db)
	other, s2 := withSink(f, &fake{}, Defaults())
	other.logAlerts(context.Background())
	if !slices.Equal(s2.dals(), []string{next}) {
		t.Fatalf("next instance logged %v", s2.dals())
	}
}

// §7.1 step 1: an instance that finds the loggers' lock held logs nothing.
func TestLoggerLockHeld(t *testing.T) {
	f := seed(t)
	stalledAlert(t, f.db)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, loggerLockClass, loggerLockObject); err != nil {
		t.Fatal(err)
	}
	m, s := withSink(f, &fake{}, Defaults())
	m.logAlerts(context.Background())
	if len(s.dals()) != 0 || lastLogged(t, f.db) != 0 {
		t.Fatalf("logged %v under a held lock", s.dals())
	}
}

// §7.1 step 2, §10.1 item 11: the logger's read waits for every holder of the DependencyMonitor
// row, so an alert committing while it reads is written in that same run.
func TestLoggerWaitsForHolder(t *testing.T) {
	f := seed(t)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	dal := id.New(id.DependencyAlert)
	if _, err := tx.Exec(`INSERT INTO dependency_alert (id, kind, epoch, recorded_at)
		SELECT $1, 'monitor-stalled', epoch, clock_timestamp() FROM installation_state`, dal); err != nil {
		t.Fatal(err)
	}
	m, s := withSink(f, &fake{}, Defaults())
	done := make(chan struct{})
	go func() { m.logAlerts(context.Background()); close(done) }()
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	<-done
	if !slices.Equal(s.dals(), []string{dal}) {
		t.Fatalf("logged %v", s.dals())
	}
}

// §7.1 step 3, §10.1 item 11: a logger paused in its log write holds no DependencyMonitor lock, so
// passes progress and the watchdog raises meanwhile; after its timeout ends its transaction, its
// lines are written again.
func TestLoggerPausedWrite(t *testing.T) {
	f := seed(t)
	dal := stalledAlert(t, f.db)
	tm := Defaults()
	tm.Interval = 100 * time.Millisecond
	tm.LockHolder = time.Second
	m, s := withSink(f, &fake{}, tm)
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { m.logAlerts(context.Background()); close(done) }()
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	completed(t, m)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("progress waited %v for a paused logger", d)
	}
	time.Sleep(400 * time.Millisecond)
	start = time.Now()
	if !watchdogOnce(t, m) {
		t.Fatal("the watchdog raised nothing while a logger was paused")
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("the watchdog waited %v for a paused logger", d)
	}
	time.Sleep(1500 * time.Millisecond)
	s.mu.Lock()
	s.gate = nil
	s.mu.Unlock()
	close(gate)
	<-done
	m.logAlerts(context.Background())
	if got := s.dals(); len(got) < 3 || got[0] != dal || got[1] != dal || lastLogged(t, f.db) != maxSeq(t, f.db) {
		t.Fatalf("logged %v, cursor %d of %d", got, lastLogged(t, f.db), maxSeq(t, f.db))
	}
}

// §7.1 step 4, §10.1 item 11: a backlog of 250 alerts, with a sink slowed so that 250 writes outlast
// the timeout and 100 do not, is logged in bounded batches, each line once.
func TestLoggerBacklog(t *testing.T) {
	f := seed(t)
	var want []string
	for range 250 {
		want = append(want, stalledAlert(t, f.db))
	}
	tm := Defaults()
	tm.LockHolder = time.Second
	m, s := withSink(f, &fake{}, tm)
	s.delay = 6 * time.Millisecond
	m.logAlerts(context.Background())
	if !slices.Equal(s.dals(), want) || lastLogged(t, f.db) != maxSeq(t, f.db) {
		t.Fatalf("logged %d of 250, cursor %d of %d", len(s.dals()), lastLogged(t, f.db), maxSeq(t, f.db))
	}
}

// §7.1 step 3: a pass logs the alerts it records; each line carries the row's fields, and a
// scheduled deletion's time only on a deletion-scheduled line.
func TestPassLogsAlerts(t *testing.T) {
	f := seed(t)
	p := &fake{}
	deletion := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	p.set(kvAnswer(t, f, func(v map[string]any) { v["deletion_time"] = deletion.Format(time.RFC3339Nano) }),
		transitAnswer(t, f))
	m, s := withSink(f, p, Defaults())
	pass(t, m)
	lines := s.snapshot()
	if len(lines) != 1 {
		t.Fatalf("lines %v", lines)
	}
	l := lines[0]
	var dal string
	var version int64
	if err := f.db.QueryRow(`SELECT id, version FROM dependency_alert`).Scan(&dal, &version); err != nil {
		t.Fatal(err)
	}
	releases := slices.Sorted(slices.Values(f.releases))
	want := map[string]any{"event": "dependency-alert", "dal": dal, "dep": f.depKV, "kind": "deletion-scheduled",
		"class": "retained", "reason": "deletion-scheduled", "provider": "kv", "object": f.kv,
		"version": float64(version), "created": f.kvCreated, "deletion": deletion.Format(time.RFC3339Nano)}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("%s = %v, want %v", k, l[k], v)
		}
	}
	got, _ := l["releases"].([]any)
	var names []string
	for _, r := range got {
		names = append(names, r.(string))
	}
	if !slices.Equal(names, releases) || l["recordedAt"] == nil || len(l) != len(want)+2 {
		t.Fatalf("line %v", l)
	}

	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	pass(t, m)
	lines = s.snapshot()
	if len(lines) != 2 || lines[1]["kind"] != "lost" {
		t.Fatalf("lines %v", lines)
	}
	if _, ok := lines[1]["deletion"]; ok {
		t.Fatalf("a lost line carries a deletion time: %v", lines[1])
	}
}

package monitor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// sink is a log sink that keeps every line, and can hold or slow each write.
type sink struct {
	mu    sync.Mutex
	lines []map[string]any
	delay time.Duration
	gate  chan struct{}
	// active and most count the writes in progress, now and at most.
	active, most int
}

func (s *sink) Write(b []byte) (int, error) {
	s.mu.Lock()
	delay, gate := s.delay, s.gate
	s.active++
	s.most = max(s.most, s.active)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
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

func alertCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM dependency_alert`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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
	dbtest.WaitForLockWait(t, f.db)
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

// §7.1 step 3: a write that blocks past the timeout ends its logger, which writes no further line of
// that batch and reports nothing, and no second write starts on the instance while it blocks; once
// the sink unblocks, the next logger writes the batch again.
func TestLoggerOneWriter(t *testing.T) {
	f := seed(t)
	first, second := stalledAlert(t, f.db), stalledAlert(t, f.db)
	tm := Defaults()
	tm.LockHolder = 300 * time.Millisecond
	m, s := withSink(f, &fake{}, tm)
	var reports atomic.Int32
	m.logf = func(string, ...any) { reports.Add(1) }
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.logAlerts(context.Background())
		}()
		time.Sleep(400 * time.Millisecond)
	}
	s.mu.Lock()
	most := s.most
	s.gate = nil
	s.mu.Unlock()
	close(gate)
	wg.Wait()
	// The blocked write returns now; take the writer once it lets go.
	m.writer <- struct{}{}
	<-m.writer
	if most != 1 || reports.Load() != 0 {
		t.Fatalf("%d writes at once on one instance, %d reports", most, reports.Load())
	}
	if got := s.dals(); !slices.Equal(got, []string{first}) {
		t.Fatalf("an expired batch wrote on: %v", got)
	}
	m.logAlerts(context.Background())
	if got := s.dals(); !slices.Equal(got, []string{first, first, second}) || lastLogged(t, f.db) != maxSeq(t, f.db) {
		t.Fatalf("logged %v, cursor %d of %d", got, lastLogged(t, f.db), maxSeq(t, f.db))
	}
}

// §7.1 step 3: a batch read delayed by a holder of the DependencyMonitor row for most of the idle
// timeout leaves the logger's transaction, and the loggers' lock, held for a full timeout from the
// read, and its write ends before that; another instance logs nothing meanwhile.
func TestLoggerLockOutlivesRead(t *testing.T) {
	f := seed(t)
	stalledAlert(t, f.db)
	tm := Defaults()
	tm.LockHolder = 2 * time.Second
	m, s := withSink(f, &fake{}, tm)
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	holder, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.logAlerts(context.Background()); close(done) }()
	defer func() {
		close(gate)
		<-done
	}()
	dbtest.WaitForLockWait(t, f.db)
	time.Sleep(1600 * time.Millisecond)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	// Without the restart, the idle timeout has ended the first logger's transaction by now.
	time.Sleep(time.Second)
	other, s2 := withSink(f, &fake{}, tm)
	other.logAlerts(context.Background())
	s.mu.Lock()
	writing := s.active
	s.mu.Unlock()
	if got := s2.dals(); len(got) != 0 || writing != 1 {
		t.Fatalf("another instance logged %v while the first wrote (%d writes)", got, writing)
	}
}

// §7.1, §6.3: a batch read that waits out the lock-holder timeout behind a holder of the
// DependencyMonitor row fails, and the failure is reported. A report blocked in the same sink as the
// lines holds up the logger at most the lock-holder timeout, so neither a pass nor the watchdog
// waits on it.
func TestLoggerBlockedReport(t *testing.T) {
	f := seed(t)
	stalledAlert(t, f.db)
	tm := Defaults()
	tm.LockHolder = time.Second
	m, s := withSink(f, &fake{}, tm)
	gate := make(chan struct{})
	defer close(gate)
	var reports atomic.Int32
	reported := make(chan string, 1)
	m.logf = func(format string, args ...any) {
		if reports.Add(1) == 1 {
			reported <- fmt.Sprintf(format, args...)
		}
		<-gate
	}
	holder, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.logAlerts(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("the logger waited on a blocked report (%d reports)", reports.Load())
	}
	if reports.Load() != 1 || len(s.dals()) != 0 || lastLogged(t, f.db) != 0 {
		t.Fatalf("%d reports, logged %v, cursor %d", reports.Load(), s.dals(), lastLogged(t, f.db))
	}
	if msg := <-reported; !strings.HasPrefix(msg, "dependency monitor logger: monitor row:") {
		t.Fatalf("reported %q, not the batch read's failure", msg)
	}
	// A further report, while that one still blocks, is dropped after the timeout.
	start := time.Now()
	m.report(context.Background(), "further")
	if d := time.Since(start); d > 2*time.Second || reports.Load() != 1 {
		t.Fatalf("a further report waited %v (%d reports)", d, reports.Load())
	}
}

// §7.1: an alert a pass records while Run's logger is still writing earlier lines is logged once
// that write ends, with no further pass or watchdog alert to wake the logger.
func TestRunWakeWhileLogging(t *testing.T) {
	f := seed(t)
	first := stalledAlert(t, f.db)
	// The provider answers only once the logger is writing the first alert, so the pass records the
	// second alert while that write is in progress.
	answers := make(chan struct{})
	p := &fake{gate: answers}
	deletion := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	p.set(kvAnswer(t, f, func(v map[string]any) { v["deletion_time"] = deletion.Format(time.RFC3339Nano) }),
		transitAnswer(t, f))
	tm := Defaults()
	tm.Interval = time.Hour
	m, s := withSink(f, p, tm)
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	answered, written := false, false
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() {
		if !answered {
			close(answers)
		}
		if !written {
			close(gate)
		}
		cancel()
		<-done
	}()
	// The pass's wake at its start reaches the logger.
	writing := 0
	for deadline := time.Now().Add(5 * time.Second); writing == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
		writing = s.active
		s.mu.Unlock()
	}
	if writing != 1 || alertCount(t, f.db) != 1 {
		t.Fatalf("%d writes before the pass recorded, %d alerts", writing, alertCount(t, f.db))
	}
	answered = true
	close(answers)
	var second string
	for deadline := time.Now().Add(5 * time.Second); second == "" && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if err := f.db.QueryRow(`SELECT coalesce(max(id), '') FROM dependency_alert WHERE kind = 'deletion-scheduled'`).
			Scan(&second); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	writing = s.active
	s.mu.Unlock()
	if second == "" || writing != 1 {
		t.Fatalf("alert %q recorded with %d writes in progress", second, writing)
	}
	s.mu.Lock()
	s.gate = nil
	s.mu.Unlock()
	written = true
	close(gate)
	for deadline := time.Now().Add(3 * time.Second); len(s.dals()) < 2 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	if got := s.dals(); !slices.Equal(got, []string{first, second}) {
		t.Fatalf("logged %v, want %v", got, []string{first, second})
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

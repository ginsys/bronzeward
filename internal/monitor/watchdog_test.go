package monitor

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// stalledCount is the number of monitor-stalled alerts recorded.
func stalledCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM dependency_alert WHERE kind = 'monitor-stalled'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func watchdogOnce(t *testing.T, m *Monitor) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raised, err := m.watchdog(ctx)
	if err != nil {
		t.Fatalf("watchdog: %v", err)
	}
	return raised
}

func completed(t *testing.T, m *Monitor) {
	t.Helper()
	if err := m.completed(context.Background()); err != nil {
		t.Fatalf("completed: %v", err)
	}
}

// Dependency monitor §6.3: with no progress for three intervals the watchdog raises one
// monitor-stalled alert, again only after each further three intervals, and none once the monitor
// progresses. The alert concerns no dependency and is recorded in the installation's epoch.
func TestWatchdogStalled(t *testing.T) {
	f := seed(t)
	tm := Defaults()
	// Every check that expects nothing runs well inside three intervals; the repeat is brought due by
	// moving the last alert back, not by sleeping.
	tm.Interval = time.Second
	m, _ := monitorFor(f, &fake{}, tm)
	completed(t, m)
	if watchdogOnce(t, m) {
		t.Fatal("raised with fresh progress")
	}
	time.Sleep(3200 * time.Millisecond)
	if !watchdogOnce(t, m) || watchdogOnce(t, m) {
		t.Fatal("not raised exactly once after three intervals")
	}
	if _, err := f.db.Exec(`UPDATE dependency_monitor SET last_stalled = last_stalled - interval '2 seconds'`); err != nil {
		t.Fatal(err)
	}
	if watchdogOnce(t, m) {
		t.Fatal("raised again before a further three intervals")
	}
	if _, err := f.db.Exec(`UPDATE dependency_monitor SET last_stalled = last_stalled - interval '1100 milliseconds'`); err != nil {
		t.Fatal(err)
	}
	if !watchdogOnce(t, m) {
		t.Fatal("not raised again after a further three intervals")
	}
	completed(t, m)
	if watchdogOnce(t, m) || stalledCount(t, f.db) != 2 {
		t.Fatalf("after progress: %d alerts", stalledCount(t, f.db))
	}
	var epochOK, timesOK bool
	if err := f.db.QueryRow(`SELECT bool_and(a.epoch = s.epoch), bool_and(a.recorded_at <= m.last_stalled)
		FROM dependency_alert a, installation_state s, dependency_monitor m`).Scan(&epochOK, &timesOK); err != nil {
		t.Fatal(err)
	}
	if !epochOK || !timesOK {
		t.Fatalf("epoch %v, times %v", epochOK, timesOK)
	}
}

// §6.3, §10.1 item 7: a pass slowed past three intervals by request timeouts still progresses at
// each step 5, so the watchdog raises nothing; measuring from completed passes would.
func TestWatchdogSlowPass(t *testing.T) {
	f := seed(t)
	p := &fake{delay: time.Minute}
	tm := Defaults()
	tm.Interval = 200 * time.Millisecond
	tm.Request = 400 * time.Millisecond
	m, _ := monitorFor(f, p, tm)
	completed(t, m)
	done := make(chan error, 1)
	go func() { done <- m.Pass(context.Background()) }()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("pass: %v", err)
			}
			if n := stalledCount(t, f.db); n != 0 {
				t.Fatalf("%d monitor-stalled alerts during a progressing pass", n)
			}
			return
		case <-time.After(100 * time.Millisecond):
			if watchdogOnce(t, m) {
				t.Fatal("raised during a progressing pass")
			}
		}
	}
}

// §6.3: the watchdog's read waits for no lock, so a held DependencyMonitor row does not delay a
// watchdog with nothing due.
func TestWatchdogPlainRead(t *testing.T) {
	f := seed(t)
	m, _ := monitorFor(f, &fake{}, Defaults())
	completed(t, m)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT 1 FROM dependency_monitor FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if watchdogOnce(t, m) {
		t.Fatal("raised with fresh progress")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waited %v", d)
	}
}

// §6.3, §10.1 item 7: a pass hung while it holds the DependencyMonitor row has its transaction
// ended by the holder timeouts, and the watchdog then raises monitor-stalled.
func TestWatchdogHungHolder(t *testing.T) {
	f := seed(t)
	tm := Defaults()
	tm.Interval = 100 * time.Millisecond
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
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		wctx, cancel := context.WithTimeout(ctx, time.Second)
		raised, _ := m.watchdog(wctx)
		cancel()
		if raised {
			return
		}
		time.Sleep(tm.Interval)
	}
	t.Fatalf("no monitor-stalled within 3s of a hung holder (%d alerts)", stalledCount(t, f.db))
}

// §6.3, §10.1 item 7: with every pass hung, Run's watchdog raises monitor-stalled; a stall check
// inside the pass loop would never run.
func TestRunHungPasses(t *testing.T) {
	f := seed(t)
	p := &fake{delay: time.Minute}
	tm := Defaults()
	tm.Interval = 100 * time.Millisecond
	tm.Request = time.Minute
	m, s := withSink(f, p, tm)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if stalledCount(t, f.db) > 0 && s.has("monitor-stalled") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("hung passes: %d monitor-stalled alerts, logged %v", stalledCount(t, f.db), s.has("monitor-stalled"))
}

// §6.1, §6.3, §7.1: a backlog of many full batches on a slow sink is logged by Run's own logger,
// so passes complete meanwhile and the watchdog finds nothing stalled; a pass that drained the
// backlog at its start would make no progress until every batch was written.
func TestRunBacklog(t *testing.T) {
	f := seed(t)
	for range 1000 {
		stalledAlert(t, f.db)
	}
	backlog := stalledCount(t, f.db)
	tm := Defaults()
	tm.Interval = 200 * time.Millisecond
	tm.LockHolder = time.Second
	m, s := withSink(f, &fake{}, tm)
	s.delay = 5 * time.Millisecond // 0.5 s a batch, 5 s the backlog
	var before sql.NullTime
	if err := f.db.QueryRow(`SELECT last_pass FROM dependency_monitor`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()
	passed := false
	for deadline := time.Now().Add(2 * time.Second); !passed && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if err := f.db.QueryRow(`SELECT last_pass IS DISTINCT FROM $1 FROM dependency_monitor`, before).
			Scan(&passed); err != nil {
			t.Fatal(err)
		}
	}
	if !passed || stalledCount(t, f.db) != backlog {
		t.Fatalf("pass completed %v, %d monitor-stalled alerts, %d of 1000 lines written", passed,
			stalledCount(t, f.db)-backlog, len(s.dals()))
	}
}

// §6.3, §7.1: with every pass hung and the log sink blocked, the watchdog still raises
// monitor-stalled every three intervals, and Run returns once cancelled; a log write holds up
// neither.
func TestRunBlockedSink(t *testing.T) {
	f := seed(t)
	p := &fake{delay: time.Minute}
	tm := Defaults()
	tm.Interval = 100 * time.Millisecond
	tm.Request = time.Minute
	tm.LockHolder = 300 * time.Millisecond
	m, s := withSink(f, p, tm)
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	defer close(gate)
	// serve reports diagnostics on the same stderr, which blocks with it.
	m.logf = func(string, ...any) { <-gate }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(4 * time.Second)
	for stalledCount(t, f.db) < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	n := stalledCount(t, f.db)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return with the sink blocked (%d monitor-stalled alerts)", n)
	}
	if n < 3 {
		t.Fatalf("%d monitor-stalled alerts with the sink blocked", n)
	}
}

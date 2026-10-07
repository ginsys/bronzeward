package migrate

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// An alert of the dependency release_rows seeded: $1 id, $2 kind, $3 dependency, $4 provider,
// $5 object, $6 version, $7 created, $8 class, $9 reason, $10 releases, $11 deletion.
const insertAlert = `INSERT INTO dependency_alert (id, kind, dependency, provider, object, version, created, class,
	reason, releases, deletion, observed_from, answer_date, epoch, recorded_at)
	SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10::text[], $11, now(), now(), epoch, now() FROM installation_state`

// A monitor-stalled alert, which concerns no dependency (dependency monitor §5.1).
const insertStalled = `INSERT INTO dependency_alert (id, kind, epoch, recorded_at)
	SELECT $1, 'monitor-stalled', epoch, now() FROM installation_state`

// insertStalledWith is a monitor-stalled alert that also sets one column, to $2 as expr.
func insertStalledWith(column, expr string) string {
	return `INSERT INTO dependency_alert (id, kind, ` + column + `, epoch, recorded_at)
	SELECT $1, 'monitor-stalled', ` + expr + `, epoch, now() FROM installation_state`
}

// Dependency monitor §5.1, §6.2: what the schema itself refuses in an alert.
func TestDependencyAlertConstraints(t *testing.T) {
	db, _ := installed(t)
	r := releaseRows(t, db)
	kv := generation(r.cluster, r.claim)
	rels := "{" + r.rel + "}"
	alert := func(kind, class string, reason, releases, deletion any) []any {
		return []any{id.New(id.DependencyAlert), kind, r.depKV, "kv", kv, 1, created, class, reason, releases, deletion}
	}
	schedule := "2026-10-09T00:00:00Z"
	for _, c := range []struct {
		name string
		q    string
		args []any
		want string
	}{
		{"alert of an unknown kind", insertAlert, alert("fine", "lost", "destroyed", rels, nil), "dependency_alert_kind"},
		{"lost alert of another class", insertAlert, alert("lost", "unknown", "absent", rels, nil), "dependency_alert_kind"},
		{"blocked alert of another class", insertAlert, alert("blocked", "lost", "destroyed", rels, nil), "dependency_alert_kind"},
		{"regression alert of another class", insertAlert, alert("regression", "blocked", "soft-deleted", rels, nil),
			"dependency_alert_kind"},
		{"persistent alert of another class", insertAlert, alert("persistent", "retained", nil, rels, nil), "dependency_alert_kind"},
		{"deletion-scheduled alert of a retained version without a schedule", insertAlert,
			alert("deletion-scheduled", "retained", nil, rels, schedule), "dependency_alert_kind"},
		{"deletion-scheduled alert without the time it warns of", insertAlert,
			alert("deletion-scheduled", "retained", "deletion-scheduled", rels, nil), "dependency_alert_shape"},
		{"lost alert with a scheduled deletion time", insertAlert, alert("lost", "lost", "destroyed", rels, schedule),
			"dependency_alert_shape"},
		// The class and reason are a row of dependency monitor §3's table for the provider.
		{"lost alert with a reason of the other provider", insertAlert, alert("lost", "lost", "trimmed", rels, nil),
			"dependency_alert_reason"},
		{"regression alert without a reason", insertAlert, alert("regression", "unknown", nil, rels, nil), "dependency_alert_reason"},
		{"alert naming no release", insertAlert, alert("lost", "lost", "destroyed", "{}", nil), "dependency_alert_shape"},
		{"alert without its releases", insertAlert, alert("lost", "lost", "destroyed", nil, nil), "dependency_alert_shape"},
		// The releases it names reference the version (dependency monitor §6.1 step 5).
		{"alert naming a release that does not reference the version", insertAlert,
			alert("lost", "lost", "destroyed", "{"+id.New(id.Release)+"}", nil), "dependency_alert_releases"},
		{"alert naming a release twice", insertAlert, alert("lost", "lost", "destroyed", "{"+r.rel+","+r.rel+"}", nil),
			"dependency_alert_releases"},
		{"Transit key alert naming a release that does not reference it", insertAlert,
			[]any{id.New(id.DependencyAlert), "lost", r.depKey, "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z", "lost",
				"trimmed", "{" + id.New(id.Release) + "}", nil}, "dependency_alert_releases"},
		// The provider object, version and creation time are the dependency's own.
		{"alert of a dependency under another object", insertAlert,
			[]any{id.New(id.DependencyAlert), "lost", r.depKV, "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z", "lost",
				"trimmed", rels, nil}, "23503"},
		{"alert of a dependency that does not exist", insertAlert,
			[]any{id.New(id.DependencyAlert), "lost", id.New(id.Dependency), "kv", kv, 1, created, "lost", "destroyed", rels, nil},
			"23503"},
		{"alert with an id of another kind", insertAlert,
			[]any{id.New(id.Dependency), "lost", r.depKV, "kv", kv, 1, created, "lost", "destroyed", rels, nil}, "dependency_alert_id_check"},
		// A monitor-stalled alert concerns no dependency, and every other alert concerns one.
		{"monitor-stalled alert naming a dependency", insertAlert, alert("monitor-stalled", "lost", "destroyed", rels, nil),
			"dependency_alert_kind"},
		{"monitor-stalled alert with a class", insertAlert,
			[]any{id.New(id.DependencyAlert), "monitor-stalled", nil, nil, nil, nil, nil, "lost", nil, nil, nil}, "dependency_alert_kind"},
		{"monitor-stalled alert with releases", insertStalledWith("releases", "$2::text[]"),
			[]any{id.New(id.DependencyAlert), rels}, "dependency_alert_shape"},
		{"monitor-stalled alert with an observation time", insertStalledWith("observed_from", "$2::timestamptz"),
			[]any{id.New(id.DependencyAlert), schedule}, "dependency_alert_shape"},
		{"monitor-stalled alert with an answer Date", insertStalledWith("answer_date", "$2::timestamptz"),
			[]any{id.New(id.DependencyAlert), schedule}, "dependency_alert_shape"},
		{"monitor-stalled alert with a reason", insertStalledWith("reason", "$2"),
			[]any{id.New(id.DependencyAlert), "absent"}, "dependency_alert_shape"},
		{"monitor-stalled alert with a provider object", insertStalledWith("object", "$2"),
			[]any{id.New(id.DependencyAlert), kv}, "dependency_alert_shape"},
		{"lost alert concerning no dependency", insertAlert,
			[]any{id.New(id.DependencyAlert), "lost", nil, "kv", kv, 1, created, "lost", "destroyed", rels, nil}, "dependency_alert_shape"},
		{"control: monitor-stalled alert", insertStalled, []any{id.New(id.DependencyAlert)}, ""},
		{"control: lost alert", insertAlert, alert("lost", "lost", "destroyed", rels, nil), ""},
		{"control: blocked alert", insertAlert, alert("blocked", "blocked", "soft-deleted", rels, nil), ""},
		{"control: regression alert", insertAlert, alert("regression", "unknown", "unreachable", rels, nil), ""},
		{"control: persistent alert", insertAlert, alert("persistent", "unknown", "absent", rels, nil), ""},
		{"control: deletion-scheduled alert", insertAlert,
			alert("deletion-scheduled", "retained", "deletion-scheduled", rels, schedule), ""},
		{"control: alert of the Transit key", insertAlert,
			[]any{id.New(id.DependencyAlert), "lost", r.depKey, "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z", "lost",
				"trimmed", rels, nil}, ""},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(c.q, c.args...); refusal(err) != c.want {
				t.Errorf("%s: %v; got %q, want %q", c.name, err, refusal(err), c.want)
			}
		}()
	}
}

// Dependency monitor §5.1: an alert is immutable.
func TestDependencyAlertImmutable(t *testing.T) {
	db, _ := installed(t)
	mustExec(t, db, insertStalled, id.New(id.DependencyAlert))
	for _, stmt := range []string{"UPDATE dependency_alert SET kind = kind", "DELETE FROM dependency_alert",
		"TRUNCATE dependency_alert"} {
		if _, err := db.Exec(stmt); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s: %v; want SQLSTATE %s", stmt, err, ImmutableSQLState)
		}
	}
}

// Dependency monitor §6.1 step 5, §7.1, §10.1 item 11: an alert's recording sequence is allocated
// under the DependencyMonitor row lock, so sequences commit in order. An insert that waits for the
// lock takes its sequence after the holder's alert, not before it. Control: a sequence allocated
// before the lock (an identity default) gives the waiting insert the lower number, committed after
// the holder's higher one, which a logger reading by sequence would skip.
func TestDependencyAlertSequenceUnderMonitorLock(t *testing.T) {
	db, _ := installed(t)
	holder, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	mustExec(t, holder, `SELECT 1 FROM dependency_monitor FOR UPDATE`)
	waiting := id.New(id.DependencyAlert)
	done := make(chan error, 1)
	go func() {
		_, err := db.Exec(insertStalled, waiting)
		done <- err
	}()
	waitAlertBlocked(t, db)
	held := id.New(id.DependencyAlert)
	mustExec(t, holder, insertStalled, held)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var first, second int64
	if err := db.QueryRow(`SELECT seq FROM dependency_alert WHERE id = $1`, held).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT seq FROM dependency_alert WHERE id = $1`, waiting).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if first >= second {
		t.Fatalf("holder's alert seq %d, waiting alert seq %d: the waiting insert allocated before the lock", first, second)
	}
	// A sequence supplied by the writer is not the one recorded.
	supplied := id.New(id.DependencyAlert)
	mustExec(t, db, `INSERT INTO dependency_alert (id, seq, kind, epoch, recorded_at)
		SELECT $1, 1, 'monitor-stalled', epoch, now() FROM installation_state`, supplied)
	var third int64
	if err := db.QueryRow(`SELECT seq FROM dependency_alert WHERE id = $1`, supplied).Scan(&third); err != nil {
		t.Fatal(err)
	}
	if third <= second {
		t.Fatalf("supplied seq recorded as %d, after %d", third, second)
	}
}

// waitAlertBlocked waits until an insert into dependency_alert in this test's database is blocked
// by another backend.
func waitAlertBlocked(t *testing.T, db *sql.DB) {
	t.Helper()
	for range 200 {
		var pid int
		switch err := db.QueryRow(`SELECT pid FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND cardinality(pg_blocking_pids(pid)) > 0
			AND query LIKE '%dependency_alert%' AND pid <> pg_backend_pid() LIMIT 1`).Scan(&pid); {
		case err == nil:
			return
		case !errors.Is(err, sql.ErrNoRows):
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the alert insert never waited for the DependencyMonitor lock")
}

// Dependency monitor §5.1, §6.3, §7.1: the DependencyMonitor row is created with the installation,
// its progress the installation time, and is the only one; its progress and its last logged
// sequence never move back.
func TestDependencyMonitorRow(t *testing.T) {
	db, _ := installed(t)
	var n int
	var progressed bool
	var lastLogged int64
	if err := db.QueryRow(`SELECT count(*), bool_and(m.progress = e.entered_at), max(m.last_logged)
		FROM dependency_monitor m, installation_state s JOIN recovery_epoch e USING (epoch)`).
		Scan(&n, &progressed, &lastLogged); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !progressed || lastLogged != 0 {
		t.Fatalf("monitor rows %d, progress at installation %v, last logged %d; want 1, true, 0", n, progressed, lastLogged)
	}
	for _, c := range []struct{ name, q, want string }{
		{"second monitor row", `INSERT INTO dependency_monitor (singleton, progress) VALUES (true, now())`, "23505"},
		{"monitor row that is not the singleton", `INSERT INTO dependency_monitor (singleton, progress) VALUES (false, now())`,
			"dependency_monitor_singleton_check"},
		{"progress moved back", `UPDATE dependency_monitor SET progress = progress - interval '1 microsecond'`,
			"dependency_monitor_forward"},
		{"last logged sequence moved back", `UPDATE dependency_monitor SET last_logged = -1`, "dependency_monitor_forward"},
		{"control: progress and last logged sequence moved on",
			`UPDATE dependency_monitor SET progress = progress + interval '1 second', last_logged = last_logged + 1,
			 last_pass = now(), last_stalled = now()`, ""},
		{"control: progress kept", `UPDATE dependency_monitor SET progress = progress`, ""},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(c.q); refusal(err) != c.want {
				t.Errorf("%s: %v; got %q, want %q", c.name, err, refusal(err), c.want)
			}
		}()
	}
}

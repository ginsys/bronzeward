package migrate

import (
	"context"
	"database/sql"
	"slices"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

func migrated(t *testing.T) (*sql.DB, []Migration) {
	t.Helper()
	db, _ := dbtest.New(t)
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), db, ms); err != nil {
		t.Fatal(err)
	}
	return db, ms
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInstallOnce(t *testing.T) {
	db, ms := migrated(t)
	ctx := context.Background()
	e1, created1, err := Install(ctx, db)
	if err != nil || !created1 {
		t.Fatalf("first Install: %q, %v, %v", e1, created1, err)
	}
	if err := id.MustHave(e1, id.Epoch); err != nil {
		t.Fatal(err)
	}
	e2, created2, err := Install(ctx, db)
	if err != nil || created2 || e2 != e1 {
		t.Fatalf("second Install: %q, %v, %v; want %q, false", e2, created2, err, e1)
	}
	var version int
	var recovery bool
	if err := db.QueryRow("SELECT schema_version, recovery_mode FROM installation_state").Scan(&version, &recovery); err != nil {
		t.Fatal(err)
	}
	if version != len(ms) || recovery || count(t, db, "recovery_epoch") != 1 {
		t.Fatalf("schema_version %d, recovery_mode %v, epochs %d", version, recovery, count(t, db, "recovery_epoch"))
	}
}

func TestInstallConcurrent(t *testing.T) {
	db, _ := migrated(t)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		epochs  []string
		created int
		errs    []error
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, c, err := Install(context.Background(), db)
			mu.Lock()
			defer mu.Unlock()
			epochs = append(epochs, e)
			if c {
				created++
			}
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	if len(errs) != 0 || created != 1 || len(slices.Compact(slices.Sorted(slices.Values(epochs)))) != 1 ||
		count(t, db, "recovery_epoch") != 1 {
		t.Fatalf("created %d, epochs %v, errors %v", created, epochs, errs)
	}
}

// The control: without the lock, two runs both find no installation; the later writer fails on
// the singleton key.
func TestInstallNoLockControl(t *testing.T) {
	db, _ := migrated(t)
	ctx := context.Background()
	held, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, _, err := install(ctx, db, options{noLock: true, afterRead: func() { close(held); <-release }})
		first <- err
	}()
	<-held
	if _, _, err := install(ctx, db, options{noLock: true}); err != nil {
		t.Fatalf("second install: %v", err)
	}
	close(release)
	if err := <-first; sqlState(err) != "23505" {
		t.Fatalf("first install after the second committed: %v; want a unique violation", err)
	}
}

func TestImmutableTablesRefuse(t *testing.T) {
	db, _ := migrated(t)
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE schema_migrations SET name = name",
		"DELETE FROM schema_migrations",
		"TRUNCATE schema_migrations",
		"UPDATE recovery_epoch SET entered_by = entered_by",
		"DELETE FROM recovery_epoch",
		"TRUNCATE recovery_epoch CASCADE",
	} {
		if _, err := db.Exec(stmt); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s: %v; want SQLSTATE %s", stmt, err, ImmutableSQLState)
		}
	}
	if count(t, db, "installation_state") != 1 {
		t.Fatal("the refused TRUNCATE ... CASCADE removed installation_state's row")
	}
}

// The control: with the row trigger dropped, the UPDATE the test above refuses succeeds.
func TestImmutableTriggerControl(t *testing.T) {
	db, _ := migrated(t)
	if _, _, err := Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DROP TRIGGER immutable_rows ON recovery_epoch"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE recovery_epoch SET entered_by = entered_by"); err != nil {
		t.Fatalf("UPDATE without the trigger: %v", err)
	}
}

// Every table is classified here, and each immutable one carries both triggers: a later
// migration's table fails this test until its author decides which it is.
func TestEveryTableClassified(t *testing.T) {
	db, _ := migrated(t)
	immutable := []string{"recovery_epoch", "schema_migrations"}
	mutable := []string{"installation_state"}
	rows, err := db.Query("SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		switch {
		case slices.Contains(immutable, name):
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM pg_trigger WHERE tgrelid = $1::regclass
				AND tgname IN ('immutable_rows', 'immutable_truncate')`, name).Scan(&n); err != nil || n != 2 {
				t.Errorf("%s: %d immutability triggers, %v", name, n, err)
			}
		case slices.Contains(mutable, name):
		default:
			t.Errorf("table %s is neither immutable nor mutable in this test", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

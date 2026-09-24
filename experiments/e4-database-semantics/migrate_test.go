package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func appliedVersions(t *testing.T, db *DB) []int {
	t.Helper()
	rows, err := db.Query(context.Background(), "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var vs []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return vs
}

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	q := "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	if db.D == Postgres {
		q = "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = ?"
	}
	var n int
	if err := db.QueryRow(context.Background(), q, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrationsLoadInOrder(t *testing.T) {
	for _, d := range []Dialect{Postgres, SQLite} {
		ms, err := Migrations(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) < 4 {
			t.Fatalf("%v: %d migrations, want at least 4", d, len(ms))
		}
		for i, m := range ms {
			if m.Version != i+1 {
				t.Fatalf("%v: migration %d has version %d", d, i, m.Version)
			}
		}
	}
}

func TestMigrateFreshAppliesAllOnce(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ms, err := Migrations(db.D)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Migrate(ctx, db, ms, MigrateOptions{Runner: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != len(ms) {
		t.Fatalf("applied %v, want all %d", applied, len(ms))
	}
	again, err := Migrate(ctx, db, ms, MigrateOptions{Runner: "t2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second run applied %v, want none", again)
	}
	if got := appliedVersions(t, db); len(got) != len(ms) {
		t.Fatalf("schema_migrations %v", got)
	}
}

func TestMigrateFailureRollsBackThatMigration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ms, err := Migrations(db.D)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Migrate(ctx, db, ms, MigrateOptions{Runner: "t", FailAt: 3})
	if err == nil {
		t.Fatal("migration 3 with an injected failure succeeded")
	}
	if got := appliedVersions(t, db); len(got) != 2 {
		t.Fatalf("schema_migrations %v after a failure in 3, want [1 2]", got)
	}
	if tableExists(t, db, "job") {
		t.Fatal("table job, created by migration 3 before its failure, survived the rollback")
	}
	if _, err := Migrate(ctx, db, ms, MigrateOptions{Runner: "t"}); err != nil {
		t.Fatalf("re-run after the failure: %v", err)
	}
	if !tableExists(t, db, "job") {
		t.Fatal("re-run did not create job")
	}
}

func TestMigrateConcurrentRunnersApplyOnce(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	ms, err := Migrations(db.D)
	if err != nil {
		t.Fatal(err)
	}
	const runners = 4
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
		errs  []error
	)
	for i := 0; i < runners; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			applied, err := Migrate(ctx, db, ms, MigrateOptions{Runner: "r" + string(rune('0'+i))})
			mu.Lock()
			defer mu.Unlock()
			total += len(applied)
			if err != nil {
				errs = append(errs, err)
			}
		}(i)
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("runner errors: %v", errs)
	}
	if total != len(ms) {
		t.Fatalf("runners applied %d migrations between them, want %d", total, len(ms))
	}
}

// The SQLite rebuild migration needs foreign-key enforcement off outside its transaction. Without
// that, the DROP of the referenced table fails; this is the cost the report counts.
func TestSQLiteRebuildNeedsForeignKeysOff(t *testing.T) {
	if os.Getenv("E4_TEST_PG_DSN") != "" {
		t.Skip("SQLite-only")
	}
	ctx := context.Background()
	db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ms, err := Migrations(SQLite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db, ms[:3], MigrateOptions{Runner: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO job (state) VALUES ('pending')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO job_event (job_id, worker, fence, action) VALUES (1, 'w', 1, 'claim')"); err != nil {
		t.Fatal(err)
	}
	rebuild := ms[3]
	if !rebuild.ForeignKeysOff {
		t.Fatal("migration 4 is not marked as needing foreign keys off")
	}
	rebuild.ForeignKeysOff = false
	if _, err := Migrate(ctx, db, []Migration{ms[0], ms[1], ms[2], rebuild}, MigrateOptions{Runner: "t"}); err == nil {
		t.Fatal("the rebuild with foreign keys on succeeded; the counted cost would be imaginary")
	}
	if _, err := Migrate(ctx, db, ms, MigrateOptions{Runner: "t"}); err != nil {
		t.Fatalf("the rebuild as marked: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO job (state) VALUES ('bogus')"); Classify(err) == ClassNone {
		t.Fatal("the rebuilt job table accepts a state outside the CHECK")
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM job_event").Scan(&n); err != nil || n != 1 {
		t.Fatalf("job_event rows after the rebuild: %d, %v", n, err)
	}
}

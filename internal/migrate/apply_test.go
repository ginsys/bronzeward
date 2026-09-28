package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/dbtest"
)

// synthetic returns migrations 1..n, each creating table tN, so the runner's tests do not depend
// on 0001's content.
func synthetic(n int) []Migration {
	var ms []Migration
	for v := 1; v <= n; v++ {
		ms = append(ms, mig(v, fmt.Sprintf("CREATE TABLE t%d (x integer)", v)))
	}
	return ms
}

func mig(v int, sql string) Migration {
	sum := sha256.Sum256([]byte(sql))
	return Migration{Version: v, Name: fmt.Sprintf("m%d", v), SQL: sql, Checksum: hex.EncodeToString(sum[:])}
}

func versions(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
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

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow("SELECT to_regclass($1) IS NOT NULL", name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// waitForLockWait polls until some session of this database waits on a lock.
func waitForLockWait(t *testing.T, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no session started waiting on a lock within 10s")
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func TestApplyFreshThenNothing(t *testing.T) {
	db, _ := dbtest.New(t)
	ctx := context.Background()
	got, err := Apply(ctx, db, synthetic(3))
	if err != nil || !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("first run: %v, %v", got, err)
	}
	got, err = Apply(ctx, db, synthetic(3))
	if err != nil || len(got) != 0 {
		t.Fatalf("second run: %v, %v", got, err)
	}
	if vs := versions(t, db); !slices.Equal(vs, []int{1, 2, 3}) {
		t.Fatalf("schema_migrations %v", vs)
	}
}

func TestApplyEmbedded(t *testing.T) {
	db, _ := dbtest.New(t)
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), db, ms); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"schema_migrations", "recovery_epoch", "installation_state"} {
		if !tableExists(t, db, name) {
			t.Errorf("%s missing", name)
		}
	}
}

func TestApplyConcurrentRunnersApplyOnce(t *testing.T) {
	db, _ := dbtest.New(t)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
		errs  []error
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := Apply(context.Background(), db, synthetic(3))
			mu.Lock()
			defer mu.Unlock()
			total += len(got)
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	if len(errs) != 0 || total != 3 {
		t.Fatalf("applied %d between them, errors %v", total, errs)
	}
}

// PA §13.5 step 2: the second run waits on the lock, then finds the version recorded and skips.
func TestSecondRunWaitsThenSkips(t *testing.T) {
	db, _ := dbtest.New(t)
	ctx := context.Background()
	held, release := make(chan struct{}), make(chan struct{})
	first := make(chan []int, 1)
	go func() {
		got, err := apply(ctx, db, synthetic(1), options{afterBody: func(int, int) { close(held); <-release }})
		if err != nil {
			t.Error(err)
		}
		first <- got
	}()
	<-held
	second := make(chan error, 1)
	var secondGot []int
	go func() {
		var err error
		secondGot, err = Apply(ctx, db, synthetic(1))
		second <- err
	}()
	waitForLockWait(t, db)
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := <-first; !slices.Equal(got, []int{1}) || len(secondGot) != 0 {
		t.Fatalf("first applied %v, second %v; want [1] and none", got, secondGot)
	}
}

// The control: without the advisory lock, the second run does not see the first's uncommitted
// row, runs the same CREATE TABLE, waits on the catalog, and fails when the first commits.
func TestNoLockControl(t *testing.T) {
	db, _ := dbtest.New(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, nil); err != nil { // create schema_migrations first
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = apply(ctx, db, synthetic(1), options{noLock: true, afterBody: func(int, int) { close(held); <-release }})
	}()
	<-held
	second := make(chan error, 1)
	go func() {
		_, err := apply(ctx, db, synthetic(1), options{noLock: true})
		second <- err
	}()
	waitForLockWait(t, db)
	close(release)
	err := <-second
	if code := sqlState(err); code != "23505" && code != "42P07" {
		t.Fatalf("second run without the lock: %v (SQLSTATE %q); want a duplicate failure", err, code)
	}
}

// PA §13.5 step 3 and §14's Migrate row: a run killed inside migration 2 leaves nothing of 2,
// the server check refuses the schema, and the next run applies 2.
func TestKilledMigrationLeavesNothing(t *testing.T) {
	db, _ := dbtest.New(t)
	ctx := context.Background()
	ms := synthetic(2)
	pidc, release := make(chan int, 1), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := apply(ctx, db, ms, options{afterBody: func(v, pid int) {
			if v == 2 {
				pidc <- pid
				<-release
			}
		}})
		done <- err
	}()
	pid := <-pidc
	if _, err := db.ExecContext(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("the killed run reported success")
	}
	if vs := versions(t, db); !slices.Equal(vs, []int{1}) || tableExists(t, db, "t2") {
		t.Fatalf("after the kill: versions %v, t2 exists %v", vs, tableExists(t, db, "t2"))
	}
	if err := Check(ctx, db, ms); err == nil || !strings.Contains(err.Error(), "[2]") {
		t.Fatalf("Check after the kill: %v", err)
	}
	if got, err := Apply(ctx, db, ms); err != nil || !slices.Equal(got, []int{2}) {
		t.Fatalf("rerun: %v, %v", got, err)
	}
}

func TestApplyRefusesNewerDatabase(t *testing.T) {
	db, _ := dbtest.New(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, synthetic(3)); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(ctx, db, synthetic(2))
	if err == nil || !strings.Contains(err.Error(), "[3]") || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("older binary on a newer database: %v", err)
	}
}

func TestApplyRefusesEditedMigration(t *testing.T) {
	db, _ := dbtest.New(t)
	ctx := context.Background()
	if _, err := Apply(ctx, db, synthetic(2)); err != nil {
		t.Fatal(err)
	}
	ms := synthetic(2)
	ms[1] = mig(2, "CREATE TABLE t2 (x integer, y integer)")
	_, err := Apply(ctx, db, ms)
	if err == nil || !strings.Contains(err.Error(), "migration 2") || !strings.Contains(err.Error(), "edited") {
		t.Fatalf("edited migration: %v", err)
	}
}

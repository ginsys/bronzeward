package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// migrated is testDB with the dialect's migrations applied.
func migrated(t *testing.T) *DB {
	t.Helper()
	db := testDB(t)
	ms, err := Migrations(db.D)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), db, ms, MigrateOptions{Runner: "test"}); err != nil {
		t.Fatal(err)
	}
	return db
}

// read runs a reader file from readers/ through the test's own connection, the same SQL the
// evidence runs through psql and sqlite3, and returns its name|value rows.
func read(t *testing.T, db *DB, file string) map[string]string {
	t.Helper()
	body, err := os.ReadFile("readers/" + file)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, stmt := range ReaderStatements(string(body)) {
		rows, err := db.QueryContext(context.Background(), stmt)
		if err != nil {
			t.Fatalf("%s: %q: %v", file, stmt, err)
		}
		for rows.Next() {
			var k, v sql.NullString
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			out[k.String] = v.String
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func want(t *testing.T, got map[string]string, key, value string) {
	t.Helper()
	if got[key] != value {
		t.Errorf("%s = %q, want %q (all: %v)", key, got[key], value, got)
	}
}

func TestReaderStatementsSkipsComments(t *testing.T) {
	got := ReaderStatements("-- a comment\nSELECT 1;\n\nSELECT 'x;y', 2;\n")
	if len(got) != 2 || got[0] != "SELECT 1" || got[1] != "SELECT 'x;y', 2" {
		t.Fatalf("%q", got)
	}
}

func TestS1CompareAndSetKeepsEveryWrite(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedFragment(ctx, db, "f1"); err != nil {
		t.Fatal(err)
	}
	st, err := RaceRevisions(ctx, db, RaceOptions{Fragment: "f1", Writers: 6, Rounds: 10, Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Wins != 10 || st.Conflicts != 50 {
		t.Fatalf("wins %d conflicts %d busy %d, want 10 and 50", st.Wins, st.Conflicts, st.Busy)
	}
	r := read(t, db, "s1.sql")
	want(t, r, "revisions_recorded", "10")
	want(t, r, "duplicate_revisions", "0")
	want(t, r, "final_revision", "10")
	want(t, r, "lost_updates", "0")
}

// The control: without the revision predicate every writer "wins" and all but one write is lost.
// The reader must see it.
func TestS1BlindWriteLosesUpdates(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedFragment(ctx, db, "f1"); err != nil {
		t.Fatal(err)
	}
	st, err := RaceRevisions(ctx, db, RaceOptions{Fragment: "f1", Writers: 6, Rounds: 10, Actor: "t", Blind: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Wins != 60 {
		t.Fatalf("blind wins %d, want 60", st.Wins)
	}
	r := read(t, db, "s1.sql")
	want(t, r, "final_revision", "10")
	want(t, r, "lost_updates", "50")
	want(t, r, "duplicate_revisions", "10")
}

func TestS2PublishIsAllOrNothing(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedFragment(ctx, db, "f1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(ctx, db, PublishOptions{Release: "r1", Actor: "t", Pins: map[string]int64{"f1": 0}, Artifacts: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(ctx, db, PublishOptions{Release: "r2", Actor: "t", Pins: map[string]int64{"f1": 0}, Artifacts: 5, FailAt: 3}); err == nil {
		t.Fatal("publication with an injected failure at artifact 3 succeeded")
	}
	if _, err := UpdateFragment(ctx, db, "f1", "t", 0, false); err != nil {
		t.Fatal(err)
	}
	_, err := Publish(ctx, db, PublishOptions{Release: "r3", Actor: "t", Pins: map[string]int64{"f1": 0}, Artifacts: 5})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("publication pinned to a superseded revision: %v, want ErrStale", err)
	}
	r := read(t, db, "s2.sql")
	want(t, r, "releases", "1")
	want(t, r, "artifacts", "5")
	want(t, r, "incomplete_releases", "0")
	want(t, r, "stale_at_commit", "0")
	if !strings.HasPrefix(r["release:r1"], "1 5 ") {
		t.Fatalf("release r1: %q", r["release:r1"])
	}
}

// A writer racing a publication that holds its source check: the publication must not commit on
// a revision superseded before its commit. With the source lock (FOR SHARE on PostgreSQL; the
// IMMEDIATE transaction on SQLite) the writer waits.
func TestS2PublishRacingWriterIsNeverStaleAtCommit(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedFragment(ctx, db, "f1"); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Publish(ctx, db, PublishOptions{Release: "r1", Actor: "p", Pins: map[string]int64{"f1": 0}, Artifacts: 2,
			Hold: 300 * time.Millisecond, OnHold: func(string) { close(held) }})
		done <- err
	}()
	<-held
	if _, err := UpdateFragment(ctx, db, "f1", "w", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want(t, read(t, db, "s2.sql"), "stale_at_commit", "0")
}

// The control for the test above: the same race with the source comparison unlocked. PostgreSQL
// lets the writer commit inside the hold, so the release commits on a superseded revision; SQLite
// still makes the writer wait, because its IMMEDIATE transaction is the lock.
func TestS2UnlockedSourceCheckControl(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedFragment(ctx, db, "f1"); err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Publish(ctx, db, PublishOptions{Release: "r1", Actor: "p", Pins: map[string]int64{"f1": 0}, Artifacts: 2,
			NoSourceLock: true, Hold: 300 * time.Millisecond, OnHold: func(string) { close(held) }})
		done <- err
	}()
	<-held
	if _, err := UpdateFragment(ctx, db, "f1", "w", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stale := "0"
	if db.D == Postgres {
		stale = "1"
	}
	want(t, read(t, db, "s2.sql"), "stale_at_commit", stale)
}

func TestS2PublishRetryIsIdempotent(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedFragment(ctx, db, "f1"); err != nil {
		t.Fatal(err)
	}
	opt := PublishOptions{Release: "r1", Actor: "t", Pins: map[string]int64{"f1": 0}, Artifacts: 3}
	first, err := Publish(ctx, db, opt)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Publish(ctx, db, opt)
	if err != nil || again.ID != first.ID || !again.Existing {
		t.Fatalf("retry: %+v, %v; want the existing release %d", again, err, first.ID)
	}
	opt.Artifacts = 4
	if _, err := Publish(ctx, db, opt); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry with different content: %v, want ErrConflict", err)
	}
}

func TestS3OneActiveIntentPerScope(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	const clients = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created int
		busy    int
		other   []error
	)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := CreateIntent(ctx, db, "m1", "k"+string(rune('a'+i)), "c"+string(rune('a'+i)))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && res.Created:
				created++
			case errors.Is(err, ErrScopeBusy):
				busy++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()
	if created != 1 || busy != clients-1 || len(other) != 0 {
		t.Fatalf("created %d busy %d other %v", created, busy, other)
	}
	r := read(t, db, "s3.sql")
	want(t, r, "operations", "1")
	want(t, r, "scopes_with_two_active", "0")
}

func TestS3RetryReturnsTheSameIntent(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	a, err := CreateIntent(ctx, db, "m1", "k1", "c1")
	if err != nil || !a.Created {
		t.Fatal(a, err)
	}
	b, err := CreateIntent(ctx, db, "m1", "k1", "c1")
	if err != nil || b.Created || b.ID != a.ID {
		t.Fatalf("retry: %+v %v, want existing %d", b, err, a.ID)
	}
	if err := FinishOperation(ctx, db, a.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	c, err := CreateIntent(ctx, db, "m1", "k2", "c2")
	if err != nil || !c.Created {
		t.Fatalf("new intent after completion: %+v %v", c, err)
	}
	want(t, read(t, db, "s3.sql"), "operations", "2")
}

func TestS4ConcurrentTakeoverHasOneWinner(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	op, err := CreateIntent(ctx, db, "m1", "k1", "o0")
	if err != nil {
		t.Fatal(err)
	}
	const contenders = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
		lost int
	)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := TakeOver(ctx, db, op.ID, "o"+string(rune('1'+i)), 1)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if errors.Is(err, ErrFenced) {
				lost++
			} else {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || lost != contenders-1 {
		t.Fatalf("wins %d lost %d", wins, lost)
	}
	want(t, read(t, db, "s4.sql"), "takeovers", "1")
}

func TestS4StaleOwnerIsFenced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	op, err := CreateIntent(ctx, db, "m1", "k1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TakeOver(ctx, db, op.ID, "b", 1); err != nil {
		t.Fatal(err)
	}
	if err := RecordAttempt(ctx, db, AttemptOptions{Op: op.ID, Owner: "a", Gen: 1}); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale owner's attempt: %v, want ErrFenced", err)
	}
	if err := RecordAttempt(ctx, db, AttemptOptions{Op: op.ID, Owner: "b", Gen: 2}); err != nil {
		t.Fatal(err)
	}
	r := read(t, db, "s4.sql")
	want(t, r, "attempts", "1")
	want(t, r, "stale_attempts", "0")
}

// The check-then-insert attempt reads ownership, then records the attempt later. On PostgreSQL a
// takeover lands in between and the attempt is recorded stale; on SQLite the IMMEDIATE
// transaction makes the takeover wait, so it is not.
func TestS4CheckThenInsertAttempt(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	op, err := CreateIntent(ctx, db, "m1", "k1", "a")
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RecordAttempt(ctx, db, AttemptOptions{Op: op.ID, Owner: "a", Gen: 1, CheckThenInsert: true,
			Hold: 300 * time.Millisecond, OnHold: func(string) { close(held) }})
	}()
	<-held
	if _, err := TakeOver(ctx, db, op.ID, "b", 1); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	stale := "0"
	if db.D == Postgres {
		stale = "1"
	}
	want(t, read(t, db, "s4.sql"), "stale_attempts", stale)
}

func TestS5WorkersClaimEachJobOnce(t *testing.T) {
	modes := []ClaimMode{ClaimGuarded}
	if os.Getenv("E4_TEST_PG_DSN") != "" {
		modes = append(modes, ClaimSkipLocked)
	}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			db := migrated(t)
			ctx := context.Background()
			if err := SeedJobs(ctx, db, 60); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 4)
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := Work(ctx, db, WorkOptions{Worker: "w" + string(rune('0'+i)), Mode: mode, Lease: time.Minute})
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			r := read(t, db, "s5.sql")
			want(t, r, "done", "60")
			want(t, r, "claims", "60")
			want(t, r, "jobs_claimed_twice", "0")
			want(t, r, "completions", "60")
			want(t, r, "jobs_completed_twice", "0")
		})
	}
}

func TestS5ExpiredLeaseIsReclaimedAndLateCompletionFenced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := SeedJobs(ctx, db, 1); err != nil {
		t.Fatal(err)
	}
	a, err := Claim(ctx, db, "a", ClaimGuarded, 200*time.Millisecond)
	if err != nil || !a.OK {
		t.Fatal(a, err)
	}
	if b, err := Claim(ctx, db, "b", ClaimGuarded, time.Minute); err != nil || b.OK {
		t.Fatalf("claim of a job under an unexpired lease: %+v %v", b, err)
	}
	time.Sleep(300 * time.Millisecond)
	b, err := Claim(ctx, db, "b", ClaimGuarded, time.Minute)
	if err != nil || !b.OK || b.Job != a.Job || b.Fence != a.Fence+1 {
		t.Fatalf("reclaim after expiry: %+v %v (first %+v)", b, err, a)
	}
	if ok, err := Complete(ctx, db, a.Job, a.Fence, "a"); err != nil || ok {
		t.Fatalf("late completion on the old fence: %v %v, want refused", ok, err)
	}
	if ok, err := Complete(ctx, db, b.Job, b.Fence, "b"); err != nil || !ok {
		t.Fatalf("completion by the current holder: %v %v", ok, err)
	}
	r := read(t, db, "s5-jobs.sql")
	want(t, r, "job:1", "done 2 b 2")
}

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Outcomes a scenario reports as results rather than failures.
var (
	// ErrStale: a publication pinned a source revision that is no longer current.
	ErrStale = errors.New("stale source revision")
	// ErrConflict: an idempotent retry names an existing record with different content.
	ErrConflict = errors.New("existing record differs")
	// ErrScopeBusy: another operation is active on the machine scope.
	ErrScopeBusy = errors.New("machine scope busy")
	// ErrFenced: the caller's ownership generation or claim fence is not current.
	ErrFenced = errors.New("fenced: ownership or claim not current")
)

// ---- S1 stale revision rejection ----

func SeedFragment(ctx context.Context, db *DB, name string) error {
	_, err := db.Exec(ctx, "INSERT INTO fragment (name, revision, body) VALUES (?, 0, 'seed')", name)
	return err
}

func CurrentRevision(ctx context.Context, db *DB, name string) (int64, error) {
	var rev int64
	err := db.QueryRow(ctx, "SELECT revision FROM fragment WHERE name = ?", name).Scan(&rev)
	return rev, err
}

// UpdateFragment writes a new body over the revision the caller read. It is a compare-and-set:
// the UPDATE matches only while the revision is still the one read, and the winner appends its
// revision to the ledger in the same transaction. blind is the control: it sets the revision it
// computed from its read without comparing, as a last-writer-wins store would.
func UpdateFragment(ctx context.Context, db *DB, name, actor string, read int64, blind bool) (won bool, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackUnlessDone(tx, &err)
	body := fmt.Sprintf("revision %d by %s", read+1, actor)
	var res sql.Result
	if blind {
		res, err = tx.Exec(ctx, "UPDATE fragment SET revision = ?, body = ? WHERE name = ?", read+1, body, name)
	} else {
		res, err = tx.Exec(ctx, "UPDATE fragment SET revision = revision + 1, body = ? WHERE name = ? AND revision = ?", body, name, read)
	}
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		_ = tx.Rollback()
		return false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO ledger (kind, subject, number, actor) VALUES ('revision', ?, ?, ?)", name, read+1, actor); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type RaceOptions struct {
	Fragment string
	Writers  int
	Rounds   int
	Actor    string
	Blind    bool
}

type RaceStats struct {
	Wins, Conflicts, Busy int
}

// RaceRevisions runs Rounds rounds in which every writer reads the current revision, all wait
// until every one has read, and then all write over what they read: every writer but the first
// to commit holds a stale revision.
func RaceRevisions(ctx context.Context, db *DB, opt RaceOptions) (RaceStats, error) {
	var st RaceStats
	for r := 0; r < opt.Rounds; r++ {
		var (
			read, wrote sync.WaitGroup
			start       = make(chan struct{})
			mu          sync.Mutex
			firstErr    error
		)
		for w := 0; w < opt.Writers; w++ {
			read.Add(1)
			wrote.Add(1)
			go func(w int) {
				defer wrote.Done()
				rev, err := CurrentRevision(ctx, db, opt.Fragment)
				read.Done()
				<-start
				won := false
				if err == nil {
					won, err = UpdateFragment(ctx, db, opt.Fragment, fmt.Sprintf("%s/w%d", opt.Actor, w), rev, opt.Blind)
				}
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil && won:
					st.Wins++
				case err == nil:
					st.Conflicts++
				case Classify(err) == ClassBusy:
					st.Busy++
				case firstErr == nil:
					firstErr = err
				}
			}(w)
		}
		read.Wait()
		close(start)
		wrote.Wait()
		if firstErr != nil {
			return st, firstErr
		}
	}
	return st, nil
}

// ---- S2 all-or-nothing publication ----

type PublishOptions struct {
	Release   string
	Actor     string
	Pins      map[string]int64 // source fragment → the revision the publication was built from
	Artifacts int
	// FailAt aborts the publication before artifact FailAt, as a compile or encryption failure
	// would.
	FailAt int
	// Hold holds the transaction open after half the artifacts are written, calling OnHold first.
	Hold   time.Duration
	OnHold func(stage string)
	// BeforeCommit runs immediately before COMMIT; the commit-unknown row pauses the server there.
	BeforeCommit func()
	// NoSourceLock is the control: the source revisions are compared without locking them.
	NoSourceLock bool
}

type Published struct {
	ID       int64
	Existing bool
}

// Digest is the release's content identity: an idempotent retry must carry the same one.
func (o PublishOptions) Digest() string {
	names := make([]string, 0, len(o.Pins))
	for n := range o.Pins {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%d\n", o.Release, o.Artifacts)
	for _, n := range names {
		fmt.Fprintf(h, "%s=%d\n", n, o.Pins[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Publish commits a release, its source pins and every artifact in one transaction, or nothing.
// The source revisions are compared inside the transaction and kept from changing until it
// commits: FOR SHARE on PostgreSQL, where READ COMMITTED would otherwise let a writer commit
// between the comparison and the COMMIT; on SQLite the IMMEDIATE transaction already excludes
// every other writer. A publication whose name exists is a retry: the existing release is
// returned if its digest matches, ErrConflict if not.
func Publish(ctx context.Context, db *DB, opt PublishOptions) (p Published, err error) {
	digest := opt.Digest()
	p, err = publishTx(ctx, db, opt, digest)
	if Classify(err) == ClassUnique {
		return existingRelease(ctx, db, opt.Release, digest)
	}
	return p, err
}

func existingRelease(ctx context.Context, db *DB, name, digest string) (Published, error) {
	var (
		id  int64
		got string
	)
	if err := db.QueryRow(ctx, "SELECT id, digest FROM release WHERE name = ?", name).Scan(&id, &got); err != nil {
		return Published{}, err
	}
	if got != digest {
		return Published{ID: id, Existing: true}, ErrConflict
	}
	return Published{ID: id, Existing: true}, nil
}

func publishTx(ctx context.Context, db *DB, opt PublishOptions, digest string) (p Published, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer rollbackUnlessDone(tx, &err)

	var n int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM release WHERE name = ?", opt.Release).Scan(&n); err != nil {
		return p, err
	}
	if n != 0 {
		_ = tx.Rollback()
		return existingRelease(ctx, db, opt.Release, digest)
	}

	names := make([]string, 0, len(opt.Pins))
	for name := range opt.Pins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		q := "SELECT revision FROM fragment WHERE name = ?"
		if db.D == Postgres && !opt.NoSourceLock {
			q += " FOR SHARE"
		}
		var rev int64
		if err = tx.QueryRow(ctx, q, name).Scan(&rev); err != nil {
			return p, fmt.Errorf("source %s: %w", name, err)
		}
		if rev != opt.Pins[name] {
			err = fmt.Errorf("%w: %s is at %d, publication pinned %d", ErrStale, name, rev, opt.Pins[name])
			return p, err
		}
	}

	if err = tx.QueryRow(ctx, "INSERT INTO release (name, digest, artifact_count, publisher) VALUES (?, ?, ?, ?) RETURNING id",
		opt.Release, digest, opt.Artifacts, opt.Actor).Scan(&p.ID); err != nil {
		return p, err
	}
	for _, name := range names {
		if _, err = tx.Exec(ctx, "INSERT INTO release_source (release_id, fragment, revision) VALUES (?, ?, ?)", p.ID, name, opt.Pins[name]); err != nil {
			return p, err
		}
	}
	for i := 1; i <= opt.Artifacts; i++ {
		if i == opt.FailAt {
			err = fmt.Errorf("injected failure before artifact %d of %d", i, opt.Artifacts)
			return p, err
		}
		// A stand-in for an encrypted artifact: synthetic bytes, not a secret.
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/m%d", opt.Release, i)))
		if _, err = tx.Exec(ctx, "INSERT INTO release_artifact (release_id, machine, ciphertext) VALUES (?, ?, ?)",
			p.ID, fmt.Sprintf("m%d", i), sum[:]); err != nil {
			return p, err
		}
		if opt.Hold > 0 && i == (opt.Artifacts+1)/2 {
			if opt.OnHold != nil {
				opt.OnHold(fmt.Sprintf("after artifact %d of %d", i, opt.Artifacts))
			}
			time.Sleep(opt.Hold)
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO ledger (kind, subject, number, actor) VALUES ('publish', ?, ?, ?)", opt.Release, p.ID, opt.Actor); err != nil {
		return p, err
	}
	if opt.BeforeCommit != nil {
		opt.BeforeCommit()
	}
	return p, tx.Commit()
}

// ---- S3 unique operation intent ----

type Intent struct {
	ID      int64
	Created bool
}

// CreateIntent records a durable operation intent in state committed. The partial unique index
// allows one active operation per machine scope; the idempotency key allows one intent per key,
// so a retry returns the intent it already made.
func CreateIntent(ctx context.Context, db *DB, machine, key, owner string) (Intent, error) {
	in, err := createIntentTx(ctx, db, machine, key, owner)
	if Classify(err) != ClassUnique {
		return in, err
	}
	var (
		id int64
		m  string
	)
	switch err := db.QueryRow(ctx, "SELECT id, machine FROM operation WHERE idem_key = ?", key).Scan(&id, &m); {
	case errors.Is(err, sql.ErrNoRows):
		return Intent{}, ErrScopeBusy
	case err != nil:
		return Intent{}, err
	case m != machine:
		return Intent{ID: id}, ErrConflict
	}
	return Intent{ID: id}, nil
}

func createIntentTx(ctx context.Context, db *DB, machine, key, owner string) (in Intent, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer rollbackUnlessDone(tx, &err)
	if err = tx.QueryRow(ctx, "INSERT INTO operation (machine, idem_key, state, owner, owner_gen) VALUES (?, ?, 'committed', ?, 1) RETURNING id",
		machine, key, owner).Scan(&in.ID); err != nil {
		return in, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO timeline (operation_id, kind, actor, owner_gen) VALUES (?, 'intent', ?, 1)", in.ID, owner); err != nil {
		return in, err
	}
	in.Created = true
	return in, tx.Commit()
}

func FinishOperation(ctx context.Context, db *DB, id int64, state string) error {
	_, err := db.Exec(ctx, "UPDATE operation SET state = ? WHERE id = ?", state, id)
	return err
}

// ---- S4 ownership transitions ----

// TakeOver moves the operation to newOwner if its generation is still expect: a compare-and-set
// on owner_gen, which is the fencing token.
func TakeOver(ctx context.Context, db *DB, op int64, newOwner string, expect int64) (gen int64, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer rollbackUnlessDone(tx, &err)
	res, err := tx.Exec(ctx, "UPDATE operation SET owner = ?, owner_gen = owner_gen + 1 WHERE id = ? AND owner_gen = ?", newOwner, op, expect)
	if err != nil {
		return 0, err
	}
	if n, e := res.RowsAffected(); e != nil || n == 0 {
		err = errors.Join(ErrFenced, e)
		return 0, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO timeline (operation_id, kind, actor, owner_gen) VALUES (?, 'takeover', ?, ?)", op, newOwner, expect+1); err != nil {
		return 0, err
	}
	return expect + 1, tx.Commit()
}

type AttemptOptions struct {
	Op    int64
	Owner string
	Gen   int64
	// CheckThenInsert is the control: read the ownership, then record the attempt, without
	// holding anything between the two.
	CheckThenInsert bool
	Hold            time.Duration
	OnHold          func(stage string)
}

// RecordAttempt records an attempt only while Owner still holds the operation at Gen. The fenced
// form makes the ownership check the first write of the transaction — a conditional UPDATE of the
// operation row, which PostgreSQL re-evaluates against a concurrently committed takeover and which
// holds the row until COMMIT — so no takeover can be ordered between the check and the attempt.
func RecordAttempt(ctx context.Context, db *DB, opt AttemptOptions) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackUnlessDone(tx, &err)
	if opt.CheckThenInsert {
		var (
			owner string
			gen   int64
		)
		if err = tx.QueryRow(ctx, "SELECT owner, owner_gen FROM operation WHERE id = ?", opt.Op).Scan(&owner, &gen); err != nil {
			return err
		}
		if owner != opt.Owner || gen != opt.Gen {
			err = ErrFenced
			return err
		}
	} else {
		res, e := tx.Exec(ctx, "UPDATE operation SET attempts = attempts + 1 WHERE id = ? AND owner = ? AND owner_gen = ?", opt.Op, opt.Owner, opt.Gen)
		if e != nil {
			err = e
			return err
		}
		if n, e := res.RowsAffected(); e != nil || n == 0 {
			err = errors.Join(ErrFenced, e)
			return err
		}
	}
	if opt.Hold > 0 {
		if opt.OnHold != nil {
			opt.OnHold("after the ownership check")
		}
		time.Sleep(opt.Hold)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO timeline (operation_id, kind, actor, owner_gen) VALUES (?, 'attempt', ?, ?)", opt.Op, opt.Owner, opt.Gen); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- S5 queue claims ----

type ClaimMode string

const (
	// ClaimGuarded is portable: the UPDATE picks the first eligible job and repeats the
	// eligibility test on the row it updates, so a claimer that lost the row to a concurrent
	// claim matches nothing and tries again.
	ClaimGuarded ClaimMode = "guarded"
	// ClaimSkipLocked is PostgreSQL's FOR UPDATE SKIP LOCKED: claimers pass over locked rows.
	ClaimSkipLocked ClaimMode = "skip-locked"
	// ClaimNaive is the control: the guarded form without the repeated eligibility test.
	ClaimNaive ClaimMode = "naive"
)

func SeedJobs(ctx context.Context, db *DB, n int) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackUnlessDone(tx, &err)
	for i := 0; i < n; i++ {
		if _, err = tx.Exec(ctx, "INSERT INTO job (state) VALUES ('pending')"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type Claimed struct {
	OK         bool
	Job, Fence int64
}

const eligible = "(state = 'pending' OR (state = 'claimed' AND lease_until < ?))"

// Claim takes the first eligible job — pending, or claimed under an expired lease — for lease,
// incrementing its fence, and records the claim. Lease times are the claimer's clock in
// milliseconds; the report names that as a limitation.
func Claim(ctx context.Context, db *DB, worker string, mode ClaimMode, lease time.Duration) (c Claimed, err error) {
	now := time.Now().UnixMilli()
	until := now + lease.Milliseconds()
	pick := "SELECT id FROM job WHERE " + eligible + " ORDER BY id LIMIT 1"
	var (
		q    string
		args = []any{worker, until, now}
	)
	switch mode {
	case ClaimGuarded:
		q = "UPDATE job SET state = 'claimed', claimed_by = ?, fence = fence + 1, lease_until = ? WHERE id = (" + pick + ") AND " + eligible + " RETURNING id, fence"
		args = append(args, now)
	case ClaimNaive:
		q = "UPDATE job SET state = 'claimed', claimed_by = ?, fence = fence + 1, lease_until = ? WHERE id = (" + pick + ") RETURNING id, fence"
	case ClaimSkipLocked:
		if db.D != Postgres {
			return c, fmt.Errorf("claim mode %s: PostgreSQL only", mode)
		}
		q = "UPDATE job SET state = 'claimed', claimed_by = ?, fence = fence + 1, lease_until = ? WHERE id = (" + pick + " FOR UPDATE SKIP LOCKED) RETURNING id, fence"
	default:
		return c, fmt.Errorf("unknown claim mode %q", mode)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer rollbackUnlessDone(tx, &err)
	switch err = tx.QueryRow(ctx, q, args...).Scan(&c.Job, &c.Fence); {
	case errors.Is(err, sql.ErrNoRows):
		err = tx.Rollback()
		return c, err
	case err != nil:
		return c, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO job_event (job_id, worker, fence, action) VALUES (?, ?, ?, 'claim')", c.Job, worker, c.Fence); err != nil {
		return c, err
	}
	c.OK = true
	return c, tx.Commit()
}

// Complete marks the job done only while fence is still its current one.
func Complete(ctx context.Context, db *DB, job, fence int64, worker string) (ok bool, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackUnlessDone(tx, &err)
	res, err := tx.Exec(ctx, "UPDATE job SET state = 'done', completed_by = ?, completed_fence = ? WHERE id = ? AND fence = ? AND state = 'claimed'",
		worker, fence, job, fence)
	if err != nil {
		return false, err
	}
	if n, e := res.RowsAffected(); e != nil || n == 0 {
		err = errors.Join(tx.Rollback(), e)
		return false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO job_event (job_id, worker, fence, action) VALUES (?, ?, ?, 'complete')", job, worker, fence); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

type WorkOptions struct {
	Worker string
	Mode   ClaimMode
	Lease  time.Duration
}

type WorkStats struct {
	Claims, Completed, Refused, Retries, Busy int
	Elapsed                                   time.Duration
}

// Work claims and completes jobs until none is eligible. A claim that matches nothing while jobs
// remain eligible lost its row to another claimer and is retried; SQLITE_BUSY is counted and
// retried.
func Work(ctx context.Context, db *DB, opt WorkOptions) (st WorkStats, err error) {
	start := time.Now()
	defer func() { st.Elapsed = time.Since(start) }()
	for {
		c, err := Claim(ctx, db, opt.Worker, opt.Mode, opt.Lease)
		if Classify(err) == ClassBusy {
			st.Busy++
			continue
		}
		if err != nil {
			return st, err
		}
		if !c.OK {
			var n int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM job WHERE "+eligible, time.Now().UnixMilli()).Scan(&n); err != nil {
				return st, err
			}
			if n == 0 {
				return st, nil
			}
			st.Retries++
			continue
		}
		st.Claims++
		ok, err := Complete(ctx, db, c.Job, c.Fence, opt.Worker)
		if err != nil {
			return st, err
		}
		if ok {
			st.Completed++
		} else {
			st.Refused++
		}
	}
}

// rollbackUnlessDone rolls tx back when the function it guards returns an error. A rollback after
// a commit or an earlier rollback is a no-op.
func rollbackUnlessDone(tx *Tx, err *error) {
	if *err != nil {
		_ = tx.Rollback()
	}
}

// ReaderStatements splits a readers/*.sql file into its statements: comment lines are dropped
// and a statement ends at a line ending in a semicolon.
func ReaderStatements(body string) []string {
	var (
		out []string
		cur []string
	)
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		if strings.HasSuffix(t, ";") {
			cur = append(cur, strings.TrimSuffix(t, ";"))
			out = append(out, strings.Join(cur, " "))
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	return out
}

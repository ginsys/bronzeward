// Command e4db is Phase-0 evidence code for the database-semantics experiment (ginsys/bronzeward
// issue 6). It is not the v1 implementation and selects no driver, ORM or schema.
//
// Each subcommand is one client action against one backend. The run scripts start several at once
// and interrupt them, and read the outcome back from the database with psql or the sqlite3 CLI;
// what e4db prints is its own transcript, never the verdict.
//
// The backend comes from the environment, never argv, because the PostgreSQL DSN carries the
// password and a process's argv is readable by every user through /proc:
//
//	E4_BACKEND      postgres | sqlite
//	E4_PG_DSN       lib/pq connection string (postgres)
//	E4_SQLITE_PATH  database file (sqlite)
//	E4_SQLITE_TXLOCK  immediate (default) | deferred, the control for the IMMEDIATE default
//
// Usage:
//
//	e4db version
//	e4db migrate [-runner R] [-upto N] [-fail-at N] [-hold-at N -hold D] [-no-lock]
//	e4db seed-fragment -fragment F
//	e4db race -fragment F -writers W -rounds R -actor A [-blind]
//	e4db update -fragment F -actor A [-read N]
//	e4db publish -release R -actor A -pin F=N... -artifacts N [-fail-at K] [-hold D] [-no-source-lock] [-commit-gate FILE]
//	e4db intent -machine M -key K -owner O
//	e4db finish -op ID -state S
//	e4db takeover -op ID -owner O -expect G
//	e4db attempt -op ID -owner O -gen G [-check-then-insert] [-hold D]
//	e4db seed-jobs -n N
//	e4db work -worker W -mode guarded|skip-locked|naive [-lease D]
//	e4db claim -worker W -mode M [-lease D]
//	e4db complete -job J -fence F -worker W
//
// Every subcommand also takes -start-at <unix milliseconds>, a start barrier for processes launched
// together. Output is key=value lines. Exit status: 0 done, 3 refused by the semantics under test
// (stale, conflict, scope busy, fenced, nothing to claim), 1 any error, printed with its class.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

const (
	exitDone    = 0
	exitError   = 1
	exitUsage   = 2
	exitRefused = 3
)

// errNoJob: a claim found nothing eligible.
var errNoJob = errors.New("no eligible job")

type pins map[string]int64

func (p pins) String() string { return fmt.Sprint(map[string]int64(p)) }
func (p pins) Set(s string) error {
	name, rev, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("want F=N, got %q", s)
	}
	n, err := strconv.ParseInt(rev, 10, 64)
	if err != nil {
		return err
	}
	p[name] = n
	return nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "e4db: no subcommand; see the package comment")
		return exitUsage
	}
	cmd := args[0]
	fs := flag.NewFlagSet("e4db "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		startAt   = fs.Int64("start-at", 0, "start barrier, unix milliseconds")
		runner    = fs.String("runner", "", "migrate: this runner's name")
		failAt    = fs.Int("fail-at", 0, "migrate: fail inside migration N; publish: fail before artifact N")
		holdAt    = fs.Int("hold-at", 0, "migrate: hold migration N open")
		hold      = fs.Duration("hold", 0, "hold the transaction open this long")
		noLock    = fs.Bool("no-lock", false, "migrate: control, no advisory lock")
		upto      = fs.Int("upto", 0, "migrate: apply migrations up to N only")
		fragment  = fs.String("fragment", "f1", "fragment name")
		writers   = fs.Int("writers", 4, "race: concurrent writers")
		rounds    = fs.Int("rounds", 10, "race: rounds")
		actor     = fs.String("actor", "", "writer or publisher name")
		blind     = fs.Bool("blind", false, "race, update: control, write without comparing the revision")
		readRev   = fs.Int64("read", -1, "update: the revision read (default: read it now)")
		release   = fs.String("release", "", "publish: release name")
		artifacts = fs.Int("artifacts", 4, "publish: artifact count")
		noSrcLock = fs.Bool("no-source-lock", false, "publish: control, compare sources without locking them")
		gate      = fs.String("commit-gate", "", "publish: before COMMIT, print commit-ready and wait for this file")
		machine   = fs.String("machine", "m1", "intent: machine scope")
		key       = fs.String("key", "", "intent: idempotency key")
		owner     = fs.String("owner", "", "intent, takeover, attempt: owner")
		op        = fs.Int64("op", 0, "operation id")
		state     = fs.String("state", "completed", "finish: terminal state")
		expect    = fs.Int64("expect", 0, "takeover: expected generation")
		gen       = fs.Int64("gen", 0, "attempt: the generation held")
		checkIns  = fs.Bool("check-then-insert", false, "attempt: control, check ownership then insert")
		n         = fs.Int("n", 0, "seed-jobs: count")
		worker    = fs.String("worker", "", "work, claim, complete: worker name")
		mode      = fs.String("mode", string(ClaimGuarded), "work, claim: claim mode")
		lease     = fs.Duration("lease", time.Minute, "work, claim: lease")
		job       = fs.Int64("job", 0, "complete: job id")
		fence     = fs.Int64("fence", 0, "complete: fence")
	)
	pinned := pins{}
	fs.Var(pinned, "pin", "publish: F=N, a source fragment and the revision it was built from (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	out := func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }

	db, err := openFromEnv(ctx)
	if err != nil {
		out("outcome=error class=%s err=%q", Classify(err), err.Error())
		return exitError
	}
	defer db.Close()
	out("backend=%s pid=%d", db.D, os.Getpid())
	if *startAt > 0 {
		time.Sleep(time.Until(time.UnixMilli(*startAt)))
	}
	onHold := func(stage string) { out("hold=%q for=%s", stage, *hold) }
	began := time.Now()
	res := func(err error, format string, a ...any) int {
		elapsed := time.Since(began).Milliseconds()
		for _, refusal := range []struct {
			err  error
			name string
		}{{ErrStale, "stale"}, {ErrConflict, "conflict"}, {ErrScopeBusy, "scope-busy"}, {ErrFenced, "fenced"}, {errNoJob, "no-job"}} {
			if errors.Is(err, refusal.err) {
				out("outcome=refused reason=%s elapsed_ms=%d err=%q", refusal.name, elapsed, err.Error())
				return exitRefused
			}
		}
		if err != nil {
			out("outcome=error class=%s elapsed_ms=%d err=%q", Classify(err), elapsed, err.Error())
			return exitError
		}
		out("outcome=done elapsed_ms=%d "+format, append([]any{elapsed}, a...)...)
		return exitDone
	}

	switch cmd {
	case "version":
		v, err := db.EngineVersion(ctx)
		return res(err, "engine=%q", v)
	case "migrate":
		ms, err := Migrations(db.D)
		if err != nil {
			return res(err, "")
		}
		if *upto > 0 && *upto < len(ms) {
			ms = ms[:*upto]
		}
		applied, err := Migrate(ctx, db, ms, MigrateOptions{Runner: *runner, FailAt: *failAt, HoldAt: *holdAt, Hold: *hold,
			NoLock: *noLock, OnHold: func(v int) { onHold(fmt.Sprintf("migration %d", v)) }})
		return res(err, "applied=%v", applied)
	case "seed-fragment":
		return res(SeedFragment(ctx, db, *fragment), "fragment=%s", *fragment)
	case "race":
		st, err := RaceRevisions(ctx, db, RaceOptions{Fragment: *fragment, Writers: *writers, Rounds: *rounds, Actor: *actor, Blind: *blind})
		return res(err, "wins=%d conflicts=%d busy=%d", st.Wins, st.Conflicts, st.Busy)
	case "update":
		rev := *readRev
		if rev < 0 {
			if rev, err = CurrentRevision(ctx, db, *fragment); err != nil {
				return res(err, "")
			}
		}
		won, err := UpdateFragment(ctx, db, *fragment, *actor, rev, *blind)
		if err == nil && !won {
			err = fmt.Errorf("%w: %s is no longer at %d", ErrStale, *fragment, rev)
		}
		return res(err, "fragment=%s revision=%d", *fragment, rev+1)
	case "publish":
		opt := PublishOptions{Release: *release, Actor: *actor, Pins: pinned, Artifacts: *artifacts, FailAt: *failAt,
			Hold: *hold, OnHold: onHold, NoSourceLock: *noSrcLock}
		if *gate != "" {
			opt.BeforeCommit = func() {
				out("commit-ready")
				for {
					if _, err := os.Stat(*gate); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				out("commit-sent")
			}
		}
		p, err := Publish(ctx, db, opt)
		return res(err, "release=%s id=%d existing=%t digest=%s", *release, p.ID, p.Existing, opt.Digest())
	case "intent":
		in, err := CreateIntent(ctx, db, *machine, *key, *owner)
		return res(err, "op=%d created=%t", in.ID, in.Created)
	case "finish":
		return res(FinishOperation(ctx, db, *op, *state), "op=%d state=%s", *op, *state)
	case "takeover":
		g, err := TakeOver(ctx, db, *op, *owner, *expect)
		return res(err, "op=%d owner=%s gen=%d", *op, *owner, g)
	case "attempt":
		err := RecordAttempt(ctx, db, AttemptOptions{Op: *op, Owner: *owner, Gen: *gen, CheckThenInsert: *checkIns, Hold: *hold, OnHold: onHold})
		return res(err, "op=%d owner=%s gen=%d", *op, *owner, *gen)
	case "seed-jobs":
		return res(SeedJobs(ctx, db, *n), "jobs=%d", *n)
	case "work":
		st, err := Work(ctx, db, WorkOptions{Worker: *worker, Mode: ClaimMode(*mode), Lease: *lease})
		return res(err, "claims=%d completed=%d refused=%d retries=%d busy=%d work_ms=%d",
			st.Claims, st.Completed, st.Refused, st.Retries, st.Busy, st.Elapsed.Milliseconds())
	case "claim":
		c, err := Claim(ctx, db, *worker, ClaimMode(*mode), *lease)
		if err == nil && !c.OK {
			err = errNoJob
		}
		return res(err, "job=%d fence=%d", c.Job, c.Fence)
	case "complete":
		ok, err := Complete(ctx, db, *job, *fence, *worker)
		if err == nil && !ok {
			err = fmt.Errorf("%w: job %d is not claimed at fence %d", ErrFenced, *job, *fence)
		}
		return res(err, "job=%d fence=%d", *job, *fence)
	}
	fmt.Fprintf(stderr, "e4db: unknown subcommand %q\n", cmd)
	return exitUsage
}

func openFromEnv(ctx context.Context) (*DB, error) {
	switch b := os.Getenv("E4_BACKEND"); b {
	case "postgres":
		dsn := os.Getenv("E4_PG_DSN")
		if dsn == "" {
			return nil, errors.New("E4_PG_DSN is empty")
		}
		return OpenPostgres(ctx, dsn)
	case "sqlite":
		path := os.Getenv("E4_SQLITE_PATH")
		if path == "" {
			return nil, errors.New("E4_SQLITE_PATH is empty")
		}
		txlock := os.Getenv("E4_SQLITE_TXLOCK")
		if txlock == "" {
			txlock = "immediate"
		}
		return openSQLite(ctx, path, sqliteBusyTimeoutMS, txlock)
	default:
		return nil, fmt.Errorf("E4_BACKEND %q: want postgres or sqlite", b)
	}
}

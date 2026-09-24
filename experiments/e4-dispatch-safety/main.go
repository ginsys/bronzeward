// Command e4x is the E4 dispatch-safety prototype: the specification's commitment and attempt
// transactions (§3.2, §3.3) and operation states (§4) over PostgreSQL, sending one Talos
// machine-configuration apply to the fixture's worker. The harness in run/ drives it through
// gates (gate.go) and judges each row from the database, the worker and the fault log, not from
// what e4x prints.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	exitCompleted  = 0
	exitError      = 1
	exitUsage      = 2
	exitRefused    = 3
	exitUnresolved = 4
	exitTerminal   = 5 // a terminal state other than completed: rejected, failed or cancelled
)

// dbRetryLimit is how long a recording step retries a database error before giving up: long
// enough to outlast the harness's PostgreSQL restart.
const dbRetryLimit = 90 * time.Second

type env struct {
	store *Store
	talos Talos
	gates Gates
	log   *Log
}

func main() { os.Exit(run(os.Args[1:])) }

func usage() int {
	fmt.Fprintln(os.Stderr, "usage: e4x setup|plan|approve|revoke|run|takeover|recover|account|state [flags]")
	return exitUsage
}

func run(args []string) int {
	if len(args) == 0 {
		return usage()
	}
	cmd, fs := args[0], flag.NewFlagSet(args[0], flag.ContinueOnError)
	var (
		id          = fs.String("id", "", "plan and operation id")
		actor       = fs.String("actor", "harness", "actor recorded on the timeline")
		machine     = fs.String("machine", "m1", "machine id")
		noScope     = fs.Bool("no-scope", false, "setup: omit the machine-scope index (the A-after-B control)")
		artifact    = fs.String("artifact", "", "plan: machine-configuration file to apply")
		expires     = fs.Duration("expires", time.Hour, "plan: approval validity")
		maxAttempts = fs.Int("max-attempts", 2, "plan: bound on attempts")
		verify      = fs.Duration("verify", 60*time.Second, "plan: attempt deadline after it is recorded")
		maxAge      = fs.Duration("max-age", 30*time.Second, "plan: maximum age of the execution-time observation")
		transport   = fs.Duration("transport", 15*time.Second, "plan: bound on one send")
		mode        = fs.String("mode", string(ModeProtocol), "run: protocol, naive or nofence")
		retry       = fs.Bool("retry", false, "recover: retry when a retry is classified safe")
		n           = fs.Int("n", 0, "account: attempt number")
		evidence    = fs.String("evidence", "", "account: why the attempt's sender can no longer send")
	)
	if err := fs.Parse(args[1:]); err != nil || *id == "" && cmd != "setup" {
		return usage()
	}
	if cmd == "run" || cmd == "recover" {
		*actor = os.Getenv("E4X_OWNER")
		if *actor == "" {
			fmt.Fprintln(os.Stderr, "e4x: E4X_OWNER names the executor")
			return exitUsage
		}
	}
	log := NewLog(os.Stdout, *actor)
	ctx := context.Background()
	store, err := OpenStore(ctx, os.Getenv("E4X_PG_DSN"), log)
	if err != nil {
		log.Printf("event=error err=%q", err)
		return exitError
	}
	defer func() { _ = store.Close() }()
	e := env{
		store: store,
		talos: Talos{Bin: os.Getenv("E4X_TALOSCTL"), Endpoint: os.Getenv("E4X_ENDPOINT"), Node: os.Getenv("E4X_NODE")},
		gates: Gates{dir: os.Getenv("E4X_GATES"), log: log},
		log:   log,
	}
	switch cmd {
	case "setup":
		digest, err := e.talos.ReadDigest(ctx)
		if err == nil {
			err = store.Setup(ctx, *machine, digest, *noScope)
		}
		return e.report(err, "event=setup machine=%s applied=%s scope_index=%t", *machine, short(digest), !*noScope)
	case "plan":
		path, err := filepath.Abs(*artifact)
		if err != nil {
			return e.report(err, "")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return e.report(err, "")
		}
		digest := digestOf(normalizeReadBack(body))
		err = store.Plan(ctx, PlanSpec{ID: *id, Machine: *machine, ArtifactPath: path, ArtifactDigest: digest,
			Expires: *expires, MaxAttempts: *maxAttempts, Verify: *verify, MaxAge: *maxAge, Transport: *transport})
		return e.report(err, "event=planned id=%s artifact=%s", *id, short(digest))
	case "approve":
		return e.report(store.Approve(ctx, *id), "event=approved id=%s", *id)
	case "revoke":
		return e.report(store.Revoke(ctx, *id, *actor), "event=revoked id=%s", *id)
	case "takeover":
		gen, err := store.Takeover(ctx, *id, *actor)
		return e.report(err, "event=takeover id=%s owner=%s gen=%d", *id, *actor, gen)
	case "account":
		return e.report(store.Account(ctx, *id, *n, *actor, *evidence), "event=accounted id=%s n=%d", *id, *n)
	case "state":
		return e.state(ctx, *id)
	case "run":
		return e.execute(ctx, *id, *actor, Mode(*mode))
	case "recover":
		return e.recover(ctx, *id, *actor, *retry)
	}
	return usage()
}

// report logs the outcome of a one-shot command and maps it to an exit code.
func (e env) report(err error, format string, a ...any) int {
	var r *Refusal
	switch {
	case errors.As(err, &r):
		e.log.Printf("event=refused comparison=%d reason=%q", r.Comparison, r.Reason)
		return exitRefused
	case err != nil:
		e.log.Printf("event=error err=%q", err)
		return exitError
	}
	if format != "" {
		e.log.Printf(format, a...)
	}
	return exitCompleted
}

func (e env) state(ctx context.Context, op string) int {
	state, owner, gen, err := e.store.Op(ctx, op)
	if err != nil {
		return e.report(err, "")
	}
	attempts, err := e.store.Attempts(ctx, op)
	if err != nil {
		return e.report(err, "")
	}
	e.log.Printf("event=state id=%s state=%s owner=%s gen=%d attempts=%d", op, state, owner, gen, len(attempts))
	for _, a := range attempts {
		e.log.Printf("event=attempt id=%s n=%d owner=%s gen=%d response=%s accounted=%t", op, a.N, a.Owner, a.Gen,
			nullOr(a.Response.String, a.Response.Valid), a.Account.Valid)
	}
	return exitForState(state)
}

func nullOr(s string, valid bool) string {
	if !valid {
		return "none"
	}
	return s
}

func exitForState(state string) int {
	switch state {
	case stateCompleted:
		return exitCompleted
	case stateRejected, stateFailed, stateCancelled:
		return exitTerminal
	}
	return exitUnresolved
}

// retryDB repeats a recording step while the database is unreachable. A refusal is an answer, not
// an outage, and is returned at once.
func (e env) retryDB(what string, fn func() error) error {
	limit := time.Now().Add(dbRetryLimit)
	for {
		err := fn()
		var r *Refusal
		if err == nil || errors.As(err, &r) || errors.Is(err, errResponded) || time.Now().After(limit) {
			return err
		}
		e.log.Printf("event=db-retry step=%s err=%q", what, err)
		time.Sleep(time.Second)
	}
}

// execute is one executor's run of an approved plan: the execution-time observation, the
// commitment transaction, the first attempt, the send and the verification.
func (e env) execute(ctx context.Context, op, owner string, mode Mode) int {
	p, err := e.store.LoadPlan(ctx, op)
	if err != nil {
		return e.report(err, "")
	}
	// Use-time check: the file on disk is still the bound artifact.
	if body, err := os.ReadFile(p.ArtifactPath); err != nil || digestOf(normalizeReadBack(body)) != p.ArtifactDigest {
		return e.report(refuse(3, "artifact at %s is not the bound artifact (%v)", p.ArtifactPath, err), "")
	}
	if mode == ModeNaive {
		// The naive control's only approval check, before the evidence and outside any write.
		if err := e.store.ApprovalHolds(ctx, op); err != nil {
			return e.report(err, "")
		}
		e.log.Printf("event=approval-checked id=%s", op)
	}
	obs, err := e.store.Observe(ctx, op, p.Machine, purposeEvidence, e.talos.ReadDigest)
	if err != nil {
		return e.report(err, "")
	}
	if obs.Err != nil {
		e.log.Printf("event=observation id=%d purpose=evidence err=%q", obs.ID, obs.Err)
		return exitError
	}
	e.log.Printf("event=observation id=%d purpose=evidence digest=%s", obs.ID, short(obs.Digest))
	if err := e.gates.Wait("evidence"); err != nil {
		return e.report(err, "")
	}
	gen, err := e.store.Commit(ctx, op, owner, mode, e.gates.Hook("commit"))
	var r *Refusal
	if errors.As(err, &r) && r.Comparison == 1 {
		if cerr := e.store.CancelUncommitted(ctx, op, owner, r.Reason); cerr != nil {
			e.log.Printf("event=error err=%q", cerr)
		}
	}
	if err != nil {
		return e.report(err, "")
	}
	e.log.Printf("event=committed id=%s owner=%s gen=%d mode=%s", op, owner, gen, mode)
	if err := e.gates.Wait("committed"); err != nil {
		return e.report(err, "")
	}
	return e.attemptAndVerify(ctx, p, owner, gen, mode, 0)
}

// attemptAndVerify records an attempt, sends it and drives the operation to a state. retryRev is
// the safe-to-retry classification a retry is bound to, or 0 for the first attempt.
func (e env) attemptAndVerify(ctx context.Context, p Plan, owner string, gen int, mode Mode, retryRev int64) int {
	n, err := e.store.Attempt(ctx, p.ID, owner, gen, mode, retryRev, e.gates.Hook("attempt"))
	if err != nil {
		code := e.report(err, "")
		if state, serr := e.store.State(ctx, p.ID); serr == nil {
			e.log.Printf("event=state id=%s state=%s", p.ID, state)
		}
		return code
	}
	attempts, err := e.store.Attempts(ctx, p.ID)
	if err != nil {
		return e.report(err, "")
	}
	deadline := attempts[n-1].Deadline
	e.log.Printf("event=attempt id=%s n=%d owner=%s gen=%d deadline=%s", p.ID, n, owner, gen,
		deadline.UTC().Format(time.RFC3339Nano))
	if err := e.gates.Wait("send"); err != nil {
		return e.report(err, "")
	}
	sendBy := time.Now().Add(p.Transport)
	if deadline.Before(sendBy) {
		sendBy = deadline
	}
	e.log.Printf("event=send id=%s n=%d transport_deadline=%s", p.ID, n, sendBy.UTC().Format(time.RFC3339Nano))
	resp := e.talos.Apply(ctx, p.ArtifactPath, sendBy)
	e.log.Printf("event=response id=%s n=%d class=%s elapsed_ms=%d detail=%q", p.ID, n, resp.Class,
		resp.Elapsed.Milliseconds(), resp.Detail)
	if err := e.gates.Wait("response"); err != nil {
		return e.report(err, "")
	}
	var state string
	err = e.retryDB("record-response", func() (err error) {
		state, err = e.store.RecordResponse(ctx, p.ID, n, owner, gen, resp)
		return err
	})
	if errors.Is(err, errResponded) {
		state, err = e.store.State(ctx, p.ID)
	}
	if err != nil {
		return e.report(err, "")
	}
	e.log.Printf("event=recorded id=%s n=%d state=%s", p.ID, n, state)
	if state != stateVerifying {
		return exitForState(state)
	}
	return e.verify(ctx, p, owner, gen, deadline)
}

// verify observes the machine until the attempt deadline. A matching observation completes the
// operation; at the deadline one last observation classifies it, completed or failed.
func (e env) verify(ctx context.Context, p Plan, owner string, gen int, deadline time.Time) int {
	if err := e.gates.Wait("verify"); err != nil {
		return e.report(err, "")
	}
	for {
		last := !time.Now().Before(deadline)
		var obs Observation
		err := e.retryDB("observe", func() (err error) {
			obs, err = e.store.Observe(ctx, p.ID, p.Machine, purposeCompletion, e.talos.ReadDigest)
			return err
		})
		if err != nil {
			return e.report(err, "")
		}
		switch {
		case obs.Err != nil:
			e.log.Printf("event=observation id=%d purpose=completion err=%q", obs.ID, obs.Err)
		case obs.Digest == p.ArtifactDigest || last:
			e.log.Printf("event=observation id=%d purpose=completion digest=%s", obs.ID, short(obs.Digest))
			if err := e.gates.Wait("complete"); err != nil {
				return e.report(err, "")
			}
			var state string
			err := e.retryDB("complete", func() (err error) {
				state, err = e.store.Complete(ctx, p.ID, owner, gen)
				return err
			})
			if err != nil {
				return e.report(err, "")
			}
			e.log.Printf("event=classified id=%s state=%s", p.ID, state)
			return exitForState(state)
		default:
			e.log.Printf("event=observation id=%d purpose=completion digest=%s", obs.ID, short(obs.Digest))
		}
		if last {
			// The final read failed: nothing classifies the operation.
			err := e.retryDB("unresolved", func() error {
				return e.store.MarkUnresolved(ctx, p.ID, owner, gen, "no successful observation by the attempt deadline")
			})
			if code := e.report(err, "event=unresolved id=%s", p.ID); code != exitCompleted {
				return code
			}
			return exitUnresolved
		}
		time.Sleep(time.Second)
	}
}

// recover is a new owner's resolution of an unresolved operation (§4). It never assumes an
// attempt has stopped: an attempt with neither a definitive response nor accounting keeps the
// operation unresolved.
func (e env) recover(ctx context.Context, op, owner string, retry bool) int {
	state, current, gen, err := e.store.Op(ctx, op)
	if err != nil {
		return e.report(err, "")
	}
	if current != owner {
		return e.report(refuse(comparisonOwner, "operation is owned by %s at %d", current, gen), "")
	}
	if state != stateUnresolved {
		e.log.Printf("event=state id=%s state=%s", op, state)
		return exitForState(state)
	}
	p, err := e.store.LoadPlan(ctx, op)
	if err != nil {
		return e.report(err, "")
	}
	attempts, err := e.store.Attempts(ctx, op)
	if err != nil {
		return e.report(err, "")
	}
	if len(attempts) == 0 {
		if e.store.ApprovalHolds(ctx, op) != nil {
			if code := e.report(e.store.CancelUnattempted(ctx, op, owner, gen), "event=cancelled id=%s", op); code != exitCompleted {
				return code
			}
			return exitTerminal
		}
	}
	accepted := false
	for _, a := range attempts {
		if !a.accounted() {
			e.log.Printf("event=unaccounted id=%s n=%d owner=%s gen=%d", op, a.N, a.Owner, a.Gen)
			return exitUnresolved
		}
		accepted = accepted || a.Response.String == respAccepted
	}
	obs, err := e.store.Observe(ctx, op, p.Machine, purposeCompletion, e.talos.ReadDigest)
	if err != nil {
		return e.report(err, "")
	}
	if obs.Err != nil {
		e.log.Printf("event=observation id=%d purpose=completion err=%q", obs.ID, obs.Err)
		return exitUnresolved
	}
	e.log.Printf("event=observation id=%d purpose=completion digest=%s", obs.ID, short(obs.Digest))
	if retry && !accepted && obs.Digest == p.PreDigest && len(attempts) < p.MaxAttempts {
		rev, err := e.store.ClassifySafeToRetry(ctx, op, owner, gen, "recovery: every attempt accounted for, none accepted")
		if err != nil {
			return e.report(err, "")
		}
		e.log.Printf("event=classified id=%s safe_to_retry_rev=%d", op, rev)
		return e.attemptAndVerify(ctx, p, owner, gen, ModeProtocol, rev)
	}
	if len(attempts) == 0 {
		// Nothing was sent and no retry was asked for: the operation stays unresolved.
		return exitUnresolved
	}
	state, err = e.store.Complete(ctx, op, owner, gen)
	if err != nil {
		return e.report(err, "")
	}
	e.log.Printf("event=classified id=%s state=%s", op, state)
	return exitForState(state)
}

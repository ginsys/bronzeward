// Command e1 is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It ingests a Talos machine configuration the way design §7.1 requires — extracting known or
// operator-marked secrets before any ordinary plaintext persistence — and records what it did in a
// journal that can be checked mechanically. It also implements the forbidden designs on purpose,
// behind explicit flags, so that the instrument measuring the honest path can be shown to detect
// the dishonest one.
//
// No secret is ever a command-line argument. Credentials are read from the environment, because a
// process's argv is world-readable through /proc and is captured by the evidence bundles this
// program's own runs produce.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/baseline"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/provider"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/staging"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/store"
)

// canaryEnv names the environment variable carrying the fixture canary. The reachability control
// is written from it; see plantReachabilityControl.
const canaryEnv = "BW_E1_CANARY"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "e1: %v\n", err)
		os.Exit(1)
	}
}

// options are the flags every subcommand shares.
type options struct {
	runRoot  string
	runID    string
	staging  string
	crashAt  checkpoint.Point
	holdAt   checkpoint.Point
	holdFor  time.Duration
	journalP string
}

func run(args []string, stdout, stderr io.Writer) error {
	// Core dumps would put the whole address space, secrets included, on disk outside every
	// surface this experiment measures. Disabling them does not make the process leak-proof — swap
	// and /proc/<pid>/mem remain unmeasured, and the report says so — but a core file would be a
	// leak caused by the instrument rather than by the design under test.
	if err := disableCoreDumps(); err != nil {
		return err
	}

	fs := flag.NewFlagSet("e1", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		runRoot = fs.String("run-root", "", "the only directory this run may write to (required)")
		runID   = fs.String("run-id", "", "identifier for this run; defaults to the run root's base name")
		staging = fs.String("staging", "transient", "staging mode: transient or encrypted")
		crashAt = fs.String("crash-at", "none", "boundary at which to SIGKILL this process: "+strings.Join(checkpoint.Names(), ", "))
		holdAt  = fs.String("hold-at", "none", "boundary at which to pause so the disk can be captured mid-flight")
		holdFor = fs.Duration("hold-for", 0, "how long --hold-at pauses; zero means the default")
	)
	fs.Usage = func() { usage(stderr, fs) }

	if err := fs.Parse(args); err != nil {
		return errors.New("see -h for the accepted flags")
	}
	if fs.NArg() == 0 {
		usage(stderr, fs)
		return errors.New("a subcommand is required")
	}

	opts := options{runRoot: *runRoot, runID: *runID, staging: *staging, holdFor: *holdFor}

	var err error
	if opts.crashAt, err = checkpoint.Parse(*crashAt); err != nil {
		return err
	}
	if opts.holdAt, err = checkpoint.Parse(*holdAt); err != nil {
		return err
	}
	if opts.crashAt != checkpoint.None && opts.holdAt != checkpoint.None {
		// Both at once would produce a bundle that is neither a crash capture nor a hold capture,
		// and the run would be captioned as whichever the operator remembered.
		return errors.New("--crash-at and --hold-at cannot both be set; a bundle must be one kind of capture or the other")
	}
	if opts.staging != "transient" && opts.staging != "encrypted" {
		return fmt.Errorf("unknown --staging %q; want transient or encrypted", opts.staging)
	}

	switch cmd := fs.Arg(0); cmd {
	case "verify-order":
		return verifyOrder(fs.Args()[1:], stdout)
	case "baseline-verify":
		return baselineVerify(fs.Args()[1:], stdout)
	case "recover":
		return recoverRun(context.Background(), opts, fs.Args()[1:], stdout)
	case "import", "adopt", "schema-report":
		return fmt.Errorf("subcommand %q is not built yet", cmd)
	default:
		usage(stderr, fs)
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, `e1 - Phase-0 evidence for the secret-ingress experiment (ginsys/bronzeward issue 2)

Usage: e1 [flags] <subcommand> [arguments]

Subcommands:
  import           ingest a Talos machine configuration and secrets bundle
  adopt            ingest an observed effective configuration read back off a node
  recover          resume an interrupted run from encrypted staging, as another principal
  verify-order     check a journal against the extraction-before-persistence requirement
  baseline-verify  check the encrypted baseline decrypts to the input it was made from
  schema-report    report which secret-bearing fields schema detection finds, and which it misses

Credentials come from the environment, never from a flag: argv is world-readable and is captured
by the evidence bundles this program's runs produce.

Flags:
`)
	fs.PrintDefaults()
}

// verifyOrder checks a journal file. It is a separate entry point from the ingestion flow on
// purpose: a crashed run's journal is verified by a process that is not the one that wrote it.
func verifyOrder(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("verify-order takes one argument: the path to a journal.jsonl")
	}
	path := args[0]

	records, err := journal.Load(path)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		// An empty journal passing every check would be the worst possible outcome: a run that
		// recorded nothing would look identical to a run that did everything right.
		return fmt.Errorf("%s holds no records; there is nothing to verify and this is not a pass", path)
	}

	violations := journal.Verify(records)
	for _, v := range violations {
		fmt.Fprintln(stdout, v)
	}
	if len(violations) > 0 {
		return fmt.Errorf("%d ordering violation(s) in %s across %d records", len(violations), path, len(records))
	}

	fmt.Fprintf(stdout, "ok: %d records in %s, every write preceded by the extraction of every secret it lists\n",
		len(records), path)
	return nil
}

// baselineVerify checks a saved encrypted baseline against the provider.
//
// It is a separate entry point from the run that produced the baseline, on purpose: a round trip
// checked only by the process that did the encryption proves that the process is self-consistent,
// not that the configuration can be recovered later by anyone else.
func baselineVerify(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("baseline-verify takes one argument: the path to a baseline.json")
	}

	b, err := baseline.Load(args[0])
	if err != nil {
		return err
	}
	client, err := provider.FromEnv(provider.TokenEnv)
	if err != nil {
		return err
	}
	if err := b.Verify(context.Background(), client); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "ok: run %s, %d bytes decrypt to the recorded digest %s under transit key %s\n",
		b.RunID, b.InputBytes, b.InputSHA256[:12], b.KeyName)
	fmt.Fprintf(stdout, "note: this baseline's ciphertext is not comparable to another run's. "+
		"Transit uses a fresh nonce per call, so identical input encrypts differently every time; "+
		"two runs are compared by their recorded input digests, not by their ciphertext.\n")
	return nil
}

// recoverRun resumes an interrupted run's pending change from staging, as a principal that is
// deliberately not the one that held it, and persists the sanitized draft.
//
// It is run for both staging modes, and the transient mode's refusal is the point: the two
// alternatives differ in exactly what this subcommand can do, so running it against each is how
// that difference becomes a measurement instead of a description. Against transient staging it
// exits non-zero carrying staging.ErrNotRecoverable; against encrypted staging it succeeds, and
// under `inject netsplit openbao` it fails at the decryption, which is that mode's own cost.
//
// The recovery gets its own run root and its own journal. Nothing of the original extraction is
// re-journalled here — this process did not extract anything — so the write it records lists no
// digests, and the note it appends points at the journal that does hold them.
func recoverRun(ctx context.Context, opts options, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("recover takes one argument: the run id whose pending change is staged")
	}
	claimed := args[0]

	opts, err := prepareRunRoot(opts)
	if err != nil {
		return err
	}
	j, err := journal.Open(opts.journalP, opts.runID)
	if err != nil {
		return err
	}
	defer j.Close()

	db, err := store.OpenFromEnv(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.EnsureSchema(ctx); err != nil {
		return err
	}

	place, err := openStaging(ctx, opts, db)
	if err != nil {
		return err
	}
	principal, err := staging.NewPrincipal("recovery")
	if err != nil {
		return err
	}

	resumed, claim, err := place.Resume(ctx, claimed, principal)
	if err != nil {
		// The refusal is a result, so it is recorded rather than only returned. A run that failed
		// and journalled nothing would be indistinguishable from a run that was never started.
		if _, appendErr := j.Append(journal.Record{
			Event:  journal.EventNote,
			Detail: fmt.Sprintf("recover %s as %s under %s staging refused: %v", claimed, principal, place.Mode(), err),
		}); appendErr != nil {
			return fmt.Errorf("%w (and the journal could not record it: %v)", err, appendErr)
		}
		return err
	}

	if _, err := j.Append(journal.Record{
		Event: journal.EventNote,
		Detail: fmt.Sprintf("resumed run %s from %s staging as %s, at checkpoint %s; "+
			"the extraction of its secrets is recorded in that run's journal, not this one",
			claimed, place.Mode(), principal, claim.ResumeCheckpoint),
	}); err != nil {
		return err
	}

	ctrl := newControl(opts, j)
	if err := db.PersistDraft(ctx, store.Draft{
		RunID:     opts.runID,
		Source:    "recover:" + claimed,
		Sanitized: resumed,
	}, j, ctrl); err != nil {
		return err
	}
	if err := place.Release(ctx, claimed); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "ok: run %s resumed from %s staging as %s and persisted as run %s\n",
		claimed, place.Mode(), principal, opts.runID)
	return nil
}

// openStaging builds the staging alternative named by --staging.
func openStaging(ctx context.Context, opts options, db *store.DB) (staging.Staging, error) {
	if opts.staging == "transient" {
		return staging.NewTransient(db.SQL(), 0), nil
	}
	client, err := provider.FromEnv(provider.TokenEnv)
	if err != nil {
		return nil, err
	}
	return staging.NewEncrypted(db.SQL(), client, provider.TransitKey, 0)
}

// newControl wires the crash and hold flags to the journal, so that a capture taken mid-flight
// carries a record of which boundary it was taken at rather than relying on the harness's caption.
func newControl(opts options, j *journal.Journal) *checkpoint.Control {
	return &checkpoint.Control{
		CrashAt: opts.crashAt,
		HoldAt:  opts.holdAt,
		HoldFor: opts.holdFor,
		Observe: func(p checkpoint.Point) {
			// A failure to record a checkpoint is not worth aborting a run over, but it must not be
			// silent either: verify-order would read the gap as a boundary that was never crossed.
			if _, err := j.Append(journal.Record{Event: journal.EventCheckpoint, Checkpoint: p.String()}); err != nil {
				fmt.Fprintf(os.Stderr, "e1: could not journal checkpoint %s: %v\n", p, err)
			}
		},
		Announce: func(msg string) { fmt.Fprintf(os.Stderr, "e1: %s\n", msg) },
	}
}

// prepareRunRoot creates the run root and plants the reachability control in it. It is called by
// every ingesting subcommand.
//
// It is here, and not in the harness, because the control must exist for the whole life of the
// run: a crash at the first boundary must still leave it behind, or that bundle's leak scan cannot
// be distinguished from one where the run root was never walked.
func prepareRunRoot(opts options) (options, error) {
	if opts.runRoot == "" {
		return opts, errors.New("--run-root is required; this program writes nowhere else")
	}
	abs, err := filepath.Abs(opts.runRoot)
	if err != nil {
		return opts, fmt.Errorf("resolving --run-root: %w", err)
	}
	opts.runRoot = abs
	if opts.runID == "" {
		opts.runID = filepath.Base(abs)
	}
	if err := os.MkdirAll(filepath.Join(abs, "meta"), 0o700); err != nil {
		return opts, fmt.Errorf("creating the run root: %w", err)
	}
	if err := plantReachabilityControl(abs); err != nil {
		return opts, err
	}
	opts.journalP = filepath.Join(abs, "journal.jsonl")
	return opts, nil
}

// plantReachabilityControl writes the canary into the run root on purpose.
//
// The fixtures' leak scan records hits only, so a path with no hits produces no line at all, and
// a clean scan of a directory is byte-for-byte identical to a scan that never walked it. The
// fixtures' own positive control lives elsewhere and proves only that its own directory was read.
//
// This file is the control for the run root specifically. Every honest bundle's leak-scan.txt must
// therefore hold exactly two lines: the fixtures' control and this one. Fewer means the run root
// went unscanned and the bundle proves nothing about the surfaces that matter most; more means
// something leaked.
func plantReachabilityControl(runRoot string) error {
	canary := os.Getenv(canaryEnv)
	if canary == "" {
		return fmt.Errorf("%s is not set; without the canary the run root's leak scan cannot be "+
			"told apart from one that never walked it, so the run would produce no usable evidence", canaryEnv)
	}
	path := filepath.Join(runRoot, "meta", "reach-control.txt")
	body := "This file is a deliberate control, not a leak.\n" +
		"It proves the leak scan reached this run root; see plantReachabilityControl in main.go.\n" +
		canary + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("planting the reachability control: %w", err)
	}
	return nil
}

// disableCoreDumps sets RLIMIT_CORE to zero.
func disableCoreDumps() error {
	zero := syscall.Rlimit{Cur: 0, Max: 0}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &zero); err != nil {
		return fmt.Errorf("disabling core dumps: %w", err)
	}
	return nil
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/baseline"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/control"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/detect"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/extract"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/mark"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/provider"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/staging"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/store"
)

// ingest is the flow under test, and the same function serves both import (§9.1) and drift
// adoption (§12.4). They differ in which document the operator points at, not in what happens to
// it, and giving them separate implementations would make their results incomparable.
//
// The honest order is read, parse, detect, extract, then persist. The control flags reorder it on
// purpose and are threaded through the same function rather than through a copy of it, so that the
// forbidden design is measured against the identical code path and not against a second one that
// might differ somewhere the report never looked.
func ingest(ctx context.Context, opts options, source string, stdout io.Writer) error {
	if opts.config == "" {
		return errors.New("--config is required: the path to the machine configuration to ingest")
	}

	opts, err := prepareRunRoot(opts)
	if err != nil {
		return err
	}
	j, err := journal.Open(opts.journalP, opts.runID)
	if err != nil {
		return err
	}
	defer j.Close()
	ctrl := newControl(opts, j)

	if _, err := j.Append(journal.Record{
		Event:  journal.EventNote,
		Detail: fmt.Sprintf("%s of %s, staging=%s, %s", source, opts.config, opts.staging, opts.control.Describe()),
	}); err != nil {
		return err
	}

	raw, err := os.ReadFile(opts.config)
	if err != nil {
		return fmt.Errorf("reading the configuration: %w", err)
	}
	ctrl.Reach(checkpoint.AfterRead)

	doc, err := document.Load(raw)
	if err != nil {
		return err
	}
	// The coverage gap travels with the run. A secret under a key whose name would make its path
	// ambiguous cannot be marked, detected or extracted by this prototype, and a clean leak scan
	// that did not say so would overstate what was covered.
	if gaps := doc.Unaddressable(); len(gaps) > 0 {
		if _, err := j.Append(journal.Record{
			Event: journal.EventNote,
			Detail: fmt.Sprintf("%d key(s) cannot be addressed and nothing below them was considered: %s",
				len(gaps), strings.Join(gaps, "; ")),
		}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "coverage: %d unaddressable key(s); nothing below them was considered: %s\n",
			len(gaps), strings.Join(gaps, "; "))
	}
	ctrl.Reach(checkpoint.AfterParse)

	paths, err := targetPaths(opts, doc, j)
	if err != nil {
		return err
	}
	ctrl.Reach(checkpoint.AfterDetect)

	// The deliberate leak's value is taken from the document before anything is extracted, because
	// after extraction the plaintext is gone from it — which is the point. It is the value at the
	// first target path rather than the canary: a control that leaked the canary would be testing
	// whether the scan finds the canary, and a control that leaks a configuration secret tests
	// whether it finds the thing the design is about. Attribution is by the file it lands in.
	var leakValue secret.Unresolved
	if opts.control.LeakAt != control.SurfaceNone {
		value, ok := doc.Get(paths[0])
		if !ok {
			return fmt.Errorf("the leak control has no value to write: %q holds no scalar", paths[0])
		}
		leakValue = secret.NewUnresolved([]byte(value))
	}

	db, err := store.OpenFromEnv(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.EnsureSchema(ctx); err != nil {
		return err
	}
	client, err := provider.FromEnv(provider.TokenEnv)
	if err != nil {
		return err
	}

	// The forbidden order, run before extraction on purpose. store.PersistDraft cannot express this
	// — it takes a secret.Sanitized, which only extract.Run constructs — so the control had to be
	// written in its own package against the database handle directly. That compile-time refusal is
	// the structural half of the claim; what follows measures it.
	if opts.control.PersistFirst {
		if err := control.PersistFirst(ctx, db.SQL(), opts.runID, source, raw,
			plainIndex(doc, paths), j, opts.control, ctrl); err != nil {
			return err
		}
	}

	result, err := extract.Run(ctx, extract.Request{
		Document: doc,
		Paths:    paths,
		Store:    client,
		Journal:  j,
		Control:  ctrl,
		RunID:    opts.runID,
	})
	if err != nil {
		return err
	}

	if opts.control.LeakAt != control.SurfaceNone {
		where, err := control.Leak(ctx, opts.control.LeakAt, opts.runRoot, db.SQL(), leakValue, j)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "control: leaked to %s\n", where)
	}
	ctrl.Reach(checkpoint.AfterFirstLog)

	// InReview is reached inside stageAndResume, while the claim is still outstanding. Reaching it
	// here instead would stop the run after the claim had already been resumed and released, so a
	// crash would leave nothing held and the recovery comparison the two staging alternatives exist
	// for would have nothing to take over.
	sanitized, err := stageAndResume(ctx, opts, db, client, result.Sanitized, j, ctrl)
	if err != nil {
		return err
	}

	switch {
	case opts.control.RedactAfter:
		// §7.1's "redacting it later is forbidden", measured: the live rows end up clean and the
		// plaintext committed a moment ago is still in the write-ahead log and in any snapshot.
		if err := control.RedactAfter(ctx, db.SQL(), opts.runID, sanitized, j); err != nil {
			return err
		}
	case opts.control.PersistFirst:
		// The plaintext draft is already committed, or was rolled back. Persisting the sanitized
		// form on top would overwrite the evidence this control exists to produce.
	default:
		if err := db.PersistDraft(ctx, store.Draft{
			RunID:     opts.runID,
			Source:    source,
			Sanitized: sanitized,
			Digests:   result.Digests,
		}, j, ctrl); err != nil {
			return err
		}
	}

	if opts.baseline {
		if err := retainBaseline(ctx, opts, db, client, raw, j); err != nil {
			return err
		}
	}
	ctrl.Reach(checkpoint.AfterBaseline)

	fmt.Fprintf(stdout, "ok: %s run %s, %d secret(s) extracted to %s before any persistence, %s\n",
		source, opts.runID, len(result.Digests), client.Name(), opts.control.Describe())
	return nil
}

// schemaReport measures schema detection against a ground truth the detector had no part in
// producing, which is the only way the numbers mean anything.
//
// The ground truth comes from the secrets bundle the configuration was generated from, intersected
// with what is actually present in the configuration: the generator is the authority and the
// detector is under test. The harness builds that file; this subcommand only reads it, so that the
// denominator cannot quietly become "whatever the detector found".
//
// Recall and precision are printed as integers with both denominators, never as an F-score: a
// single number would hide which of the two moved, and §6.9 forbids the completeness claim that a
// single number invites.
func schemaReport(opts options, args []string, stdout io.Writer) error {
	if opts.config == "" {
		return errors.New("--config is required: the path to the configuration to assess")
	}
	if len(args) != 1 {
		return errors.New("schema-report takes one argument: a file of ground-truth secret paths, " +
			"one per line, derived from the secrets bundle rather than from this detector")
	}

	raw, err := os.ReadFile(opts.config)
	if err != nil {
		return fmt.Errorf("reading the configuration: %w", err)
	}
	doc, err := document.Load(raw)
	if err != nil {
		return err
	}

	truthList, err := mark.LoadPathList(args[0])
	if err != nil {
		return err
	}
	truth, err := truthList.Marks(doc)
	if err != nil {
		return fmt.Errorf("the ground truth does not match the configuration: %w", err)
	}

	rules := detect.TalosRules()
	found := detect.Paths(detect.Schema(doc, rules))

	fmt.Fprintf(stdout, "configuration: %s (%d scalar paths)\n", opts.config, len(doc.Paths()))
	fmt.Fprintf(stdout, "ground truth:  %s (%d paths)\n", args[0], len(truth))
	fmt.Fprintf(stdout, "detector:      %d schema rules\n\n", len(rules))
	fmt.Fprintln(stdout, detect.Assess(found, truth))
	fmt.Fprintf(stdout, "\nThis recall is over the fields this environment produces. A Docker-provisioned\n"+
		"Talos node emits no systemDiskEncryption, installer or disk configuration — precisely the\n"+
		"secret-bearing areas that are absent here — so the denominator understates what a real node\n"+
		"would present. Design §6.9 forbids a completeness claim for unmarked values outright: what\n"+
		"is recorded is reliability on identified fields, with the rest explicitly the operator's\n"+
		"responsibility through marking.\n")
	return nil
}

// pathsHolding prints every configuration path whose scalar is one of the values in a file. It is
// how the harness builds schema-report's ground truth, and the split of work is the point.
//
// The authority on what counts as a secret is the fixture's own rule over the secrets bundle Talos
// generated, applied by the harness with the fixture's own expression. This subcommand only does
// the mechanical half: it says where in the configuration each of those values ended up. A ground
// truth this program derived on its own would be the detector grading its own work.
//
// The values file holds real synthetic secrets and belongs in the run output directory, never in
// the repository. Nothing here prints a value: the output is paths, and a value that appears at
// several paths is reported at each of them.
func pathsHolding(opts options, args []string, stdout io.Writer) error {
	if opts.config == "" {
		return errors.New("--config is required: the path to the configuration to look in")
	}
	if len(args) != 1 {
		return errors.New("paths-holding takes one argument: a file of known secret values, one per line")
	}

	raw, err := os.ReadFile(opts.config)
	if err != nil {
		return fmt.Errorf("reading the configuration: %w", err)
	}
	doc, err := document.Load(raw)
	if err != nil {
		return err
	}

	body, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading the known secret values: %w", err)
	}
	wanted := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		v := strings.TrimSpace(line)
		// A short value would match a version string or a boolean and inflate the denominator with
		// fields that hold no secret. The fixture's own pattern list drops the same length.
		if len(v) >= 8 {
			wanted[v] = true
		}
	}
	if len(wanted) == 0 {
		return fmt.Errorf("%s holds no value of at least 8 characters; an empty ground truth would "+
			"make every recall figure meaningless rather than zero", args[0])
	}

	found := 0
	for _, path := range doc.Paths() {
		value, ok := doc.Get(path)
		if !ok || !wanted[value] {
			continue
		}
		fmt.Fprintln(stdout, path)
		found++
	}
	if found == 0 {
		return fmt.Errorf("none of the %d known value(s) appears in %s; the ground truth would be "+
			"empty and every later number would be reporting that, not the detector", len(wanted), opts.config)
	}
	return nil
}

// targetPaths is the union of schema detection and the operator's marks.
//
// Both sources are recorded in the journal separately, because the report's schema-detection
// numbers depend on knowing which paths each one contributed. A union alone would make a path found
// by both indistinguishable from one found by either.
func targetPaths(opts options, doc *document.Document, j *journal.Journal) ([]string, error) {
	found := detect.Schema(doc, detect.TalosRules())
	schemaPaths := detect.Paths(found)

	source, err := markSource(opts)
	if err != nil {
		return nil, err
	}
	var markPaths []string
	if source != nil {
		if markPaths, err = source.Marks(doc); err != nil {
			return nil, err
		}
		if _, err := j.Append(journal.Record{
			Event:  journal.EventNote,
			Detail: fmt.Sprintf("mark source %s contributed %d path(s)", source.Name(), len(markPaths)),
		}); err != nil {
			return nil, err
		}
	}
	if _, err := j.Append(journal.Record{
		Event:  journal.EventNote,
		Detail: fmt.Sprintf("schema detection contributed %d path(s) from %d rule(s)", len(schemaPaths), len(detect.TalosRules())),
	}); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var union []string
	for _, p := range append(schemaPaths, markPaths...) {
		if !seen[p] {
			seen[p] = true
			union = append(union, p)
		}
	}
	sort.Strings(union)

	if len(union) == 0 {
		// A run with nothing to extract produces a document identical to its input and a clean leak
		// scan, which looks exactly like a successful extraction.
		return nil, fmt.Errorf("neither schema detection nor the marks found anything in %s to extract; "+
			"this run would be clean for the wrong reason", opts.config)
	}
	return union, nil
}

// markSource resolves --marks or --mark-suffix.
//
// Two implementations exist so the matrix can be re-run with a different one and show that the
// ordering results do not depend on which marking syntax is used. E1 selects neither for v1; §6.9
// stays open and issue 3 owns that decision.
func markSource(opts options) (mark.Source, error) {
	if opts.marks != "" && opts.markSuffix != "" {
		return nil, errors.New("--marks and --mark-suffix cannot both be set; the run is supposed to " +
			"show the results are the same under either, which needs one per run")
	}
	switch {
	case opts.marks != "":
		return mark.LoadPathList(opts.marks)
	case opts.markSuffix != "":
		return mark.NewKeySuffix(strings.Split(opts.markSuffix, ",")...)
	default:
		return nil, nil
	}
}

// plainIndex is the parsed index the persist-first control writes: the values as they were read,
// before anything was extracted. It exists only for that control.
func plainIndex(doc *document.Document, paths []string) map[string]string {
	index := map[string]string{}
	for _, p := range paths {
		if value, ok := doc.Get(p); ok {
			index[p] = value
		}
	}
	return index
}

// stageAndResume puts the sanitized change through staging and takes it back out, which is where
// the two alternatives differ and where a crash or a hold at in-review lands.
//
// The same principal resumes here. A resume by a different one is what the recover subcommand does,
// and keeping it a separate process is the point: an in-process resume would prove the program is
// self-consistent, not that a second party can take over.
func stageAndResume(ctx context.Context, opts options, db *store.DB, client *provider.Client, s secret.Sanitized, j *journal.Journal, ctrl *checkpoint.Control) (secret.Sanitized, error) {
	place, err := openStagingWith(opts, db, client)
	if err != nil {
		return secret.Sanitized{}, err
	}
	principal, err := staging.NewPrincipal("operator")
	if err != nil {
		return secret.Sanitized{}, err
	}

	claim, err := place.Hold(ctx, opts.runID, principal, s, checkpoint.InReview)
	if err != nil {
		return secret.Sanitized{}, err
	}
	if _, err := j.Append(journal.Record{
		Event: journal.EventNote,
		Detail: fmt.Sprintf("staged in %s staging as %s until %s",
			place.Mode(), principal, claim.ExpiresAt.UTC().Format("15:04:05Z")),
	}); err != nil {
		return secret.Sanitized{}, err
	}

	// The change is staged and the claim is outstanding: this is the review window the two staging
	// alternatives are compared over, and the only moment at which a crash leaves something for a
	// second principal to recover.
	ctrl.Reach(checkpoint.InReview)

	resumed, _, err := place.Resume(ctx, opts.runID, principal)
	if err != nil {
		return secret.Sanitized{}, err
	}
	if err := place.Release(ctx, opts.runID); err != nil {
		return secret.Sanitized{}, err
	}
	return resumed, nil
}

// retainBaseline keeps the observed configuration as ciphertext, which §7.1 permits, rather than as
// the plaintext draft it forbids.
func retainBaseline(ctx context.Context, opts options, db *store.DB, client *provider.Client, raw []byte, j *journal.Journal) error {
	b, err := baseline.Make(ctx, client, provider.TransitKey, opts.runID, raw)
	if err != nil {
		return err
	}
	if err := db.SaveBaseline(ctx, b); err != nil {
		return err
	}
	path := filepath.Join(opts.runRoot, "baseline.json")
	if err := baseline.Save(path, b); err != nil {
		return err
	}
	if _, err := j.Append(journal.Record{
		Event:         journal.EventWrite,
		Surface:       "encrypted_baseline",
		PayloadSHA256: b.InputSHA256,
		Detail: "the observed configuration retained as ciphertext under transit key " + b.KeyName +
			"; the digest here is of the input, not of the ciphertext, which differs on every call",
	}); err != nil {
		return err
	}
	return nil
}

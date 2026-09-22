package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI drives run() with the flag set a real invocation would get.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err = run(args, &out, &errBuf)
	return out.String(), errBuf.String(), err
}

// TestFlagValidation covers the invocations that must fail before anything is written. Each of
// these would otherwise produce a bundle captioned as something it is not, which is worse than no
// bundle: a wrongly labelled control is read as a result.
func TestFlagValidation(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		wants string
	}{
		{"no subcommand", nil, "subcommand is required"},
		{"unknown subcommand", []string{"ingest"}, `unknown subcommand "ingest"`},
		{"misspelled crash point", []string{"--crash-at=after_read", "import"}, "unknown checkpoint"},
		{"misspelled hold point", []string{"--hold-at=in-db-tx", "import"}, "unknown checkpoint"},
		{"crash and hold together", []string{"--crash-at=after-read", "--hold-at=in-review", "import"}, "cannot both be set"},
		{"unknown staging mode", []string{"--staging=encryptd", "import"}, "unknown --staging"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := runCLI(t, c.args...)
			if err == nil {
				t.Fatalf("run(%v) returned no error", c.args)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("run(%v) said %q, want it to mention %q", c.args, err, c.wants)
			}
		})
	}
}

// TestUnbuiltSubcommandsSaySo checks the skeleton is honest about what it does not do yet, rather
// than exiting zero and leaving a caller to assume a run happened.
func TestUnbuiltSubcommandsSaySo(t *testing.T) {
	for _, cmd := range []string{"import", "adopt", "schema-report"} {
		_, _, err := runCLI(t, cmd)
		if err == nil {
			t.Errorf("%q returned no error despite not being built", cmd)
			continue
		}
		if !strings.Contains(err.Error(), "not built yet") {
			t.Errorf("%q failed with %q, which does not say it is unbuilt", cmd, err)
		}
	}
}

// TestRecoverRefusesBeforeTouchingAnything covers the guards that run before recover opens a
// database or a provider. Both failures below would otherwise surface as a connection error, which
// reads as an environment problem rather than as a wrong invocation.
func TestRecoverRefusesBeforeTouchingAnything(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run")

	if _, _, err := runCLI(t, "--run-root="+root, "recover"); err == nil {
		t.Error("recover accepted no run id")
	} else if !strings.Contains(err.Error(), "one argument") {
		t.Errorf("the error does not say what recover wants: %v", err)
	}

	// The run root is prepared before the database is opened, so a missing canary stops the run
	// here. Without it the bundle's leak scan of the run root would be unreadable, and a recovery
	// that produced no usable evidence is not worth running at all.
	t.Setenv(canaryEnv, "")
	if _, _, err := runCLI(t, "--run-root="+root, "recover", "run-earlier"); err == nil {
		t.Error("recover ran with no canary set")
	} else if !strings.Contains(err.Error(), canaryEnv) {
		t.Errorf("the error does not name the missing variable: %v", err)
	}
}

// writeJournal puts lines in a temporary journal file and returns its path.
func writeJournal(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the journal fixture: %v", err)
	}
	return path
}

// TestVerifyOrderAcceptsAnHonestJournal is the honest-path case: extraction, then the write.
func TestVerifyOrderAcceptsAnHonestJournal(t *testing.T) {
	path := writeJournal(t,
		`{"seq":1,"run_id":"r","event":"secret.extracted","secret_digests":["aaaa"],"mono_ns":10}`,
		`{"seq":2,"run_id":"r","event":"payload.write","surface":"machine_draft","secret_digests":["aaaa"],"mono_ns":20}`,
	)

	stdout, _, err := runCLI(t, "verify-order", path)
	if err != nil {
		t.Fatalf("verify-order on an honest journal: %v", err)
	}
	if !strings.HasPrefix(stdout, "ok: 2 records") {
		t.Errorf("stdout = %q, want it to report 2 records", stdout)
	}
}

// TestVerifyOrderRejectsPersistFirst is the positive control for the command. A verify-order that
// cannot fail is not a check, and every honest run's pass would be worthless.
func TestVerifyOrderRejectsPersistFirst(t *testing.T) {
	path := writeJournal(t,
		`{"seq":1,"run_id":"r","event":"payload.write","surface":"machine_draft","secret_digests":["aaaa"],"mono_ns":10}`,
		`{"seq":2,"run_id":"r","event":"secret.extracted","secret_digests":["aaaa"],"mono_ns":20}`,
	)

	stdout, _, err := runCLI(t, "verify-order", path)
	if err == nil {
		t.Fatal("verify-order accepted a journal that persisted before extracting")
	}
	if !strings.Contains(err.Error(), "1 ordering violation") {
		t.Errorf("the error does not count the violations: %v", err)
	}
	// The violations themselves go to stdout so the harness can file them with the bundle.
	if !strings.Contains(stdout, "machine_draft") {
		t.Errorf("stdout does not name the surface: %q", stdout)
	}
}

// TestVerifyOrderRefusesAnEmptyJournal pins the failure mode that would quietly turn a run which
// recorded nothing into a pass.
func TestVerifyOrderRefusesAnEmptyJournal(t *testing.T) {
	_, _, err := runCLI(t, "verify-order", writeJournal(t))
	if err == nil {
		t.Fatal("verify-order accepted an empty journal")
	}
	if !strings.Contains(err.Error(), "not a pass") {
		t.Errorf("the error does not say an empty journal is not a pass: %v", err)
	}
}

// TestVerifyOrderArgumentCount checks the command refuses a missing or extra path rather than
// verifying whichever one it happened to pick.
func TestVerifyOrderArgumentCount(t *testing.T) {
	for _, args := range [][]string{{"verify-order"}, {"verify-order", "a", "b"}} {
		if _, _, err := runCLI(t, args...); err == nil {
			t.Errorf("run(%v) returned no error", args)
		}
	}
}

// TestBaselineVerifyArgumentsAndLoading covers what can be checked without a live provider: the
// argument count, and that a missing or malformed file is reported before anything contacts
// OpenBao. The decryption itself is exercised by the matrix run against the fixtures.
func TestBaselineVerifyArgumentsAndLoading(t *testing.T) {
	for _, args := range [][]string{{"baseline-verify"}, {"baseline-verify", "a", "b"}} {
		if _, _, err := runCLI(t, args...); err == nil {
			t.Errorf("run(%v) returned no error", args)
		}
	}

	missing := filepath.Join(t.TempDir(), "absent.json")
	_, _, err := runCLI(t, "baseline-verify", missing)
	if err == nil {
		t.Fatal("baseline-verify accepted a path that does not exist")
	}
	// It must fail on the file, not on an unset provider token: reporting the wrong one first
	// sends a reader to fix the wrong thing.
	if !strings.Contains(err.Error(), "absent.json") {
		t.Errorf("the error does not name the missing file: %v", err)
	}
}

// TestPrepareRunRootPlantsTheReachabilityControl covers the invariant every honest bundle is
// checked against: the run root holds exactly one deliberate canary, so a leak scan of it produces
// exactly one line and "clean" can be told apart from "never walked".
func TestPrepareRunRootPlantsTheReachabilityControl(t *testing.T) {
	const canary = "BWSYNTH-TESTCANARY-0123456789"
	t.Setenv(canaryEnv, canary)

	root := filepath.Join(t.TempDir(), "run-0001")
	opts, err := prepareRunRoot(options{runRoot: root})
	if err != nil {
		t.Fatalf("prepareRunRoot: %v", err)
	}

	if opts.runID != "run-0001" {
		t.Errorf("run id = %q, want it derived from the run root's base name", opts.runID)
	}
	if !filepath.IsAbs(opts.runRoot) {
		t.Errorf("run root %q was not made absolute", opts.runRoot)
	}
	if want := filepath.Join(opts.runRoot, "journal.jsonl"); opts.journalP != want {
		t.Errorf("journal path = %q, want %q", opts.journalP, want)
	}

	body, err := os.ReadFile(filepath.Join(opts.runRoot, "meta", "reach-control.txt"))
	if err != nil {
		t.Fatalf("reading the reachability control: %v", err)
	}
	if !strings.Contains(string(body), canary) {
		t.Errorf("the control does not hold the canary:\n%s", body)
	}
	// It must also say what it is. A bundle is read by someone who did not run it, and an
	// unexplained canary in the evidence looks exactly like the leak it is there to rule out.
	if !strings.Contains(string(body), "deliberate control") {
		t.Errorf("the control does not explain itself:\n%s", body)
	}
	// Exactly one occurrence, or the two-line invariant becomes a three-line one.
	if n := strings.Count(string(body), canary); n != 1 {
		t.Errorf("the control holds the canary %d times, want 1", n)
	}
}

// TestPrepareRunRootRefusesWithoutACanary checks the run fails before writing anything when the
// control cannot be planted. Proceeding would produce a bundle whose run-root scan is
// uninterpretable, which is the failure this whole mechanism exists to prevent.
func TestPrepareRunRootRefusesWithoutACanary(t *testing.T) {
	t.Setenv(canaryEnv, "")

	root := filepath.Join(t.TempDir(), "run-0002")
	if _, err := prepareRunRoot(options{runRoot: root}); err == nil {
		t.Fatal("prepareRunRoot proceeded without a canary")
	} else if !strings.Contains(err.Error(), canaryEnv) {
		t.Errorf("the error does not name the variable to set: %v", err)
	}
}

// TestPrepareRunRootRequiresARunRoot checks the program refuses to pick a directory for itself.
func TestPrepareRunRootRequiresARunRoot(t *testing.T) {
	t.Setenv(canaryEnv, "BWSYNTH-TESTCANARY-0123456789")

	if _, err := prepareRunRoot(options{}); err == nil {
		t.Fatal("prepareRunRoot accepted an empty run root")
	}
}

// TestUsageListsEverySubcommand guards against a subcommand that exists but is undiscoverable,
// and against one listed in the help that the dispatch does not accept.
func TestUsageListsEverySubcommand(t *testing.T) {
	_, stderr, err := runCLI(t)
	if err == nil {
		t.Fatal("run with no arguments returned no error")
	}
	for _, cmd := range []string{"import", "adopt", "recover", "verify-order", "baseline-verify", "schema-report"} {
		if !strings.Contains(stderr, cmd) {
			t.Errorf("the usage text does not list %q", cmd)
		}
	}
}

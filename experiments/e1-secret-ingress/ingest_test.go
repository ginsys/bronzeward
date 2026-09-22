package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
)

// miniConfig is a Talos-shaped fragment small enough to reason about. The values are obviously
// synthetic: nothing in these tests may resemble a real credential, and a fixture that did would
// end up in a committed evidence bundle.
const miniConfig = `machine:
  token: e1-test-machine-token
  ca:
    crt: e1-test-machine-ca-certificate
    key: e1-test-machine-ca-key
cluster:
  id: e1-test-cluster-id
  secret: e1-test-cluster-secret
  apiServer:
    extraArgs:
      oidc-client-secret: e1-test-oidc-client-secret
`

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func loadMini(t *testing.T) *document.Document {
	t.Helper()
	doc, err := document.Load([]byte(miniConfig))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	return doc
}

// TestIngestRefusesBeforeOpeningAnything covers the guards that run before a database or a provider
// is contacted. Each would otherwise surface as a connection error, which an operator reads as an
// environment problem rather than as a wrong invocation.
func TestIngestRefusesBeforeOpeningAnything(t *testing.T) {
	t.Setenv(canaryEnv, "BWSYNTH-e1-test-canary")
	config := writeFile(t, "controlplane.yaml", miniConfig)
	root := filepath.Join(t.TempDir(), "run")

	cases := []struct {
		name  string
		args  []string
		wants string
	}{
		{"no config", []string{"--run-root=" + root, "import"}, "--config is required"},
		{
			"both mark sources",
			[]string{"--run-root=" + root, "--config=" + config, "--marks=/dev/null", "--mark-suffix=key", "import"},
			"cannot both be set",
		},
		{
			"redact without persist",
			[]string{"--run-root=" + root, "--config=" + config, "--redact-after", "import"},
			"needs --persist-first",
		},
		{
			"misspelled leak surface",
			[]string{"--run-root=" + root, "--config=" + config, "--leak-at=applog", "import"},
			"unknown leak surface",
		},
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

// TestSchemaReportMeasuresAgainstAGroundTruthItDidNotProduce is the criterion-4 path end to end. It
// needs neither a database nor a provider, which is the point: the denominator comes from a file,
// so it cannot quietly become "whatever the detector found".
func TestSchemaReportMeasuresAgainstAGroundTruthItDidNotProduce(t *testing.T) {
	config := writeFile(t, "controlplane.yaml", miniConfig)
	// The truth includes the OIDC client secret, which the Talos rules deliberately do not cover,
	// and excludes machine.ca.crt, which is a certificate sitting beside its key. Both are there to
	// make the two numbers move independently.
	truth := writeFile(t, "truth.txt", strings.Join([]string{
		"doc[0].machine.token",
		"doc[0].machine.ca.key",
		"doc[0].cluster.id",
		"doc[0].cluster.secret",
		"doc[0].cluster.apiServer.extraArgs.oidc-client-secret",
	}, "\n")+"\n")

	stdout, _, err := runCLI(t, "--config="+config, "schema-report", truth)
	if err != nil {
		t.Fatalf("schema-report: %v", err)
	}

	// The miss must be reported as a miss. If the detector ever grows a rule for it, this fails and
	// the report's limits section has to change with the code rather than drift away from it.
	if !strings.Contains(stdout, "doc[0].cluster.apiServer.extraArgs.oidc-client-secret") {
		t.Errorf("the report does not name the path the detector misses:\n%s", stdout)
	}
	// Both denominators, never a single score: one number hides which of the two moved. The figures
	// are asserted, not the words: the footer says "recall" on every run, so the bare word could not
	// fail. Truth is 5 paths and the rules find 4 of them, missing the OIDC secret; they flag nothing
	// else, the certificate included, so all 4 of their hits are true.
	if want := "recall 4/5, precision 4/4"; !strings.Contains(stdout, want) {
		t.Errorf("the report does not give %q:\n%s", want, stdout)
	}
	if strings.Contains(strings.ToLower(stdout), "f1") || strings.Contains(strings.ToLower(stdout), "f-score") {
		t.Errorf("the report gives a combined score, which §6.9 forbids the claim behind:\n%s", stdout)
	}
	// §6.9's limitation has to travel with the numbers, not sit in a document read separately.
	if !strings.Contains(stdout, "§6.9") {
		t.Errorf("the report does not carry the completeness limitation:\n%s", stdout)
	}

	// No value from the configuration may appear in the output: the report is committed evidence.
	for _, value := range []string{"e1-test-machine-token", "e1-test-cluster-secret", "e1-test-machine-ca-key"} {
		if strings.Contains(stdout, value) {
			t.Errorf("the report printed the value at a detected path: %q", value)
		}
	}
}

// TestSchemaReportRefusesAGroundTruthThatDoesNotFit checks a truth file naming a path the
// configuration does not have is an error. Silently ignoring it would shrink the denominator and
// improve recall by deleting the cases the detector failed.
func TestSchemaReportRefusesAGroundTruthThatDoesNotFit(t *testing.T) {
	config := writeFile(t, "controlplane.yaml", miniConfig)
	truth := writeFile(t, "truth.txt", "doc[0].machine.token\ndoc[0].machine.nonexistent.key\n")

	if _, _, err := runCLI(t, "--config="+config, "schema-report", truth); err == nil {
		t.Fatal("schema-report accepted a ground truth naming a path the configuration lacks")
	} else if !strings.Contains(err.Error(), "doc[0].machine.nonexistent.key") {
		t.Errorf("the error does not name the offending path: %v", err)
	}
}

// TestPlainIndexTakesTheValuesAsRead covers the map the persist-first control writes. It exists to
// carry plaintext, and a version that quietly skipped a path would weaken the control without
// changing its caption.
func TestPlainIndexTakesTheValuesAsRead(t *testing.T) {
	doc := loadMini(t)
	paths := []string{"doc[0].machine.token", "doc[0].cluster.secret"}

	index := plainIndex(doc, paths)
	if len(index) != len(paths) {
		t.Fatalf("plainIndex gave %d entries for %d paths", len(index), len(paths))
	}
	if index["doc[0].machine.token"] != "e1-test-machine-token" {
		t.Errorf("plainIndex did not take the value as read: %q", index["doc[0].machine.token"])
	}

	// A path that is not in the document is dropped rather than stored empty: an empty value would
	// scan clean and be read as the control having covered that path.
	if got := plainIndex(doc, []string{"doc[0].machine.absent"}); len(got) != 0 {
		t.Errorf("plainIndex invented an entry for an absent path: %v", got)
	}
}

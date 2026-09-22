package detect

import (
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
)

// sample carries one of each interesting case: a recognised secret, the public certificate that
// sits beside it, and two secrets in locations the rules do not cover.
const sample = `version: v1alpha1
machine:
  type: controlplane
  token: machine-join-token
  ca:
    crt: public-certificate
    key: machine-ca-private-key
  files:
    - path: /var/etc/creds.conf
      content: password=hidden-in-a-file
cluster:
  id: cluster-id-value
  secret: cluster-secret-value
  ca:
    crt: public-cluster-certificate
    key: cluster-ca-private-key
  apiServer:
    extraArgs:
      oidc-client-secret: hidden-in-an-extra-arg
`

func load(t *testing.T) *document.Document {
	t.Helper()
	d, err := document.Load([]byte(sample))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	return d
}

// TestSchemaFindsTheKnownLocations covers the recall half.
func TestSchemaFindsTheKnownLocations(t *testing.T) {
	got := Paths(Schema(load(t), TalosRules()))
	want := []string{
		"cluster.ca.key",
		"cluster.id",
		"cluster.secret",
		"machine.ca.key",
		"machine.token",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Schema() = %v\nwant %v", got, want)
	}
}

// TestSchemaDoesNotFlagPublicCertificates is the precision case the rule table was written around.
// A CA key and its certificate differ by three characters and sit under the same mapping; flagging
// the certificate would put a public value in a secret store.
func TestSchemaDoesNotFlagPublicCertificates(t *testing.T) {
	for _, path := range Paths(Schema(load(t), TalosRules())) {
		if strings.HasSuffix(path, ".crt") {
			t.Errorf("the rules flagged %s, which is a public certificate", path)
		}
	}
}

// TestSchemaMissesWhatItWasNeverToldAbout is a deliberate assertion of a limitation, not a bug
// report. §6.9 forbids claiming completeness, and the experiment's method is to demonstrate the
// misses rather than to caveat them: these two paths hold secrets and the schema rules do not see
// them, so an operator must mark them by hand.
//
// If this test ever fails because a rule was added, the report's limits section has to change with
// it — which is the point of asserting it here rather than writing it in prose.
func TestSchemaMissesWhatItWasNeverToldAbout(t *testing.T) {
	found := map[string]bool{}
	for _, p := range Paths(Schema(load(t), TalosRules())) {
		found[p] = true
	}

	for _, missed := range []string{
		"machine.files[0].content",
		"cluster.apiServer.extraArgs.oidc-client-secret",
	} {
		if _, ok := load(t).Get(missed); !ok {
			t.Fatalf("the fixture does not contain %s, so this test asserts nothing", missed)
		}
		if found[missed] {
			t.Errorf("the schema rules now find %s; the report's limits section must be updated", missed)
		}
	}
}

// TestSchemaReportsAnEmptyKnownField checks a recognised location with no value is still reported.
// Dropping it would make recall depend on whether this environment happened to populate the field,
// which is the denominator problem the report has to state rather than hide.
func TestSchemaReportsAnEmptyKnownField(t *testing.T) {
	d, err := document.Load([]byte("machine:\n  token: \"\"\n"))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	if got := Paths(Schema(d, TalosRules())); strings.Join(got, ",") != "machine.token" {
		t.Errorf("Schema() = %v, want machine.token reported despite being empty", got)
	}
}

// TestPatternMatching pins the matcher, including the two things it deliberately will not do.
func TestPatternMatching(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"machine.token", "machine.token", true},
		{"machine.token", "machine.tokens", false},
		{"machine.token", "cluster.machine.token", false},
		{"machine.*.key", "machine.ca.key", true},
		{"machine.*.key", "machine.ca.sub.key", false},
		{"machine.files[*].content", "machine.files[0].content", true},
		{"machine.files[*].content", "machine.files[12].content", true},
		{"machine.files[*].content", "machine.files.content", false},
		// No `**`: a pattern that crosses an arbitrary number of levels is how a detector starts
		// matching locations nobody reviewed.
		{"machine.**.key", "machine.ca.key", false},
	}

	for _, c := range cases {
		if got := matches(c.pattern, c.path); got != c.want {
			t.Errorf("matches(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// TestAssessSplitsTheThreeSets covers the comparison the report is built from.
func TestAssessSplitsTheThreeSets(t *testing.T) {
	a := Assess(
		[]string{"machine.token", "machine.ca.key", "machine.ca.crt"},
		[]string{"machine.token", "machine.ca.key", "machine.files[0].content"},
	)

	if strings.Join(a.Correct, ",") != "machine.ca.key,machine.token" {
		t.Errorf("Correct = %v", a.Correct)
	}
	if strings.Join(a.Spurious, ",") != "machine.ca.crt" {
		t.Errorf("Spurious = %v", a.Spurious)
	}
	if strings.Join(a.Missed, ",") != "machine.files[0].content" {
		t.Errorf("Missed = %v", a.Missed)
	}

	if n, d := a.Recall(); n != 2 || d != 3 {
		t.Errorf("Recall() = %d/%d, want 2/3", n, d)
	}
	if n, d := a.Precision(); n != 2 || d != 3 {
		t.Errorf("Precision() = %d/%d, want 2/3", n, d)
	}
}

// TestAssessOnAnEmptyTruth checks the degenerate case returns 0/0 rather than a ratio. A detector
// cannot be said to recall anything when there was nothing to recall, and a printed 1.00 there
// would be the most misleading number in the whole report.
func TestAssessOnAnEmptyTruth(t *testing.T) {
	a := Assess([]string{"machine.ca.crt"}, nil)
	if n, d := a.Recall(); n != 0 || d != 0 {
		t.Errorf("Recall() = %d/%d, want 0/0", n, d)
	}
	if n, d := a.Precision(); n != 0 || d != 1 {
		t.Errorf("Precision() = %d/%d, want 0/1", n, d)
	}
}

// TestAssessmentStringPrintsDenominatorsAndMembers pins the output format §6.9 requires: integers
// with both denominators, every set listed, and no derived score a reader could quote as a
// completeness claim.
func TestAssessmentStringPrintsDenominatorsAndMembers(t *testing.T) {
	out := Assess(
		[]string{"machine.token", "machine.ca.crt"},
		[]string{"machine.token", "machine.files[0].content"},
	).String()

	for _, want := range []string{
		"recall 1/2", "precision 1/2",
		"machine.ca.crt", "machine.files[0].content", "machine.token",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendering does not contain %q:\n%s", want, out)
		}
	}
	// An F-score or any single ratio would let a reader quote one number as completeness.
	for _, unwanted := range []string{"F1", "f1", "0.5", "50%"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the rendering contains a derived score %q:\n%s", unwanted, out)
		}
	}
}

// TestAssessmentStringSaysNoneRatherThanNothing checks an empty set prints a word. A blank after
// "missed" reads as an omission from the report rather than as an empty set.
func TestAssessmentStringSaysNoneRatherThanNothing(t *testing.T) {
	out := Assess([]string{"a"}, []string{"a"}).String()
	if !strings.Contains(out, "spurious (0): none") {
		t.Errorf("an empty set did not print a word:\n%s", out)
	}
}

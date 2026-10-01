package ingest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestGuard: compilation.md §4.2. Each case copies an extracted value (the schema-identified
// machine.token) somewhere substitution does not reach. The guard must refuse with the rule and
// the copy's path, never the value; the control builds the same candidate with the guard off and
// finds the value in it, so the refusal is the guard's.
func TestGuard(t *testing.T) {
	const s = secretText
	for _, tc := range []struct {
		name  string
		text  string
		marks []string
		rule  Rule
		path  string
	}{
		{"copied as another field", "machine:\n  token: " + s + "\n  nodeLabels:\n    copy: " + s + "\n",
			nil, RuleGuardValue, "doc[0]/machine/nodeLabels/copy"},
		{"copied as an escaped string over two lines", "machine:\n  token: " + s + "\n  nodeAnnotations:\n    copy: \"\\x" + fmt.Sprintf("%x", s[0]) + s[1:10] + "\\\n      " + s[10:] + "\"\n",
			nil, RuleGuardValue, "doc[0]/machine/nodeAnnotations/copy"},
		{"copied as a mapping key", "machine:\n  token: " + s + "\n  nodeLabels:\n    " + s + ": x\n",
			nil, RuleGuardValue, "doc[0]/machine/nodeLabels/<redacted>"},
		{"inside a URL", "machine:\n  token: " + s + "\n  kubelet:\n    extraArgs:\n      u: https://user:" + s + "@example.test/\n",
			nil, RuleGuardSubstring, "doc[0]/machine/kubelet/extraArgs/u"},
		{"inside a join command in document 1", "machine:\n  token: " + s + "\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: join-" + s + "\n",
			nil, RuleGuardSubstring, "doc[1]/hostname"},
		{"inside unidentified embedded text", "machine:\n  token: " + s + "\ncluster:\n  inlineManifests:\n    - name: m\n      contents: |\n        kind: Secret\n        stringData:\n          p: " + s + "\n",
			nil, RuleGuardSubstring, "doc[0]/cluster/inlineManifests/0/contents"},
		{"inside a comment", "machine:\n  token: " + s + "\n  # the token is " + s + "\n  type: worker\n",
			nil, RuleGuardSubstring, "doc[0]/machine/type"},
		// The decoder gives a "---" line comment to the first node; the encoder would drop a
		// document node's own line comment.
		{"inside a document marker comment", "--- # the token is " + s + "\nmachine:\n  token: " + s + "\n",
			nil, RuleGuardSubstring, "doc[0]/machine"},
		{"equal to a reference already present", "machine:\n  token: !bwref s-abc\n  nodeLabels:\n    x: s-abc\n",
			[]string{"doc[0]/machine/nodeLabels/x"}, RuleGuardValue, "doc[0]/machine/token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(t, tc.text, tc.marks...)
			if strings.Contains(tc.text, "!bwref s-abc") {
				req.Declarations.References = map[string]Reference{"s-abc": str("string")}
			}
			_, err := Extract(req)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != tc.rule {
				t.Fatalf("got %v, want a %s refusal", err, tc.rule)
			}
			if !slices.Contains(r.Paths, tc.path) {
				t.Errorf("paths %v, want %s", r.Paths, tc.path)
			}
			copyText := s
			if len(tc.marks) > 0 {
				copyText = "s-abc"
			}
			for _, text := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", r)} {
				if strings.Contains(text, s) {
					t.Errorf("the refusal quotes the value: %s", text)
				}
			}
			if strings.Contains(tc.text, "copy: \"") && strings.Contains(tc.text, s) && strings.Count(tc.text, s) != 1 {
				t.Fatal("the escaped copy is not escaped")
			}
			control, err := extract(req, false)
			if err != nil {
				t.Fatalf("control: %v", err)
			}
			if !strings.Contains(string(control.docs), copyText) {
				t.Errorf("control: the unguarded candidate does not hold the copy:\n%s", control.docs)
			}
		})
	}
}

// TestGuardSkipsThisRunsReferences: the substring search skips the content of this run's
// !bwref nodes (compilation.md §4.2), so a value found only inside a minted name passes.
func TestGuardSkipsThisRunsReferences(t *testing.T) {
	c, err := Extract(request(t, "machine:\n  nodeLabels:\n    k: \"-\"\n", "doc[0]/machine/nodeLabels/k"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.docs), refTag+" s-") {
		t.Fatalf("no reference in\n%s", c.docs)
	}
}

func TestRedactRefusal(t *testing.T) {
	exs := []extraction{{plain: secretText}, {plain: map[string]any{"k": labelSecret}}}
	err := redactRefusal(refuse(RuleReservedText, "doc[0]/a/x-"+secretText+"/b", "doc[1]/"+labelSecret, "not a path "+secretText, "doc[0]/plain"), searchTexts(extractedScalars(exs)))
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != RuleReservedText {
		t.Fatalf("%v", err)
	}
	want := []string{"doc[0]/a/<redacted>/b", "doc[1]/<redacted>", "<redacted>", "doc[0]/plain"}
	if !slices.Equal(r.Paths, want) {
		t.Errorf("paths %v", r.Paths)
	}
	if other := errors.New("other"); redactRefusal(other, nil) != other {
		t.Error("a non-refusal changed")
	}
}

// TestSameScalar: redaction's equality is the guard's: same boolean, or same number across
// integer, hexadecimal and float spellings, without truncating fractions or wrapping signs.
func TestSameScalar(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0x4cb2f", "314159", true},
		{"3.14159e5", "314159", true},
		{"314159.0", "314159", true},
		{"314159.5", "314159", false},
		{"TRUE", "true", true},
		{"1", "true", false},
		{"18446744073709551615", "18446744073709551615", true},
		{"18446744073709551615", "-1", false},
		{"-1", "18446744073709551615", false},
		{"0", "-0", true},
		{"0x20000000000001", "9007199254740992", true}, // equal when the key was tagged !!float
		{"abc", "abc", false},
	}
	for _, tc := range cases {
		if got := sameScalar(tc.a, tc.b); got != tc.want {
			t.Errorf("sameScalar(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestExtractRefusesReservedText(t *testing.T) {
	_, err := Extract(request(t, "machine:\n  nodeLabels:\n    k: \"!bwref x\"\n"))
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != RuleReservedText {
		t.Fatalf("got %v", err)
	}
}

// TestGuardNamesAndEmbeddedText: copies the per-node walk does not reach as scalars — anchor and
// alias names, a declared embedded document's whole text and its document comments — are refused,
// each with a control showing the unguarded candidate holds the copy.
func TestGuardNamesAndEmbeddedText(t *testing.T) {
	embeddedDecl := Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}
	for _, tc := range []struct {
		name, text, path string
		decl             Declarations
	}{
		{name: "anchor and alias named by the value",
			text: "machine:\n  token: &" + secretText + " " + secretText + "\ncluster:\n  token: *" + secretText + "\n",
			path: "doc[0]/cluster/token"},
		{name: "embedded text copied whole",
			text: "machine:\n  token: \"password: " + secretText + "\"\n" + manifestStream("password: "+secretText+"\n"),
			path: manifestPath, decl: embeddedDecl},
		{name: "embedded document comment",
			text: "machine:\n  token: " + secretText + "\n" + manifestStream("kind: Secret\n# "+secretText+"\n"),
			path: manifestPath, decl: embeddedDecl},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := request(t, tc.text)
			req.Declarations = tc.decl
			_, err := Extract(req)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != RuleGuardSubstring || !slices.Contains(r.Paths, tc.path) {
				t.Fatalf("got %v, want a %s refusal at %s", err, RuleGuardSubstring, tc.path)
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, r), secretText) {
				t.Errorf("the refusal quotes the input: %v", err)
			}
			control, err := extract(req, false)
			if err != nil || !strings.Contains(string(control.docs), secretText) {
				t.Fatalf("control: %v", err)
			}
		})
	}
}

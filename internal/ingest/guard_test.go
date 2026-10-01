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
	err := redactRefusal(refuse(RuleReservedText, "doc[0]/a/x-"+secretText+"/b", "doc[1]/"+labelSecret, "not a path "+secretText, "doc[0]/plain"), exs)
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != RuleReservedText {
		t.Fatalf("%v", err)
	}
	want := []string{"doc[0]/a/<redacted>/b", "doc[1]/<redacted>", "<redacted>", "doc[0]/plain"}
	if !slices.Equal(r.Paths, want) {
		t.Errorf("paths %v", r.Paths)
	}
	if other := errors.New("other"); redactRefusal(other, exs) != other {
		t.Error("a non-refusal changed")
	}
}

func TestExtractRefusesReservedText(t *testing.T) {
	_, err := Extract(request(t, "machine:\n  nodeLabels:\n    k: \"!bwref x\"\n"))
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != RuleReservedText {
		t.Fatalf("got %v", err)
	}
}

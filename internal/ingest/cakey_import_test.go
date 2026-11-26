package ingest

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// An import's machine.ca.key is set apart (compilation.md §2.3 step 3a) and guarded (§4.2).

var offeredCAKey = base64.StdEncoding.EncodeToString([]byte(syntheticCAKey))

const importToken = "token-secret-5c1d"

// importDoc is a control plane stream with the synthetic key at machine.ca.key and extra under machine.
func importDoc(extra string) string {
	return "version: v1alpha1\nmachine:\n  type: controlplane\n  token: " + importToken +
		"\n  ca:\n    key: " + offeredCAKey + "\n" + extra
}

func importRequest(t *testing.T, text string, marks ...string) Request {
	t.Helper()
	req := request(t, text, marks...)
	req.Import = true
	return req
}

func matched(t *testing.T, c *Candidate) {
	t.Helper()
	if err := c.MatchCAKey(func(o string) bool { return o == offeredCAKey }); err != nil {
		t.Fatalf("match: %v", err)
	}
}

func TestImportSetsTheCAKeyApart(t *testing.T) {
	c, err := Extract(importRequest(t, importDoc("")))
	if err != nil {
		t.Fatal(err)
	}
	if !c.OffersCAKey() {
		t.Fatal("no key was offered")
	}
	matched(t, c)
	s, calls := commitAll(t, c)
	if len(calls) != 1 || calls[0].name == CAKeyName {
		t.Fatalf("generations %v, want the token's alone", calls)
	}
	docs := string(s.Documents())
	if !strings.Contains(docs, "key: !bwref "+CAKeyName) || strings.Contains(docs, offeredCAKey) {
		t.Fatalf("the stored stream does not hold the typed reference in the key's place:\n%s", docs)
	}
	if got := s.Declarations().References[CAKeyName]; got != (Reference{Kind: provider.KindCAKey}) {
		t.Fatalf("declaration %+v", got)
	}
}

// Nothing is committed until the key was offered and matched, and a candidate renders no key.
func TestImportCommitWaitsForTheMatch(t *testing.T) {
	c, err := Extract(importRequest(t, importDoc("")))
	if err != nil {
		t.Fatal(err)
	}
	if out := fmt.Sprintf("%v %+v %#v", *c, *c, *c); strings.Contains(out, offeredCAKey) {
		t.Fatal("the candidate renders the key")
	}
	none := func(context.Context, string, provider.Value) error { t.Fatal("a generation was created"); return nil }
	if _, err := c.Commit(context.Background(), none); err == nil {
		t.Fatal("committed with the key never offered")
	}
	err = c.MatchCAKey(func(string) bool { return false })
	if got := ruleOf(t, err); got != RuleCAKeyMismatch {
		t.Fatalf("rule %s", got)
	}
	if strings.Contains(err.Error(), offeredCAKey) || !strings.Contains(err.Error(), "doc[0]/machine/ca/key") {
		t.Fatalf("the refusal: %v", err)
	}
	if _, err := c.Commit(context.Background(), none); err == nil {
		t.Fatal("committed after a mismatch")
	}
}

func TestImportOffersNothingWithoutAKey(t *testing.T) {
	for name, req := range map[string]Request{
		"worker":         importRequest(t, "version: v1alpha1\nmachine:\n  type: worker\n  token: "+importToken+"\n"),
		"not an import":  request(t, importDoc("")),
		"already a name": importRequest(t, caKeyDoc),
	} {
		t.Run(name, func(t *testing.T) {
			if name == "already a name" {
				req.Declarations = caKeyDecl(CAKeyName, Reference{Kind: provider.KindCAKey})
				if _, err := Extract(req); ruleOf(t, err) != RuleBadDeclaration {
					t.Fatalf("a request declared the kind: %v", err)
				}
				return
			}
			c, err := Extract(req)
			if err != nil {
				t.Fatal(err)
			}
			if c.OffersCAKey() {
				t.Fatal("a key was offered")
			}
		})
	}
}

// Both forms of the key are guarded: a copy left in the document, and one inside a payload step 6
// would write.
func TestImportGuardsTheCAKey(t *testing.T) {
	pemBlock := "      " + strings.ReplaceAll(strings.TrimSuffix(syntheticCAKey, "\n"), "\n", "\n      ") + "\n"
	for name, c := range map[string]struct {
		extra string
		marks []string
	}{
		"base64 copy":              {extra: "  nodeLabels:\n    note: " + offeredCAKey + "\n"},
		"pem copy":                 {extra: "  nodeLabels:\n    note: |\n" + pemBlock},
		"base64 in a marked file":  {extra: "  files:\n    - content: k=" + offeredCAKey + "\n      path: /var/x\n      op: create\n", marks: []string{"doc[0]/machine/files/0/content"}},
		"pem as a marked file":     {extra: "  files:\n    - content: |\n" + "  " + strings.ReplaceAll(pemBlock, "\n      ", "\n        ") + "      path: /var/x\n      op: create\n", marks: []string{"doc[0]/machine/files/0/content"}},
		"inside a schema secret":   {extra: "cluster:\n  token: " + offeredCAKey + "\n"},
		"a member of a marked map": {extra: "  nodeLabels:\n    note: " + offeredCAKey + "\n", marks: []string{"doc[0]/machine/nodeLabels"}},
		"a key of a marked map":    {extra: "  nodeLabels:\n    x" + offeredCAKey + ": a\n", marks: []string{"doc[0]/machine/nodeLabels"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(importRequest(t, importDoc(c.extra), c.marks...))
			if got := ruleOf(t, err); got != RuleGuardValue && got != RuleGuardSubstring {
				t.Fatalf("rule %s", got)
			}
			if strings.Contains(err.Error(), offeredCAKey) {
				t.Fatalf("the refusal holds the key: %v", err)
			}
		})
	}
}

// The mutation control of the payload comparison (persistence-api.md §16): without it the marked
// copy is committed as a generation.
func TestImportPayloadCheckControl(t *testing.T) {
	doc := importDoc("  nodeLabels:\n    note: " + offeredCAKey + "\n")
	req := importRequest(t, doc, "doc[0]/machine/nodeLabels/note")
	if _, err := Extract(req); ruleOf(t, err) != RuleGuardValue {
		t.Fatalf("not refused: %v", err)
	}
	was := checkPayloads
	checkPayloads = func([]extraction) error { return nil }
	t.Cleanup(func() { checkPayloads = was })
	c, err := Extract(req)
	if err != nil {
		t.Fatalf("without the payload check: %v", err)
	}
	matched(t, c)
	if _, calls := commitAll(t, c); len(calls) != 2 {
		t.Fatalf("%d generations, want the token's and the one holding the copy of the key", len(calls))
	}
}

// A mapping extracted whole would carry the key into a generation.
func TestImportRefusesAMappingHoldingTheCAKey(t *testing.T) {
	_, err := Extract(importRequest(t, importDoc(""), "doc[0]/machine/ca"))
	if got := ruleOf(t, err); got != RuleMarkKind {
		t.Fatalf("rule %s", got)
	}
}

// The reference stands once in a stream: a second key could not be matched on its own.
func TestImportRefusesASecondCAKey(t *testing.T) {
	other := base64.StdEncoding.EncodeToString([]byte("another synthetic key\n"))
	first := strings.Replace(importDoc(""), offeredCAKey, other, 1)
	for name, doc := range map[string]string{
		"another key first":  first + "---\n" + importDoc(""),
		"the same key twice": importDoc("") + "---\n" + importDoc(""),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(importRequest(t, doc))
			if got := ruleOf(t, err); got != RuleTagPlacement {
				t.Fatalf("rule %s", got)
			}
		})
	}
}

// The staged document of an import holds the reference: a later mark and the envelope accept it.
func TestImportReferenceSurvivesTheEnvelopeAndAMark(t *testing.T) {
	c, err := Extract(importRequest(t, importDoc("  nodeLabels:\n    note: plain-label\n")))
	if err != nil {
		t.Fatal(err)
	}
	matched(t, c)
	gens := map[string]string{}
	s, err := c.Commit(context.Background(), func(_ context.Context, name string, _ provider.Value) error {
		gens[name] = "gen/x"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, sum, err := Staged{Sanitized: s, Generations: gens,
		Baseline: Baseline{Ciphertext: "c", DigestKey: "k"}}.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	st, err := Open(plain, sum)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	p, err := ParsePath("doc[0]/machine/nodeLabels/note")
	if err != nil {
		t.Fatal(err)
	}
	next, err := Remark(st, []Path{p})
	if err != nil {
		t.Fatalf("remark: %v", err)
	}
	if next.OffersCAKey() {
		t.Fatal("a mark offered the key again")
	}
}

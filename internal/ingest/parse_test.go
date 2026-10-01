package ingest

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestParseKeepsEveryDocumentAndTags(t *testing.T) {
	u, err := Read(strings.NewReader("a: !bwref s-one\n---\nb: 2\n---\nc: [x, !bwref s-two]\n"), 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("%d documents", len(docs))
	}
	if n, ok := resolve(docs, 0, []string{"a"}); !ok || n.Tag != "!bwref" || n.Value != "s-one" {
		t.Errorf("doc[0]/a: %+v", n)
	}
	if n, ok := resolve(docs, 2, []string{"c", "1"}); !ok || n.Tag != "!bwref" {
		t.Errorf("doc[2]/c/1: %+v", n)
	}
	if n, ok := resolve(docs, 1, []string{"b"}); !ok || n.Tag != "!!int" || n.Kind != yaml.ScalarNode {
		t.Errorf("doc[1]/b: %+v", n)
	}
}

// TestParseRefusalQuotesNoInput: the decoder's messages can quote the input (an unknown alias
// names it); the refusal keeps only a line number (compilation.md §2.3 step 2).
func TestParseRefusalQuotesNoInput(t *testing.T) {
	for _, in := range []string{
		"a: 1\nb: *" + secretText + "\n",
		"a: 1\nb: c: " + secretText + "\n",
		"a: \"" + secretText + "\n",
		"a: 1\n\t" + secretText + ": 2\n",
		"a: !!int " + secretText + "\n" + "[" + secretText,
	} {
		_, err := parseStream([]byte(in))
		var r *Refusal
		if !errors.As(err, &r) || r.Rule != RuleParse {
			t.Errorf("%q: %v, want a %s refusal", in, err, RuleParse)
			continue
		}
		if strings.Contains(err.Error(), secretText) {
			t.Errorf("the refusal quotes the input: %v", err)
		}
	}
}

func TestParseRefusesEmptyStream(t *testing.T) {
	for _, in := range []string{"# only a comment\n", "\n\n"} {
		if _, err := parseStream([]byte(in)); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

// TestParseRefusesCyclicAlias: an alias inside the node it names makes an infinite graph; it is
// refused at parse, in the outer stream and in an identified embedded document.
func TestParseRefusesCyclicAlias(t *testing.T) {
	req := request(t, "machine: &m\n  nodeLabels: *m\n")
	if _, err := Extract(req); !isRule(err, RuleParse) {
		t.Fatalf("outer stream: %v", err)
	}
	req = request(t, "machine:\n  token: "+secretText+"\n"+manifestStream("a: &m\n  b: *m\n"))
	req.Declarations.Embedded = []Embedded{{Path: manifestPath, Format: "yaml"}}
	if _, err := Extract(req); !isRule(err, RuleEmbedded) {
		t.Fatalf("embedded document: %v", err)
	}
}

// TestParseRefusesDuplicateKeys: a pointer through a mapping that holds a key twice names two
// nodes; extracting the first would leave the other value in the stream, unextracted and unequal
// to what the guard compares. YAML forbids duplicate keys, so they are refused at parse, keys
// compared by their text as a pointer token matches them. The refusal quotes no key.
func TestParseRefusesDuplicateKeys(t *testing.T) {
	for _, in := range []string{
		"a: 1\na: 2\n",
		"a:\n  " + secretText + ": 1\n  '" + secretText + "': 2\n",
		"a: [{b: 1, b: 2}]\n",
		"1: x\n\"1\": y\n",
		"? &k " + secretText + "\n: 1\n? *k\n: 2\n",
	} {
		_, err := parseStream([]byte(in))
		if !isRule(err, RuleParse) {
			t.Errorf("%q: %v, want a %s refusal", in, err, RuleParse)
			continue
		}
		if strings.Contains(err.Error(), secretText) {
			t.Errorf("the refusal quotes the input: %v", err)
		}
	}
	manifest := "kind: Secret\nstringData:\n  password: one\n  password: " + secretText + "\n"
	req := request(t, "machine:\n  token: "+secretText+"x\n"+manifestStream(manifest), manifestPath+"|yaml/stringData/password")
	req.Declarations.Embedded = []Embedded{{Path: manifestPath, Format: "yaml"}}
	if _, err := Extract(req); !isRule(err, RuleEmbedded) {
		t.Errorf("embedded document: %v, want a %s refusal", err, RuleEmbedded)
	}
}

func isRule(err error, rule Rule) bool {
	var r *Refusal
	return errors.As(err, &r) && r.Rule == rule
}

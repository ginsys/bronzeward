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

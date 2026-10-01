package ingest

import (
	"errors"
	"slices"
	"testing"
)

func TestParsePathRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		in      string
		doc     int
		pointer []string
		format  string
		inner   []string
	}{
		{"doc[0]", 0, nil, "", nil},
		{"doc[2]", 2, nil, "", nil},
		{"doc[0]/", 0, []string{""}, "", nil},
		{"doc[0]/machine/files/0/content", 0, []string{"machine", "files", "0", "content"}, "", nil},
		{"doc[0]/machine/nodeLabels/example.test~1role", 0, []string{"machine", "nodeLabels", "example.test/role"}, "", nil},
		{"doc[1]/a~0b/c~01", 1, []string{"a~b", "c~1"}, "", nil},
		{"doc[10]/x[0]/y.z", 10, []string{"x[0]", "y.z"}, "", nil},
		{"doc[0]/cluster/inlineManifests/0/contents|yaml/stringData/password", 0,
			[]string{"cluster", "inlineManifests", "0", "contents"}, "yaml", []string{"stringData", "password"}},
		{"doc[0]/a|json", 0, []string{"a"}, "json", nil},
	} {
		p, err := ParsePath(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if p.Doc != tc.doc || !slices.Equal(p.Pointer, tc.pointer) || p.Format != tc.format || !slices.Equal(p.Inner, tc.inner) {
			t.Errorf("%q parsed as %#v", tc.in, p)
		}
		if got := p.String(); got != tc.in {
			t.Errorf("%q formats back as %q", tc.in, got)
		}
	}
}

func TestParsePathRefusals(t *testing.T) {
	for _, in := range []string{
		"", "/machine", "machine/ca", "doc[]", "doc[01]/a", "doc[-1]", "doc[a]", "doc[0", "doc[0]x",
		"doc[1000001]", "doc[0]/a~2", "doc[0]/a~", "doc[0]/a|toml/b", "doc[0]/a|yaml/b|yaml/c",
		"doc[0]/a|yaml|json", "doc[0]/a|yamlx", "doc[0]|yaml/a",
	} {
		_, err := ParsePath(in)
		var r *Refusal
		if !errors.As(err, &r) || r.Rule != RuleBadPath {
			t.Errorf("%q: %v, want a %s refusal", in, err, RuleBadPath)
		}
	}
}

func TestResolve(t *testing.T) {
	docs, err := parseStream([]byte(`a:
  "x/y": 1
  "t~u": 2
  list: [p, q, &anchor {k: v}]
  alias: *anchor
---
second: here
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, value string
	}{
		{"doc[0]/a/x~1y", "1"},
		{"doc[0]/a/t~0u", "2"},
		{"doc[0]/a/list/1", "q"},
		{"doc[0]/a/list/2/k", "v"},
		{"doc[0]/a/alias/k", "v"},
		{"doc[1]/second", "here"},
	} {
		p, err := ParsePath(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		n, ok := resolve(docs, p.Doc, p.Pointer)
		if !ok || n.Value != tc.value {
			t.Errorf("%s: %v %v", tc.path, n, ok)
		}
	}
	for _, path := range []string{
		"doc[0]/a/list/3", "doc[0]/a/list/01", "doc[0]/a/list/-", "doc[0]/a/none", "doc[0]/a/x~1y/deeper",
		"doc[2]/second", "doc[0]/a/list/1/k",
	} {
		p, err := ParsePath(path)
		if err != nil {
			t.Fatal(err)
		}
		if n, ok := resolve(docs, p.Doc, p.Pointer); ok {
			t.Errorf("%s resolved to %v", path, n)
		}
	}
}

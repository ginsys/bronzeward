package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestCases holds the oracle to the expectations of sets/cases.json. The expectations were written
// from the issues before the oracle ran; a mismatch means the case, not the oracle, is rechecked
// (the oracle is go-yaml itself). "want": null marks a stream go-yaml refuses.
func TestCases(t *testing.T) {
	raw, err := os.ReadFile("sets/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string          `json:"name"`
		Doc  string          `json:"doc"`
		Want json.RawMessage `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// A missing key is a malformed case, not a refusal: only an explicit null is one.
			if len(c.Want) == 0 {
				t.Fatal(`the case has no "want" key`)
			}
			got := References(c.Doc)
			if string(c.Want) == "null" {
				if got.Err == "" {
					t.Fatalf("go-yaml accepted the stream, listing %v", got.Tags)
				}
				return
			}
			var want []string
			if err := json.Unmarshal(c.Want, &want); err != nil {
				t.Fatalf(`the case has a "want" that is no list of names: %v`, err)
			}
			if got.Err != "" {
				t.Fatalf("go-yaml refused the stream: %s", got.Err)
			}
			if !slices.Equal(got.Tags, want) {
				t.Fatalf("go-yaml lists %v, want %v", got.Tags, want)
			}
		})
	}
}

// TestTaggedKey holds the oracle to the server's rule for a tag on a key (tag-placement in
// internal/ingest/declare.go): a scalar key with the tag lists no name and marks the document unfit,
// and so does an alias key whose anchor is such a key. An alias key whose anchored node is a tagged
// value lists no name for that value either (ginsys/bronzeward#672). A tag on a value that no alias
// key repeats is read as before.
func TestTaggedKey(t *testing.T) {
	for _, c := range []struct {
		name  string
		doc   string
		tags  []string
		unfit bool
	}{
		{"implicit key", "!bwref x/foo: v\n", []string{}, true},
		{"explicit key", "? !bwref x/foo\n: v\n", []string{}, true},
		{"explicit key with an anchor", "? &e !bwref x/foo\n: v\n", []string{}, true},
		{"flow explicit key", "{? !bwref x/foo : v}\n", []string{}, true},
		{"alias key of a tagged key", "? &k !bwref x/foo\n: v\n*k : w\n", []string{}, true},
		// The implicit key rows of ginsys/bronzeward#629 that go-yaml accepts. The numbers are those of
		// the oracle batch in the refinement comment of that issue. Rows 50 and 51 are refused by
		// go-yaml, so the oracle has no reading for them.
		{"row 46: sequence entry key", "- !bwref x/foo:\n", []string{}, true},
		{"row 47: sequence entry key, continuation line", "- !bwref x/foo:\n    bar\n", []string{}, true},
		{"row 48: colon that ends a flow sequence, a bad name", "[!bwref x/foo:]\n", []string{}, true},
		{"row 49: key in a flow sequence value", "k: [!bwref x/foo:\nbar]\n", []string{}, true},
		{"row 55: flow mapping key", "{!bwref x/foo: v}\n", []string{}, true},
		{"row 56: flow sequence key", "[!bwref x/foo: v]\n", []string{}, true},
		{"row 57: anchor after the tag", "- !bwref &e x/foo:\n    bar\n", []string{}, true},
		{"row 58: anchor before the tag", "- &e !bwref x/foo:\n    bar\n", []string{}, true},
		{"nested key", "k:\n  !bwref x/foo: v\n", []string{}, true},
		{"value control", "a: !bwref x/foo\n", []string{"x/foo"}, false},
		{"sequence entry control", "- !bwref x/foo\n", []string{"x/foo"}, false},
		{"value of an explicit key control", "? a\n: !bwref x/foo\n", []string{"x/foo"}, false},
		// An alias key whose anchored node carries the tag is refused whatever the node is, so the
		// oracle lists no name for the node (ginsys/bronzeward#672).
		{"alias key of a tagged value", "a: &k !bwref x/foo\n*k : v\n", []string{}, true},
		{"alias key of a tagged value, the anchor after the tag", "a: !bwref &k x/foo\n*k : v\n", []string{}, true},
		{"alias key of a tagged value, no blank before the colon", "a: &k !bwref x/foo\n*k: v\n", []string{}, true},
		{"alias key of a tagged value, an explicit key", "a: &k !bwref x/foo\n? *k\n: v\n", []string{}, true},
		{"alias key of a tagged value in a flow mapping", "{a: &k !bwref x/foo, *k : v}\n", []string{}, true},
		{"alias key in a later document", "a: &k !bwref x/foo\n---\n*k : v\n", []string{}, true},
		{"alias key of the latest anchor, tagged", "a: &k 1\nb: &k !bwref x/foo\n*k : v\n", []string{}, true},
		{"alias key of the latest anchor, not tagged", "a: &k !bwref x/foo\nb: &k 1\n*k : v\n", []string{"x/foo"}, false},
		{"another tagged value stays listed", "a: &k !bwref x/foo\nc: !bwref x/bar\n*k : v\n", []string{"x/bar"}, true},
		{"alias as a value control", "a: &k !bwref x/foo\nb: *k\n", []string{"x/foo"}, false},
		{"alias in a sequence control", "- &k !bwref x/foo\n- *k\n", []string{"x/foo"}, false},
		{"quoted text that looks like an alias key control", "a: &k !bwref x/foo\n\"*k\": v\n", []string{"x/foo"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := References(c.doc)
			if got.Err != "" {
				t.Fatalf("go-yaml refused the stream: %s", got.Err)
			}
			if !slices.Equal(got.Tags, c.tags) || got.Unfit != c.unfit {
				t.Fatalf("got names %v, unfit %v; want %v, %v", got.Tags, got.Unfit, c.tags, c.unfit)
			}
		})
	}
}

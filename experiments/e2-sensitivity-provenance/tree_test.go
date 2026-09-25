package main

import (
	"strings"
	"testing"
)

const treeDoc = `machine:
    registries:
        config:
            r.test:
                auth:
                    password: p1
    certSANs:
        - 127.0.0.1
        - s2
cluster:
    inlineManifests:
        - name: dup
          contents: |
            stringData:
              password: first
        - name: dup
          contents: |
            stringData:
              password: second
---
apiVersion: v1alpha1
kind: TrustedRootsConfig
name: roots
certificates: c1
`

var treeEmbedded = []Embedded{{Doc: "v1alpha1", Path: "cluster/inlineManifests[name=dup]/contents", Format: "yaml", Identified: true}}

func leafPaths(t *testing.T, text string, emb []Embedded) map[string]string {
	t.Helper()
	tr, err := parseTree([]byte(text), emb)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, l := range tr.Leaves {
		out[l.Doc+" "+l.Path] = l.Node.Value
	}
	return out
}

func TestParseTreeNamesEveryLeaf(t *testing.T) {
	got := leafPaths(t, treeDoc, treeEmbedded)
	want := map[string]string{
		"v1alpha1 machine/registries/config/r.test/auth/password":                        "p1",
		"v1alpha1 machine/certSANs[0]":                                                   "127.0.0.1",
		"v1alpha1 machine/certSANs[1]":                                                   "s2",
		"v1alpha1 cluster/inlineManifests[name=dup#0]/name":                              "dup",
		"v1alpha1 cluster/inlineManifests[name=dup#0]/contents|yaml/stringData/password": "first",
		"v1alpha1 cluster/inlineManifests[name=dup#1]/name":                              "dup",
		"v1alpha1 cluster/inlineManifests[name=dup#1]/contents|yaml/stringData/password": "second",
		"TrustedRootsConfig/roots apiVersion":                                            "v1alpha1",
		"TrustedRootsConfig/roots kind":                                                  "TrustedRootsConfig",
		"TrustedRootsConfig/roots name":                                                  "roots",
		"TrustedRootsConfig/roots certificates":                                          "c1",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d leaves %v, want %d", len(got), got, len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}

func TestParseTreeLeavesUnidentifiedEmbeddedOpaque(t *testing.T) {
	emb := []Embedded{{Doc: "v1alpha1", Path: "cluster/inlineManifests[name=dup]/contents", Format: "yaml"}}
	got := leafPaths(t, treeDoc, emb)
	if _, ok := got["v1alpha1 cluster/inlineManifests[name=dup#0]/contents"]; !ok {
		t.Fatalf("the unidentified document is not one leaf: %v", got)
	}
}

func TestRenderWithoutTokensIsIdentity(t *testing.T) {
	out, err := render([]byte(treeDoc), treeEmbedded, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != treeDoc {
		t.Fatalf("render changed the text:\n%s", out)
	}
}

func TestRenderReplacesOnlyTheTokenedLeafByPosition(t *testing.T) {
	tr, err := parseTree([]byte(treeDoc), treeEmbedded)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, l := range tr.Leaves {
		if l.Path == "cluster/inlineManifests[name=dup#1]/contents|yaml/stringData/password" ||
			l.Path == "machine/registries/config/r.test/auth/password" {
			tokens[l.Pos] = "<redacted:x@1>"
		}
	}
	out, err := render([]byte(treeDoc), treeEmbedded, tokens)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, gone := range []string{"p1", "second"} {
		if strings.Contains(s, gone) {
			t.Errorf("%q still in the rendering:\n%s", gone, s)
		}
	}
	if !strings.Contains(s, "password: first") {
		t.Errorf("the other duplicate element was touched:\n%s", s)
	}
	if strings.Count(s, "<redacted:x@1>") != 2 {
		t.Errorf("want two tokens:\n%s", s)
	}
}

package main

import (
	"encoding/base64"
	"testing"
)

func forms(t *testing.T, text string, s ...Secret) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, f := range scan(text, s, "") {
		out[f.Secret+" "+f.Form] += f.Count
	}
	return out
}

func TestScanFindsTheExactValueOnlyOnce(t *testing.T) {
	s := Secret{ID: "reg-pass@1", Class: "ref", Kind: "str", Value: "BWSYNTH-registry-password"}
	got := forms(t, "a: BWSYNTH-registry-password\nb: BWSYNTH-registry-password\n", s)
	if len(got) != 1 || got["reg-pass@1 exact"] != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestScanFindsEscapedForms(t *testing.T) {
	s := Secret{ID: "p@1", Class: "ref", Kind: "str", Value: "BWSYNTH-esc\t\"q\"<&>"}
	if got := forms(t, `password: "BWSYNTH-esc\t\"q\"<&>"`, s); got["p@1 yaml-escaped"] != 1 {
		t.Fatalf("yaml: got %v", got)
	}
	bs := `\`
	jsonText := `{"token":"BWSYNTH-esc\t\"q\"` + bs + "u003c" + bs + "u0026" + bs + "u003e" + `"}`
	if got := forms(t, jsonText, s); got["p@1 json-escaped"] != 1 {
		t.Fatalf("json: got %v", got)
	}
}

func TestScanFindsBase64AndPlacedForms(t *testing.T) {
	s := Secret{ID: "ca@1", Class: "ref", Kind: "bytes", Value: "BWSYNTH-extra-ca",
		Placed: base64.StdEncoding.EncodeToString([]byte("BWSYNTH-extra-ca"))}
	if got := forms(t, "crt: "+s.Placed+"\n", s); got["ca@1 placed"] != 1 || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	s2 := Secret{ID: "x@1", Class: "ref", Kind: "str", Value: "BWSYNTH-plain-value"}
	if got := forms(t, base64.StdEncoding.EncodeToString([]byte(s2.Value)), s2); got["x@1 base64"] != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestScanFindsATalosPrefixQuote(t *testing.T) {
	s := Secret{ID: "port@1", Class: "ref", Kind: "str", Value: "BWSYNTH-not-a-port"}
	if got := forms(t, "cannot construct !!str `BWSYNTH...` into int", s); got["port@1 prefix"] != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestScanFindsAFragmentAndLines(t *testing.T) {
	s := Secret{ID: "script@1", Class: "ref", Kind: "str", Value: "#!/bin/sh\necho BWSYNTH-whole-content-line\n"}
	if got := forms(t, "  echo BWSYNTH-whole-content-line\n", s); got["script@1 line"] != 1 || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	s2 := Secret{ID: "p@1", Class: "ref", Kind: "str", Value: "BWSYNTH-registry-password"}
	if got := forms(t, "x registry-pass x", s2); got["p@1 fragment"] != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestScanIgnoresFragmentsThePublicTextHolds(t *testing.T) {
	s := Secret{ID: "p@1", Class: "ref", Kind: "str", Value: "ghcr.io/siderolabs/secret-x"}
	if got := scan("image: ghcr.io/siderolabs/kubelet", []Secret{s}, "image: ghcr.io/siderolabs/kubelet"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestScanFindsKeyedScalars(t *testing.T) {
	s := Secret{ID: "port@1", Class: "ref", Kind: "int", Value: "7446", Key: "port"}
	if got := forms(t, "+            port: 7446\n", s); got["port@1 keyed"] != 1 {
		t.Fatalf("got %v", got)
	}
	if got := forms(t, "port: 74460\nport: 7445\n", s); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

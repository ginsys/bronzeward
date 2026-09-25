package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func mustTree(t *testing.T, text string) *Tree {
	t.Helper()
	tr, err := parseTree([]byte(text), nil)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

var attrTracers = []Tracer{
	{ID: 0, Ref: "reg-pass", Kind: "str", Value: shapeTracer("BWSYNTH-registry-password", 0)},
	{ID: 1, Ref: "prism-port", Kind: "int", Value: intTracer(1)},
	{ID: 2, Ref: "extra-ca", Kind: "bytes", Raw: shapeTracer("BWSYNTH-extra-ca", 2),
		Value: base64.StdEncoding.EncodeToString([]byte(shapeTracer("BWSYNTH-extra-ca", 2)))},
}

func TestAttributeNamesTheTracerAtEachDifferingLeaf(t *testing.T) {
	real := mustTree(t, "a:\n    password: BWSYNTH-registry-password\n    port: 7446\n    ca: "+
		base64.StdEncoding.EncodeToString([]byte("BWSYNTH-extra-ca"))+"\n    user: u\n")
	trace := mustTree(t, "a:\n    password: "+attrTracers[0].Value+"\n    port: 61001\n    ca: "+attrTracers[2].Value+"\n    user: u\n")
	got, fails := attribute(real, trace, attrTracers)
	if len(fails) != 0 {
		t.Fatalf("fidelity failures: %v", fails)
	}
	want := map[string]int{"a/password": 0, "a/port": 1, "a/ca": 2}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, a := range got {
		id, ok := want[a.Path]
		if !ok || len(a.Tracers) != 1 || a.Tracers[0] != id {
			t.Errorf("%s attributed to %v", a.Path, a.Tracers)
		}
	}
}

func TestAttributeFailsClosedOnAnUnexplainedDifference(t *testing.T) {
	real := mustTree(t, "a:\n    password: BWSYNTH-registry-password\n")
	trace := mustTree(t, "a:\n    password: zz-not-a-tracer\n")
	_, fails := attribute(real, trace, attrTracers)
	if len(fails) != 1 || !strings.Contains(fails[0], "a/password") {
		t.Fatalf("want one fidelity failure at a/password, got %v", fails)
	}
}

func TestAttributeFailsClosedOnADifferentStructure(t *testing.T) {
	real := mustTree(t, "a:\n    - x\n    - y\n")
	trace := mustTree(t, "a:\n    - x\n")
	if _, fails := attribute(real, trace, attrTracers); len(fails) == 0 {
		t.Fatal("a structural difference was not reported")
	}
}

func TestAttributeFlipNamesTheBooleanLeaf(t *testing.T) {
	trace := mustTree(t, "machine:\n    install:\n        wipe: true\n        other: false\n")
	flip := mustTree(t, "machine:\n    install:\n        wipe: false\n        other: false\n")
	got, fails := attributeFlip(trace, flip, Tracer{ID: 4, Kind: "bool"})
	if len(fails) != 0 || len(got) != 1 || got[0].Path != "machine/install/wipe" || got[0].Tracers[0] != 4 {
		t.Fatalf("got %v, failures %v", got, fails)
	}
}

func TestFindTracersInAComposition(t *testing.T) {
	tr := mustTree(t, "a:\n    password: "+attrTracers[0].Value+"\n    port: 61001\n")
	got := findTracers(tr, attrTracers)
	if len(got[0]) != 1 || len(got[1]) != 1 || len(got[2]) != 0 {
		t.Fatalf("got %v", got)
	}
}

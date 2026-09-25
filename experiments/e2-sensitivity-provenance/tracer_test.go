package main

import (
	"strings"
	"testing"
)

func TestShapeTracerKeepsLengthLinesAndPunctuation(t *testing.T) {
	real := "BWSYNTH-esc \"q\" \\ tab\tend # x"
	got := shapeTracer(real, 7)
	if len(got) != len(real) {
		t.Fatalf("length %d, want %d: %q", len(got), len(real), got)
	}
	if !strings.HasPrefix(got, "zq007") {
		t.Fatalf("no head: %q", got)
	}
	for i := 5; i < len(real); i++ {
		r, g := real[i], got[i]
		alnum := func(c byte) bool {
			return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		}
		if alnum(r) != alnum(g) || (!alnum(r) && r != g) {
			t.Fatalf("byte %d: %q became %q in %q", i, r, g, got)
		}
	}
	if got == real {
		t.Fatal("tracer equals the value")
	}
}

func TestShapeTracerHeadsEveryLongLine(t *testing.T) {
	real := "#!/bin/sh\nexport E2SP_TOKEN=BWSYNTH-x\nab\n"
	got := shapeTracer(real, 12)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("line count changed: %q", got)
	}
	if !strings.HasPrefix(lines[0], "zq012") || !strings.HasPrefix(lines[1], "zq012") {
		t.Fatalf("a long line has no head: %q", got)
	}
	if lines[2] != "xx" || lines[3] != "" {
		t.Fatalf("short lines not mapped as expected: %q", got)
	}
}

func TestShapeTracerIsDistinctPerID(t *testing.T) {
	a, b := shapeTracer("BWSYNTH-same-value", 1), shapeTracer("BWSYNTH-same-value", 2)
	if a == b || strings.Contains(a, b) || strings.Contains(b, a) {
		t.Fatalf("tracers %q and %q are not distinct", a, b)
	}
}

func TestIntTracer(t *testing.T) {
	if intTracer(3) != "61003" {
		t.Fatalf("got %s", intTracer(3))
	}
}

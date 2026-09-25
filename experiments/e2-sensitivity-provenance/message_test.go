package main

import (
	"strings"
	"testing"
)

var msgTracers = []Tracer{
	{ID: 0, Ref: "long-port", Version: "1", Kind: "str", Value: shapeTracer("BWSYNTH-not-a-port-long", 0)},
	{ID: 1, Ref: "short-port", Version: "1", Kind: "str", Value: shapeTracer("BWSYNTH-42", 1)},
}

func tok(id int) string { return "<redacted:" + msgTracers[id].Ref + "@1>" }

func TestRedactMessageReplacesAFullQuote(t *testing.T) {
	real := `* "BWSYNTH-42" is not a valid DNS name`
	trace := `* "` + msgTracers[1].Value + `" is not a valid DNS name`
	got, outcome := redactMessage(real, trace, 1, 1, msgTracers, tok)
	if outcome != "redacted" || got != `* "<redacted:short-port@1>" is not a valid DNS name` {
		t.Fatalf("%s: %q", outcome, got)
	}
}

func TestRedactMessageReplacesTruncatedAndFullQuotesTogether(t *testing.T) {
	real := "line 4: cannot construct !!str `BWSYNTH...` into int\n  line 7: cannot construct !!str `BWSYNTH-42` into int\n"
	trace := "line 4: cannot construct !!str `" + msgTracers[0].Value[:7] + "...` into int\n  line 7: cannot construct !!str `" +
		msgTracers[1].Value + "` into int\n"
	got, outcome := redactMessage(real, trace, 1, 1, msgTracers, tok)
	want := "line 4: cannot construct !!str `<redacted:long-port@1>...` into int\n  line 7: cannot construct !!str `<redacted:short-port@1>` into int\n"
	if outcome != "redacted" || got != want {
		t.Fatalf("%s: %q", outcome, got)
	}
	if strings.Contains(got, "BWSYNTH") {
		t.Fatal("a prefix survived")
	}
}

func TestRedactMessageWithholdsWhenTheConstantPartsDiffer(t *testing.T) {
	real := `* "BWSYNTH-42" is not a valid DNS name, and more`
	trace := `* "` + msgTracers[1].Value + `" is not a valid DNS name`
	if got, outcome := redactMessage(real, trace, 1, 1, msgTracers, tok); outcome != "withheld" || strings.Contains(got, "BWSYNTH") {
		t.Fatalf("%s: %q", outcome, got)
	}
}

func TestRedactMessageWithholdsAnUnexplainedDifferenceWithoutTracers(t *testing.T) {
	if got, outcome := redactMessage("error: BWSYNTH-x", "error: other", 1, 1, msgTracers, tok); outcome != "withheld" || strings.Contains(got, "BWSYNTH") {
		t.Fatalf("%s: %q", outcome, got)
	}
}

func TestRedactMessageKeepsAValueFreeMessage(t *testing.T) {
	m := "* inline manifest name \"m1\" is duplicate\n"
	if got, outcome := redactMessage(m, m, 1, 1, msgTracers, tok); outcome != "verbatim" || got != m {
		t.Fatalf("%s: %q", outcome, got)
	}
	if _, outcome := redactMessage("", "", 0, 0, msgTracers, tok); outcome != "none" {
		t.Fatalf("empty messages: %s", outcome)
	}
}

func TestRedactMessageWithholdsWhenOnlyOneRunFailed(t *testing.T) {
	if _, outcome := redactMessage("ok", "ok", 1, 0, msgTracers, tok); outcome != "withheld" {
		t.Fatalf("got %s", outcome)
	}
}

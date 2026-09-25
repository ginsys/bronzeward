package main

import "testing"

func TestValueMatchReplacesWholeValuesAndLongLines(t *testing.T) {
	m := newValueMatcher([]string{"BWSYNTH-registry-password", "#!/bin/sh\necho BWSYNTH-line-two\nab", "true", "7446"})
	got, n := m.redact("a: BWSYNTH-registry-password\n  echo BWSYNTH-line-two\nwipe: true\nport: 7446\n")
	want := "a: <redacted:value>\n  <redacted:value>\nwipe: true\nport: 7446\n"
	if got != want || n != 2 {
		t.Fatalf("%d: %q", n, got)
	}
}

func TestValueMatchPrefersTheLongerValue(t *testing.T) {
	m := newValueMatcher([]string{"BWSYNTH-abc", "BWSYNTH-abc-longer"})
	if got, _ := m.redact("x BWSYNTH-abc-longer y"); got != "x <redacted:value> y" {
		t.Fatalf("%q", got)
	}
}

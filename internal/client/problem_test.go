package client

import (
	"strings"
	"testing"
)

// TestRecoveryRefusalWithoutScope checks that the refusal of a server started for recovery, which names no
// scope, does not say that a restart ends it before the recovery entry is recorded: that restart is an
// operator error until the entry has committed (execution-recovery.md §7.2).
func TestRecoveryRefusalWithoutScope(t *testing.T) {
	p := &Problem{Status: 409, Doc: map[string]any{"type": problemPrefix + "recovery-mode-active", "title": "Recovery mode active", "instance": "req_BWSYNTH"}}
	got := p.Error()
	for _, want := range []string{
		"the server was started for recovery",
		"A restriction of the process, not of one scope: record the recovery entry first, then restart the server without the flag to end it.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "it ends when the server is restarted without the flag") {
		t.Errorf("refusal %q says a restart ends the restriction with no mention of the entry", got)
	}
}

// TestRuleOfForbiddenAndConflict checks that a 403 forbidden and a 409 conflict name the rule the
// server refused by, which tells the import base text's two refusals apart, and add nothing when
// the answer carries none (client-contracts.md §11.3).
func TestRuleOfForbiddenAndConflict(t *testing.T) {
	for _, tc := range []struct {
		code string
		doc  map[string]any
		want string
	}{
		{"forbidden", map[string]any{"rule": "not-the-importer"}, "\nRefused: rule not-the-importer."},
		{"conflict", map[string]any{"rule": "import-not-reviewed"}, "\nRefused: rule import-not-reviewed."},
		{"conflict", map[string]any{"rule": "a\nb"}, "\nRefused: rule a\\x0ab."},
		{"forbidden", map[string]any{}, ""},
		{"conflict", map[string]any{}, ""},
	} {
		tc.doc["type"], tc.doc["title"], tc.doc["instance"] = problemPrefix+tc.code, "BWSYNTH", "req_BWSYNTH"
		p := &Problem{Status: 403, Doc: tc.doc}
		_, after, _ := strings.Cut(p.Error(), "(instance req_BWSYNTH)")
		if after != tc.want {
			t.Errorf("%s %v: after the instance %q, want %q", tc.code, tc.doc, after, tc.want)
		}
	}
}

// TestValidationRefusal checks that a validation refusal names the rule and the place in the body
// the server gives, and nothing for a member it does not give.
func TestValidationRefusal(t *testing.T) {
	for _, tc := range []struct {
		doc  map[string]any
		want string
	}{
		{map[string]any{"path": "fragments[1]", "revision": "frv_BWSYNTH"}, "\nRefused: path fragments[1], revision frv_BWSYNTH."},
		{map[string]any{"rule": "unknown-reference", "reference": "s-a"}, "\nRefused: rule unknown-reference, reference s-a."},
		{map[string]any{"rule": "BWSYNTH", "position": float64(2)}, "\nRefused: rule BWSYNTH, position 2."},
		{map[string]any{"rule": "BWSYNTH", "paths": []any{"machine.a", "machine.b"}}, "\nRefused: rule BWSYNTH, paths [machine.a machine.b]."},
		{map[string]any{}, ""},
	} {
		tc.doc["type"], tc.doc["title"], tc.doc["instance"] = problemPrefix+"validation-failed", "Compilation refused the input", "req_BWSYNTH"
		p := &Problem{Status: 422, Doc: tc.doc, Route: "PUT /api/v1/drafts/drf_BWSYNTH/profiles/workers", Where: "lab"}
		_, after, _ := strings.Cut(p.Error(), "(instance req_BWSYNTH)")
		if after != tc.want {
			t.Errorf("%v: after the instance %q, want %q", tc.doc, after, tc.want)
		}
	}
}

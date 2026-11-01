package client

import (
	"strings"
	"testing"
)

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

package client

import (
	"encoding/json"
	"strings"
	"testing"
)

// Each refusal about a shared fragment or profile names its rule, what it refers to and the next step,
// whether the route answers it directly or a failed publication carries it.
func TestSharedRefusalWords(t *testing.T) {
	for _, c := range []struct {
		name  string
		doc   string
		lines []string
	}{
		{"a reference in a shared document", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422,"instance":"req_1","rule":"library-reference"}`,
			[]string{"Refused: rule library-reference.", "Next: a shared document holds no secret reference, so a fragment that holds a secret stays in one cluster"}},
		{"a name the library lost", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422,"instance":"req_1","rule":"library-removed","kind":"fragment","name":"ntp"}`,
			[]string{"rule library-removed, kind fragment, name ntp", "Next: the shared fragment ntp has no head to adopt: bring it back with a shared edit"}},
		{"a name in two scopes", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422,"instance":"req_1","rule":"name-in-two-scopes","kind":"fragment","name":"ntp"}`,
			[]string{"rule name-in-two-scopes", "Next: this cluster holds the fragment ntp as its own and as a shared one: in the same draft remove one of them"}},
		{"two revisions of one name", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422,"instance":"req_1","rule":"library-pin-conflict","fragment":"ntp",` +
			`"sources":[{"profile":"base-set","revision":"frv_a"},{"adoption":"frv_x","revision":"frv_b"}]}`,
			[]string{"fragment ntp, sources profile base-set at frv_a and adoption frv_x at frv_b", "Next: two sources of this release pin different revisions of the fragment ntp"}},
		{"a detached profile's pin", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422,"instance":"req_1","rule":"detach-pin-not-in-view","fragment":"ntp"}`,
			[]string{"Next: a profile of this cluster pins the fragment ntp at a revision the cluster does not hold: adopt that fragment at the pinned revision (fragment adopt ntp --revision"}},
		{"an adoption that moved", `{"type":"urn:bronzeward:problem:stale-input","title":"BWSYNTH","status":409,"instance":"req_1","conflicts":[` +
			`{"machine":"mch_x","expected":"a","actual":"b"},{"head":"frg_ntp","scope":"adoption","expected":3,"actual":4}]}`,
			[]string{"Next: the shared name moved after the draft was read (frg_ntp was at adoption 3 and is at 4): read the draft again with draft show, stage the adoption again in a new draft and publish that."}},
	} {
		p := &Problem{Doc: map[string]any{}, Status: 422, Route: "PUT /api/v1/x", Where: "lab"}
		if err := json.Unmarshal([]byte(c.doc), &p.Doc); err != nil {
			t.Fatal(err)
		}
		got := p.Error()
		for _, want := range c.lines {
			if !strings.Contains(got, want) {
				t.Errorf("%s: no %q in\n%s", c.name, want, got)
			}
		}
		if next := SharedNext(p.Code(), p.Doc); !strings.Contains(got, "Next: "+next) {
			t.Errorf("%s: SharedNext %q is not the line of the problem", c.name, next)
		}
	}
	for _, other := range []map[string]any{{"type": "urn:bronzeward:problem:validation-failed", "rule": "other"},
		{"type": "urn:bronzeward:problem:stale-input", "conflicts": []any{map[string]any{"head": "h", "expected": 1, "actual": 2}}}} {
		if next := SharedNext(strings.TrimPrefix(other["type"].(string), problemPrefix), other); next != "" {
			t.Errorf("%v: a next step %q for a refusal that is no shared one", other, next)
		}
	}
}

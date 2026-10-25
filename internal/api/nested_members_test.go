package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// §9.1: a nested member is held to the top level's rule. For each mutating route whose body nests
// an object, a member of that object that differs in case or repeats (encoding/json would accept
// both) is refused 400 invalid-request, and the same body without the fault is not: it passes the
// body checks and is answered by the route. A map member's object takes any key but not one key
// twice.
func TestNestedMemberRefusals(t *testing.T) {
	ie := setupIngestEnv(t)
	var entered atomic.Int32
	r := recoveryStart(t, ie.env, &entered)
	author, recovery := ie.human("h-author"), ie.human("h-recovery")
	obs := func() string { return id.New(id.Observation) }
	doc := `"document":"machine:\n  nodeLabels:\n    tier: a\n"`
	frag := prefix + "/drafts/" + ie.draft + "/fragments/base"
	ingest := func(decl string) string {
		return `{"kind":"import","machine":"` + ie.machine + `","draft":"` + ie.draft +
			`","source":"document","staging":"transient","marks":[],` + doc + `,"declarations":` + decl + `}`
	}
	entry := func(backup string) string {
		return `{"restored":[` + backup + `],"stoppedAt":"2026-09-25T03:00:00Z","reason":"database restored"}`
	}
	backup := func(members string) string {
		return `{"family":"database","backup":"pg-1",` + members + `}`
	}
	scope := func(members string) string {
		return `{"scopes":[{"machine":"` + mch + `",` + members + `}],"facts":{"why":"restored"}}`
	}
	start, end := obs(), obs()
	reference := func(members string) string {
		return `{"layer":"site",` + doc + `,"marks":[],"declarations":{"references":{"s-a":{` + members + `}}}}`
	}
	type form struct{ good, wrongCase, repeated string }
	for _, rt := range []struct {
		name, method, path, token, ifMatch string
		a                                  *API
		forms                              []form
	}{
		{"fragment", "PUT", frag, author, ie.etag, ie.api, []form{
			{reference(`"kind":"string","version":1`), reference(`"Kind":"string","version":1`),
				reference(`"kind":"string","version":1,"version":2`)},
			{`{"layer":"site",` + doc + `,"marks":[],"declarations":{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml"}]}}`,
				`{"layer":"site",` + doc + `,"marks":[],"declarations":{"embedded":[{"path":"machine.nodeLabels.tier","Format":"yaml"}]}}`,
				`{"layer":"site",` + doc + `,"marks":[],"declarations":{"embedded":[{"path":"machine.nodeLabels.tier","path":"machine.nodeLabels.tier","format":"yaml"}]}}`},
			{`{"layer":"site",` + doc + `,"marks":[],"declarations":{"embedded":[]}}`,
				`{"layer":"site",` + doc + `,"marks":[],"declarations":{"Embedded":[]}}`,
				`{"layer":"site",` + doc + `,"marks":[],"declarations":{"embedded":[],"embedded":[]}}`},
		}},
		{"ingestion", "POST", prefix + "/ingestions", author, ie.etag, ie.api, []form{
			{ingest(`{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml"}]}`),
				ingest(`{"embedded":[{"PATH":"machine.nodeLabels.tier","format":"yaml"}]}`),
				ingest(`{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml","format":"yaml"}]}`)},
		}},
		{"recovery entry", "POST", path(entryPattern), recovery, "", r, []form{
			{entry(backup(`"takenAt":"2026-09-25T02:00:00Z"`)), entry(backup(`"takenat":"2026-09-25T02:00:00Z"`)),
				entry(backup(`"takenAt":"2026-09-25T01:00:00Z","takenAt":"2026-09-25T02:00:00Z"`))},
		}},
		{"recovery accounting", "POST", prefix + "/recovery/accountings", recovery, "", ie.api, []form{
			{scope(`"start":"` + start + `","end":"` + end + `"`), scope(`"Start":"` + start + `","end":"` + end + `"`),
				scope(`"start":"` + start + `","end":"` + end + `","end":"` + end + `"`)},
			{scope(`"start":"` + start + `","end":"` + end + `"`), "",
				`{"scopes":[{"machine":"` + mch + `","start":"` + start + `","end":"` + end + `"}],"facts":{"why":"a","why":"b"}}`},
		}},
		{"attempt accounting", "POST", path("/operations/{id}/attempts/{attempt}/accountings"), recovery, "", ie.api, []form{
			{`{"start":"` + start + `","end":"` + end + `","facts":{"why":"a"}}`, "",
				`{"start":"` + start + `","end":"` + end + `","facts":{"why":"a","why":"b"}}`},
		}},
		{"recovery exit", "POST", path(exitPattern), recovery, "", ie.api, []form{
			{`{"facts":{"why":"a"}}`, "", `{"facts":{"why":"a","why":"b"}}`},
		}},
		{"scope mark", "POST", path("/recovery/scopes/{machine}/marks"), recovery, "", ie.api, []form{
			{`{"facts":{"why":"a"}}`, "", `{"facts":{"why":"a","why":"b"}}`},
		}},
		{"scope release", "POST", path("/recovery/scopes/{machine}/releases"), recovery, "", ie.api, []form{
			{`{"facts":{"why":"a"}}`, "", `{"facts":{"why":"a","why":"b"}}`},
		}},
		{"assignment", "PUT", prefix + "/drafts/" + ie.draft + "/assignments/" + ie.machine, author, ie.etag, ie.api, []form{
			{`{"profiles":[],"fragments":{"site":["base"]}}`, "", `{"profiles":[],"fragments":{"site":["base"],"site":["other"]}}`},
		}},
	} {
		for i, f := range rt.forms {
			for fault, body := range map[string]string{"wrong case": f.wrongCase, "repeated": f.repeated} {
				if body == "" {
					continue
				}
				t.Run(fmt.Sprintf("%s %d %s", rt.name, i, fault), func(t *testing.T) {
					k := "k-nested-" + strings.ReplaceAll(rt.name, " ", "-") + fmt.Sprintf("-%d-", i) + strings.ReplaceAll(fault, " ", "-")
					wantProblem(t, ie.do(rt.a, call{method: rt.method, path: rt.path, token: rt.token, key: k, ifMatch: rt.ifMatch, body: body}),
						http.StatusBadRequest, "invalid-request")
				})
			}
		}
	}
	// The controls run last: one that succeeds changes the state a later refusal would meet.
	for _, rt := range []struct {
		name, method, path, token, ifMatch string
		a                                  *API
		body                               string
	}{
		{"fragment", "PUT", frag, author, ie.etag, ie.api, reference(`"kind":"string","version":1`)},
		{"ingestion", "POST", prefix + "/ingestions", author, ie.etag, ie.api, ingest(`{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml"}]}`)},
		{"recovery entry", "POST", path(entryPattern), recovery, "", r, entry(backup(`"takenAt":"2026-09-25T02:00:00Z"`))},
		{"recovery accounting", "POST", prefix + "/recovery/accountings", recovery, "", ie.api, scope(`"start":"` + start + `","end":"` + end + `"`)},
		{"attempt accounting", "POST", path("/operations/{id}/attempts/{attempt}/accountings"), recovery, "", ie.api,
			`{"start":"` + start + `","end":"` + end + `","facts":{"why":"a"}}`},
		{"recovery exit", "POST", path(exitPattern), recovery, "", ie.api, `{"facts":{"why":"a"}}`},
		{"scope mark", "POST", path("/recovery/scopes/{machine}/marks"), recovery, "", ie.api, `{"facts":{"why":"a"}}`},
		{"scope release", "POST", path("/recovery/scopes/{machine}/releases"), recovery, "", ie.api, `{"facts":{"why":"a"}}`},
		{"assignment", "PUT", prefix + "/drafts/" + ie.draft + "/assignments/" + ie.machine, author, ie.etag, ie.api,
			`{"profiles":[],"fragments":{"site":["base"]}}`},
	} {
		t.Run(rt.name+" control", func(t *testing.T) {
			k := "k-nested-control-" + strings.ReplaceAll(rt.name, " ", "-")
			rec := ie.do(rt.a, call{method: rt.method, path: rt.path, token: rt.token, key: k, ifMatch: rt.ifMatch, body: rt.body})
			if rec.Code == http.StatusBadRequest || rec.Code == http.StatusForbidden {
				t.Fatalf("the well-formed body: %d %s", rec.Code, rec.Body)
			}
			t.Logf("%s control: %d", rt.name, rec.Code)
		})
	}
}

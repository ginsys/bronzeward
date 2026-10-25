package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// §9.1: a nested member is held to the top level's rule. For each mutating route whose body nests
// an object, a member of that object that differs in case or repeats (encoding/json would accept
// both) is refused 400 invalid-request, and the same body without the fault is not: it passes the
// body checks and is answered by the route. A map member's object takes any key but not one key
// twice. Neither refusal names the client's member or key (§9.4).
func TestNestedMemberRefusals(t *testing.T) {
	ie := setupIngestEnv(t)
	var entered atomic.Int32
	r := recoveryStart(t, ie.env, &entered)
	author, recovery := ie.human("h-author"), ie.human("h-recovery")
	doc := `"document":"machine:\n  nodeLabels:\n    tier: a\n"`
	frag := prefix + "/drafts/" + ie.draft + "/fragments/base"
	fragment := func(decl string) string {
		return `{"layer":"site",` + doc + `,"marks":[],"declarations":` + decl + `}`
	}
	ingest := func(decl string) string {
		return `{"kind":"import","machine":"` + ie.machine + `","draft":"` + ie.draft +
			`","source":"document","staging":"transient","marks":[],` + doc + `,"declarations":` + decl + `}`
	}
	entry := func(members string) string {
		return `{"restored":[{"family":"database","backup":"pg-1",` + members + `}],` +
			`"stoppedAt":"2026-09-25T03:00:00Z","reason":"database restored"}`
	}
	start, end := id.New(id.Observation), id.New(id.Observation)
	scope := func(members, facts string) string {
		return `{"scopes":[{"machine":"` + mch + `",` + members + `}],"facts":{` + facts + `}}`
	}
	settle := `"start":"` + start + `","end":"` + end + `"`
	// A form is one nested object: its well-formed body, the body with one member in another case
	// (none for a map, whose keys are free) and with one member repeated, and the client's text
	// that no refusal may name.
	type form struct{ good, wrongCase, repeated, hidden string }
	facts := func(members string) form {
		return form{`{` + members + `"facts":{"why":"a"}}`, "", `{` + members + `"facts":{"why":"a","why":"b"}}`, "why"}
	}
	for _, rt := range []struct {
		name, method, path, token, ifMatch string
		a                                  *API
		forms                              []form
	}{
		{"fragment", "PUT", frag, author, ie.etag, ie.api, []form{
			{fragment(`{"references":{"s-a":{"kind":"string","version":1}}}`),
				fragment(`{"references":{"s-a":{"Kind":"string","version":1}}}`),
				fragment(`{"references":{"s-a":{"kind":"string","version":1,"version":2}}}`), "Kind"},
			{fragment(`{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml"}]}`),
				fragment(`{"embedded":[{"path":"machine.nodeLabels.tier","Format":"yaml"}]}`),
				fragment(`{"embedded":[{"path":"machine.nodeLabels.tier","path":"machine.nodeLabels.tier","format":"yaml"}]}`), "Format"},
			{fragment(`{"embedded":[]}`), fragment(`{"Embedded":[]}`), fragment(`{"embedded":[],"embedded":[]}`), "Embedded"},
			{fragment(`{"references":{"s-a":{"kind":"string","version":1}}}`), "",
				fragment(`{"references":{"s-a":{"kind":"string","version":1},"s-a":{"kind":"string","version":2}}}`), "s-a"},
		}},
		{"ingestion", "POST", prefix + "/ingestions", author, ie.etag, ie.api, []form{
			{ingest(`{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml"}]}`),
				ingest(`{"embedded":[{"PATH":"machine.nodeLabels.tier","format":"yaml"}]}`),
				ingest(`{"embedded":[{"path":"machine.nodeLabels.tier","format":"yaml","format":"yaml"}]}`), "PATH"},
		}},
		{"recovery entry", "POST", path(entryPattern), recovery, "", r, []form{
			{entry(`"takenAt":"2026-09-25T02:00:00Z"`), entry(`"takenat":"2026-09-25T02:00:00Z"`),
				entry(`"takenAt":"2026-09-25T01:00:00Z","takenAt":"2026-09-25T02:00:00Z"`), "takenat"},
		}},
		{"recovery accounting", "POST", prefix + "/recovery/accountings", recovery, "", ie.api, []form{
			{scope(settle, `"why":"restored"`), scope(`"Start":"`+start+`","end":"`+end+`"`, `"why":"restored"`),
				scope(settle+`,"end":"`+end+`"`, `"why":"restored"`), "Start"},
			{scope(settle, `"why":"a"`), "", scope(settle, `"why":"a","why":"b"`), "why"},
		}},
		{"attempt accounting", "POST", path("/operations/{id}/attempts/{attempt}/accountings"), recovery, "", ie.api,
			[]form{facts(settle + ",")}},
		{"recovery exit", "POST", path(exitPattern), recovery, "", ie.api, []form{facts("")}},
		{"scope mark", "POST", path("/recovery/scopes/{machine}/marks"), recovery, "", ie.api, []form{facts("")}},
		{"scope release", "POST", path("/recovery/scopes/{machine}/releases"), recovery, "", ie.api, []form{facts("")}},
		{"assignment", "PUT", prefix + "/drafts/" + ie.draft + "/assignments/" + ie.machine, author, ie.etag, ie.api, []form{
			{`{"profiles":[],"fragments":{"site":["base"]}}`, "", `{"profiles":[],"fragments":{"site":["base"],"site":["other"]}}`, "site"},
		}},
	} {
		for i, f := range rt.forms {
			send := func(what, body string) *httptest.ResponseRecorder {
				k := fmt.Sprintf("k-nested-%s-%d-%s", strings.ReplaceAll(rt.name, " ", "-"), i, what)
				return ie.do(rt.a, call{method: rt.method, path: rt.path, token: rt.token, key: k, ifMatch: rt.ifMatch, body: body})
			}
			for _, fault := range []struct{ what, body string }{{"wrong-case", f.wrongCase}, {"repeated", f.repeated}} {
				if fault.body == "" {
					continue
				}
				t.Run(fmt.Sprintf("%s %d %s", rt.name, i, fault.what), func(t *testing.T) {
					p := wantProblem(t, send(fault.what, fault.body), http.StatusBadRequest, "invalid-request")
					if detail, _ := p["detail"].(string); strings.Contains(detail, f.hidden) {
						t.Fatalf("the refusal names the client's %q: %s", f.hidden, detail)
					}
				})
			}
			// The control: the form's well-formed body passes the body checks; what the route then
			// answers depends on state this test does not set up.
			t.Run(fmt.Sprintf("%s %d control", rt.name, i), func(t *testing.T) {
				rec := send("control", f.good)
				if rec.Code == http.StatusBadRequest || rec.Code == http.StatusForbidden {
					t.Fatalf("the well-formed body: %d %s", rec.Code, rec.Body)
				}
				t.Logf("%s %d control: %d", rt.name, i, rec.Code)
			})
		}
	}
}

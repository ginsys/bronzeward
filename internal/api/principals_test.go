package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/id"
)

// principalMembers is §9.2's entry of GET /principals, every member always present.
var principalMembers = []string{"id", "issuer", "kind", "service", "subject"}

// principalsPath is GET /principals with ids as its id parameters, in order.
func principalsPath(ids ...string) string {
	v := url.Values{"id": ids}
	return prefix + "/principals?" + v.Encode()
}

// readPrincipals reads GET /principals for ids as tok and fails unless the answer is a 200 holding
// only items, each with exactly §9.2's members, the epoch the read held and no ETag.
func readPrincipals(t *testing.T, e *env, a *API, tok string, ids ...string) []principalBody {
	t.Helper()
	rec := e.do(a, call{method: "GET", path: principalsPath(ids...), token: tok})
	if rec.Code != http.StatusOK {
		t.Fatalf("principals: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Bronzeward-Epoch") != epoch(t, e.db) || rec.Header().Get("ETag") != "" {
		t.Fatalf("headers %v", rec.Header())
	}
	var raw map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(raw)); !slices.Equal(got, []string{"items"}) {
		t.Fatalf("members %v; want only items: %s", got, rec.Body)
	}
	for _, it := range raw["items"] {
		if got := slices.Sorted(maps.Keys(it)); !slices.Equal(got, principalMembers) {
			t.Fatalf("item members %v; want %v", got, principalMembers)
		}
	}
	return decode[listPage[principalBody]](t, rec, http.StatusOK).Items
}

func str(s string) *string { return &s }

// §9.2, choice §17.40 (#335): a human, a service identity and an identifier no row holds are
// answered in the order given, a repeated identifier once, to every role and the automation
// token, with nothing of the row beyond its kind and names; nothing is recorded.
func TestPrincipalsRead(t *testing.T) {
	e := newEnv(t, options{})
	var hAll string // the robot's responsible human (helpers_test.go)
	if err := e.db.QueryRow(`SELECT id FROM principal WHERE kind = 'human' AND sub = 'h-all'`).Scan(&hAll); err != nil {
		t.Fatal(err)
	}
	nobody := id.New(id.Principal)
	want := []principalBody{
		{ID: hAll, Kind: "human", Issuer: str(e.iss.URL), Subject: str("h-all")},
		{ID: e.robotID, Kind: "service", Service: str("ci")},
		{ID: nobody, Kind: "unknown"},
	}
	acts, principals := count(t, e.db, "SELECT count(*) FROM act"), count(t, e.db, "SELECT count(*) FROM principal")
	for _, tok := range []string{e.human("h-viewer"), e.human("h-author"), e.human("h-publisher"), e.human("h-approver"),
		e.human("h-recovery"), e.human("h-all"), e.robot} {
		got := readPrincipals(t, e, e.api, tok, hAll, e.robotID, nobody, hAll, e.robotID)
		if !principalsEqual(got, want) {
			t.Fatalf("answered %s; want %s", principalsText(got), principalsText(want))
		}
	}
	// The other order is answered in that order.
	if got := readPrincipals(t, e, e.api, e.human("h-viewer"), nobody, e.robotID, hAll); !principalsEqual(got, []principalBody{want[2], want[1], want[0]}) {
		t.Fatalf("reversed: %s", principalsText(got))
	}
	// The reads recorded nothing, not even a principal row for a human never seen before.
	if n := count(t, e.db, "SELECT count(*) FROM act"); n != acts {
		t.Fatalf("acts %d → %d from the role reads", acts, n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal"); n != principals {
		t.Fatalf("principals %d → %d from the role reads", principals, n)
	}
	// A revoked identity is still answered by its kind and name; its revocation is not served.
	revocationOf(t, revoke(e, e.human("h-recovery"), "k-revoke-robot-0123456789", `{"identity":"`+e.robotID+`","reason":"left"}`))
	// The revocation recorded its act and h-recovery's first row; the reads after it record nothing.
	acts, principals = count(t, e.db, "SELECT count(*) FROM act"), count(t, e.db, "SELECT count(*) FROM principal")
	if got := readPrincipals(t, e, e.api, e.human("h-viewer"), e.robotID); !principalsEqual(got, want[1:2]) {
		t.Fatalf("revoked: %s", principalsText(got))
	}
	// 100 identifiers, the limit, are taken.
	many := make([]string, maxPrincipalIDs)
	for i := range many {
		many[i] = id.New(id.Principal)
	}
	if got := readPrincipals(t, e, e.api, e.human("h-viewer"), many...); len(got) != maxPrincipalIDs || got[0].ID != many[0] || got[99].ID != many[99] {
		t.Fatalf("100 identifiers: %d answered", len(got))
	}
	if n := count(t, e.db, "SELECT count(*) FROM act"); n != acts {
		t.Fatalf("acts %d → %d from principals reads", acts, n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal"); n != principals {
		t.Fatalf("principal rows %d → %d from principals reads", principals, n)
	}
}

// §9.2: an identifier whose row a restore to an older backup removed is answered unknown, not
// refused (§12.3); the restore is the row's removal and a new epoch, as the restore leaves them.
func TestPrincipalsAfterRestore(t *testing.T) {
	e := newEnv(t, options{})
	viewer := e.human("h-viewer")
	created := mustRecordHuman(t, e, "h-author")
	if got := readPrincipals(t, e, e.api, viewer, created); len(got) != 1 || got[0].Kind != "human" {
		t.Fatalf("before the restore: %s", principalsText(got))
	}
	mustExec(t, e.db, "DELETE FROM principal WHERE id = $1", created)
	newEpoch(t, e.db)
	viewer = e.human("h-viewer")
	if got := readPrincipals(t, e, e.api, viewer, created, e.robotID); !principalsEqual(got, []principalBody{
		{ID: created, Kind: "unknown"}, {ID: e.robotID, Kind: "service", Service: str("ci")},
	}) {
		t.Fatalf("after the restore: %s", principalsText(got))
	}
}

// §9.2: the read takes only id, 1 to 100 times, each a principal identifier; anything else is
// 400 invalid-request. A credential with no role is refused as on every any-role read.
func TestPrincipalsRefusals(t *testing.T) {
	e := newEnv(t, options{})
	viewer := e.human("h-viewer")
	ok := id.New(id.Principal)
	tooMany := make([]string, maxPrincipalIDs+1)
	for i := range tooMany {
		tooMany[i] = id.New(id.Principal)
	}
	for name, query := range map[string]string{
		"no query":          "",
		"no id":             "?",
		"bare id":           "?id",
		"empty id":          "?id=",
		"empty among valid": "?id=" + ok + "&id=",
		"malformed":         "?id=idn_short",
		"upper case":        "?id=" + strings.ToUpper(ok),
		"another prefix":    "?id=" + id.New(id.Request),
		"101 ids":           strings.TrimPrefix(principalsPath(tooMany...), prefix+"/principals"),
		"limit":             "?id=" + ok + "&limit=1",
		"cursor":            "?id=" + ok + "&cursor=x",
		"other parameter":   "?id=" + ok + "&x=1",
		"unparseable":       "?id=%zz",
	} {
		t.Run(name, func(t *testing.T) {
			wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/principals" + query, token: viewer}),
				http.StatusBadRequest, "invalid-request")
		})
	}
	now := time.Now()
	noRole, err := e.iss.Sign(map[string]any{"iss": e.iss.URL, "aud": "bronzeward", "sub": "h-nobody", "jti": issuer.JTI(),
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: principalsPath(ok), token: noRole}), http.StatusForbidden, "forbidden")
	wantProblem(t, e.do(e.api, call{method: "GET", path: principalsPath(ok), token: "not-a-token"}), http.StatusUnauthorized, "unauthenticated")
}

// §12.2: a recovery start refuses the read, as every request but the entry, until its entry
// commits; afterwards it answers every role.
func TestPrincipalsRecoveryStart(t *testing.T) {
	e := newEnv(t, options{})
	var entered atomic.Int32
	r := recoveryStart(t, e, &entered)
	wantProblem(t, e.do(r, call{method: "GET", path: principalsPath(e.robotID), token: e.human("h-viewer")}),
		http.StatusConflict, "recovery-mode-active")
	decode[entryBody](t, e.enter(r, "1", entryBodyText), http.StatusCreated)
	for _, h := range []string{"h-viewer", "h-recovery"} {
		if got := readPrincipals(t, e, r, e.human(h), e.robotID); len(got) != 1 || got[0].Kind != "service" {
			t.Fatalf("%s after the entry: %s", h, principalsText(got))
		}
	}
}

func mustRecordHuman(t *testing.T, e *env, sub string) string {
	t.Helper()
	e.recordHuman(sub)
	var idn string
	if err := e.db.QueryRow(`SELECT id FROM principal WHERE kind = 'human' AND sub = $1`, sub).Scan(&idn); err != nil {
		t.Fatal(err)
	}
	return idn
}

func principalsEqual(a, b []principalBody) bool {
	return slices.EqualFunc(a, b, func(x, y principalBody) bool {
		eq := func(p, q *string) bool { return (p == nil) == (q == nil) && (p == nil || *p == *q) }
		return x.ID == y.ID && x.Kind == y.Kind && eq(x.Issuer, y.Issuer) && eq(x.Subject, y.Subject) && eq(x.Service, y.Service)
	})
}

func principalsText(ps []principalBody) string {
	b, _ := json.Marshal(ps)
	return string(b)
}

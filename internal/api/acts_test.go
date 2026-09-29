package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

type actPage struct {
	Items []actItem `json:"items"`
	Next  string    `json:"next"`
}

func acts(t *testing.T, e *env, tok, query string) actPage {
	t.Helper()
	rec := e.do(e.api, call{method: "GET", path: prefix + "/acts" + query, token: tok})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var p actPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListActsPages(t *testing.T) {
	e := newEnv(t, options{})
	var human string
	if err := e.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-all'").Scan(&human); err != nil {
		t.Fatal(err)
	}
	for range 4 { // plus the token tool's issue act: 5 acts
		mustExec(t, e.db, `INSERT INTO act (id, principal, principal_kind, via, action, subjects, epoch, at)
			SELECT $1, $2, 'human', 'tool', 'test', '{}', epoch, now() FROM installation_state`, id.New(id.Act), human)
	}
	var all []actItem
	q := "?limit=2"
	for pages := 0; ; pages++ {
		p := acts(t, e, e.human("h-viewer"), q)
		all = append(all, p.Items...)
		if p.Next == "" {
			if pages != 2 || len(all) != 5 {
				t.Fatalf("%d pages, %d acts; want 3 pages of 2, 2 and 1", pages+1, len(all))
			}
			break
		}
		// §2: an internal sequence key never appears in a cursor; the cursor names the last act.
		if b, err := base64.RawURLEncoding.DecodeString(p.Next); err != nil || string(b) != epoch(t, e.db)+"."+p.Items[len(p.Items)-1].ID {
			t.Fatalf("cursor %q decodes to %q; want the epoch and the last act's id", p.Next, b)
		}
		q = "?limit=2&cursor=" + url.QueryEscape(p.Next)
	}
	if all[0].Action != "token.issue" || all[0].Via != "tool" || all[0].Role != nil || len(all[0].Subjects) != 2 {
		t.Fatalf("first act %+v", all[0])
	}
	if p := acts(t, e, e.robot, ""); len(p.Items) != 5 || p.Next != "" { // any role reads acts
		t.Fatalf("robot: %d acts, next %q", len(p.Items), p.Next)
	}
}

// A reader paging GET /acts never passes an act that commits later: acts become visible in seq
// order, because each act-writing transaction holds the act-order lock from its act to its end.
func TestActsVisibleInOrder(t *testing.T) { actsVisibleInOrder(t, false) }

// Control: without the lock a later act commits first, a cursor passes it, and the earlier act
// that commits afterwards is never listed.
func TestActsVisibleInOrderNoActOrderControl(t *testing.T) { actsVisibleInOrder(t, true) }

func actsVisibleInOrder(t *testing.T, control bool) {
	hook, held, release := holdFirst()
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, afterEffect: hook, noActOrder: control})
	t.Cleanup(release)
	author, viewer := e.human("h-author"), e.human("h-viewer")
	e.recordHuman("h-author")
	a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- e.do(e.api, post(author, "k-first-0123456789", `{}`)) }()
	<-held // the first request's act is written, uncommitted
	go func() { b <- e.do(e.api, post(e.robot, "k-second-012345678", `{}`)) }()
	if control {
		if rb := <-b; rb.Code != http.StatusCreated {
			t.Fatalf("second: %d %s", rb.Code, rb.Body)
		}
	} else {
		dbtest.WaitForLockWait(t, e.db) // the second waits for the first to end
	}
	seen := acts(t, e, viewer, "?limit=500").Items
	release()
	if ra := <-a; ra.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", ra.Code, ra.Body)
	}
	if !control {
		if rb := <-b; rb.Code != http.StatusCreated {
			t.Fatalf("second: %d %s", rb.Code, rb.Body)
		}
	}
	rest := acts(t, e, viewer, "?limit=500&cursor="+url.QueryEscape(makeCursor(epoch(t, e.db), seen[len(seen)-1].ID))).Items
	got, want := len(seen)+len(rest), count(t, e.db, "SELECT count(*) FROM act")
	switch {
	case control && got == want:
		t.Fatal("control: paging listed every act; the interleaving did not happen")
	case !control && got != want:
		t.Fatalf("paging listed %d of %d acts: an act committed behind the cursor", got, want)
	}
}

func TestListActsRefusals(t *testing.T) {
	e := newEnv(t, options{})
	tok := e.human("h-viewer")
	for _, q := range []string{"?limit=0", "?limit=501", "?limit=x", "?limit=1&limit=2", "?order=desc", "?limit=%zz"} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts" + q, token: tok}), http.StatusBadRequest, "invalid-request")
	}
	var first string // the token tool's issue act
	if err := e.db.QueryRow("SELECT id FROM act ORDER BY seq LIMIT 1").Scan(&first); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"garbage", makeCursor(id.New(id.Epoch), first), makeCursor(epoch(t, e.db), "act_1"),
		makeCursor(epoch(t, e.db), id.New(id.Act))} { // the last names no act
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts?cursor=" + url.QueryEscape(c), token: tok}),
			http.StatusBadRequest, "cursor-invalid")
	}
	// A cursor from the epoch before an entry is refused after it (§9.1).
	old := makeCursor(epoch(t, e.db), first)
	newEpoch(t, e.db)
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts?cursor=" + url.QueryEscape(old), token: tok}),
		http.StatusBadRequest, "cursor-invalid")
}

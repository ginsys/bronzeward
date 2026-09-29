package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

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
		q = "?limit=2&cursor=" + url.QueryEscape(p.Next)
	}
	if all[0].Action != "token.issue" || all[0].Via != "tool" || all[0].Role != nil || len(all[0].Subjects) != 2 {
		t.Fatalf("first act %+v", all[0])
	}
	if p := acts(t, e, e.robot, ""); len(p.Items) != 5 || p.Next != "" { // any role reads acts
		t.Fatalf("robot: %d acts, next %q", len(p.Items), p.Next)
	}
}

func TestListActsRefusals(t *testing.T) {
	e := newEnv(t, options{})
	tok := e.human("h-viewer")
	for _, q := range []string{"?limit=0", "?limit=501", "?limit=x", "?limit=1&limit=2", "?order=desc", "?limit=%zz"} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts" + q, token: tok}), http.StatusBadRequest, "invalid-request")
	}
	for _, c := range []string{"garbage", makeCursor(id.New(id.Epoch), 1), makeCursor(epoch(t, e.db), 0)} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts?cursor=" + url.QueryEscape(c), token: tok}),
			http.StatusBadRequest, "cursor-invalid")
	}
	// A cursor from the epoch before an entry is refused after it (§9.1).
	old := makeCursor(epoch(t, e.db), 1)
	newEpoch(t, e.db)
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts?cursor=" + url.QueryEscape(old), token: tok}),
		http.StatusBadRequest, "cursor-invalid")
}

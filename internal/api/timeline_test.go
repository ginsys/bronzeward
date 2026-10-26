package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// §9.2 (API revision 3): a machine's timeline reads oldest or newest first, each order paging with
// its own cursors, and every answer carries tail, the forward cursor from the machine's newest
// entry, absent while it has none. A forward read from a tail answers exactly the entries written
// after it, across a page boundary.
func TestTimelineOrderAndTail(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	author, approver, viewer := e.human("h-author"), e.human("h-approver"), e.human("h-viewer")
	m := e.endpointMachine(author)
	get := func(query string) *httptest.ResponseRecorder {
		return e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m + "/timeline" + query, token: viewer})
	}
	read := func(query string) timelinePage { return decode[timelinePage](t, get(query), http.StatusOK) }
	revisions := func(p timelinePage) []int64 {
		var rs []int64
		for _, x := range p.Items {
			rs = append(rs, x.Revision)
		}
		return rs
	}
	forward := func(rev int64) string { return makeCursor(epoch(t, e.db), strconv.FormatInt(rev, 10)) }

	for _, q := range []string{"", "?order=oldest", "?order=newest"} {
		if p := read(q); len(p.Items) != 0 || p.Next != "" || p.Tail != "" {
			t.Fatalf("no entries, %q: %+v", q, p)
		}
	}

	// Each freeze or unfreeze is one entry at the machine's next revision (T7).
	written := 0
	write := func(n int) {
		t.Helper()
		for range n {
			written++
			act, tok := "freezes", author
			if written%2 == 0 {
				act, tok = "unfreezes", approver
			}
			decode[freezeBody](t, e.do(e.api, scopeAct(tok, fmt.Sprintf("k-timeline-%02d-0123456789", written), act, m,
				`{"reason":"timeline"}`)), http.StatusCreated)
		}
	}
	write(5)

	// Newest first, two at a time, back to the start; every page names the same tail.
	first := read("?order=newest&limit=2")
	if first.Tail != forward(5) {
		t.Fatalf("tail %q; want the cursor after revision 5", first.Tail)
	}
	var got []int64
	for p := first; ; p = read("?order=newest&limit=2&cursor=" + url.QueryEscape(p.Next)) {
		if p.Tail != first.Tail {
			t.Fatalf("tail moved to %q while nothing was written", p.Tail)
		}
		got = append(got, revisions(p)...)
		if p.Next == "" {
			break
		}
	}
	if !slices.Equal(got, []int64{5, 4, 3, 2, 1}) {
		t.Fatalf("newest first: %v", got)
	}
	// The default order is unchanged: oldest first, with the same tail.
	if p := read("?limit=500"); !slices.Equal(revisions(p), []int64{1, 2, 3, 4, 5}) || p.Next != "" || p.Tail != first.Tail {
		t.Fatalf("oldest first: %v, next %q, tail %q", revisions(p), p.Next, p.Tail)
	}

	// Polling from the tail answers nothing until an entry is written, then exactly the later ones.
	if p := read("?cursor=" + url.QueryEscape(first.Tail)); len(p.Items) != 0 || p.Next != "" || p.Tail != first.Tail {
		t.Fatalf("poll with nothing new: %+v", p)
	}
	write(3)
	p := read("?limit=2&cursor=" + url.QueryEscape(first.Tail))
	if !slices.Equal(revisions(p), []int64{6, 7}) || p.Next == "" || p.Tail != forward(8) {
		t.Fatalf("poll, first page: %v, next %q, tail %q", revisions(p), p.Next, p.Tail)
	}
	if p = read("?limit=2&cursor=" + url.QueryEscape(p.Next)); !slices.Equal(revisions(p), []int64{8}) || p.Next != "" ||
		p.Tail != forward(8) {
		t.Fatalf("poll, last page: %v, next %q, tail %q", revisions(p), p.Next, p.Tail)
	}

	// A cursor names its order; another epoch's cursor and a malformed one are refused (§9.1).
	for _, c := range []string{"?order=newest&cursor=" + first.Tail, "?cursor=" + first.Next, "?order=oldest&cursor=" + first.Next,
		"?order=newest&cursor=" + makeCursor(id.New(id.Epoch), "<3"), "?cursor=" + makeCursor(id.New(id.Epoch), "3"),
		"?order=newest&cursor=" + makeCursor(epoch(t, e.db), "<0"), "?order=newest&cursor=" + makeCursor(epoch(t, e.db), "<x")} {
		wantProblem(t, get(c), http.StatusBadRequest, "cursor-invalid")
	}
	for _, q := range []string{"?order=latest", "?order=NEWEST", "?order=", "?order=newest&order=newest"} {
		wantProblem(t, get(q), http.StatusBadRequest, "invalid-request")
	}
}

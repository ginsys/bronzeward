package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/id"
)

// completedOperation runs xe's plan through the executor loop to a completed operation, which it
// returns; the loop stops with the test.
func (xe *execEnv) completedOperation(t *testing.T) string {
	t.Helper()
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	t.Cleanup(func() { stopping(a, stop) })
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	var op string
	if err := xe.db.QueryRow(`SELECT id FROM operation WHERE plan = $1`, xe.pid).Scan(&op); err != nil {
		t.Fatal(err)
	}
	return op
}

func (xe *execEnv) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return xe.do(xe.env.api, call{method: "GET", path: prefix + path, token: xe.robot})
}

type eventItem struct {
	ID   string                     `json:"id"`
	Type string                     `json:"type"`
	At   time.Time                  `json:"at"`
	Data map[string]json.RawMessage `json:"data"`
}

type eventsPage struct {
	Items []eventItem `json:"items"`
	Next  string      `json:"next"`
}

// §8.3 and §9.2 (AP S4): a plan operation is listed by plan, state, kind and machine, in
// identifier order, and its resource names its plan and machine and shows each attempt with its
// response and accounting. No configuration digest is served (choice §17.38).
func TestOperationsRead(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	op := xe.completedOperation(t)

	list := decode[listPage[operationBody]](t, xe.get(t, "/operations?plan="+xe.pid), http.StatusOK)
	if len(list.Items) != 1 || list.Items[0].ID != op || list.Items[0].Kind != "apply-config" || list.Items[0].State != "completed" ||
		list.Items[0].Subject == nil || list.Items[0].Subject.Plan != xe.pid || list.Items[0].Subject.Machine != xe.machine {
		t.Fatalf("operations of the plan: %+v", list)
	}
	for q, want := range map[string]int{
		"?machine=" + xe.machine:             1,
		"?kind=apply-config&state=completed": 1,
		"?state=failed":                      0,
		"?kind=publish":                      count(t, xe.db, `SELECT count(*) FROM operation WHERE kind = 'publish'`),
		"?plan=" + id.New(id.Plan):           0,
		"?limit=0":                           -1, // refused (§9.1)
	} {
		rec := xe.get(t, "/operations"+q)
		if want < 0 {
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
			}
			continue
		}
		if got := decode[listPage[operationBody]](t, rec, http.StatusOK); len(got.Items) != want {
			t.Fatalf("%s: %d operations, want %d", q, len(got.Items), want)
		}
	}
	for _, q := range []string{"?state=nonesuch", "?kind=nonesuch", "?plan=not-a-plan", "?nonesuch=1"} {
		if rec := xe.get(t, "/operations"+q); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body)
		}
	}

	rec := xe.get(t, "/operations/"+op)
	var b struct {
		operationBody
		Attempts []struct {
			ID         string                  `json:"id"`
			Number     int                     `json:"number"`
			Response   *struct{ Class string } `json:"response"`
			Accounting *struct{ Basis string } `json:"accounting"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	if len(b.Attempts) != 1 || b.Attempts[0].Number != 1 || b.Attempts[0].Response == nil || b.Attempts[0].Response.Class == "" ||
		b.Attempts[0].Accounting == nil || b.Attempts[0].Accounting.Basis != "response" {
		t.Fatalf("attempts %+v", b.Attempts)
	}
	if strings.Contains(rec.Body.String(), "digest") {
		t.Fatalf("the resource carries a digest: %s", rec.Body)
	}
	if rec := xe.get(t, "/operations/"+id.New(id.Operation)); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown operation: %d", rec.Code)
	}
}

// §8.3: a plan operation's events are the machine entries that name it, from its commitment to
// its Applied change, in machine revision order, as `<epoch>:<revision>` identifiers. They are
// cursor-paginated, a cursor of another epoch is refused (§9.1), and no event carries a response's
// text or a digest.
func TestOperationEvents(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	op := xe.completedOperation(t)

	var kinds []string
	rows, err := xe.db.Query(`SELECT kind FROM machine_event WHERE machine = $1 AND entry->>'operation' = $2
		AND kind <> 'observation-started' ORDER BY revision`, xe.machine, op)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(kinds) < 5 || kinds[0] != "commitment" || !slices.Contains(kinds, "applied-change") {
		t.Fatalf("entries naming the operation: %v", kinds)
	}

	all := decode[eventsPage](t, xe.get(t, "/operations/"+op+"/events"), http.StatusOK)
	var types []string
	last := int64(0)
	for _, e := range all.Items {
		types = append(types, e.Type)
		var rev int64
		ep, n, ok := strings.Cut(e.ID, ":")
		if !ok || ep != epoch(t, xe.db) {
			t.Fatalf("event id %q", e.ID)
		}
		if err := json.Unmarshal([]byte(n), &rev); err != nil || rev <= last {
			t.Fatalf("event id %q after revision %d", e.ID, last)
		}
		last = rev
		for _, k := range []string{"text", "digest"} {
			if _, has := e.Data[k]; has {
				t.Fatalf("event %s carries %s", e.ID, k)
			}
		}
	}
	if !slices.Equal(types, kinds) {
		t.Fatalf("event types %v, entries %v", types, kinds)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND entry ? 'text'`, xe.machine); n == 0 {
		t.Fatal("no stored entry holds text: the redaction is not exercised")
	}

	// Two at a time.
	var got []string
	next := ""
	for range len(kinds) {
		path := "/operations/" + op + "/events?limit=2"
		if next != "" {
			path += "&cursor=" + next
		}
		p := decode[eventsPage](t, xe.get(t, path), http.StatusOK)
		for _, e := range p.Items {
			got = append(got, e.Type)
		}
		if next = p.Next; next == "" {
			break
		}
	}
	if !slices.Equal(got, kinds) {
		t.Fatalf("paged event types %v, entries %v", got, kinds)
	}
	foreign := makeCursor(id.New(id.Epoch), "1")
	if rec := xe.get(t, "/operations/"+op+"/events?cursor="+foreign); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "cursor-invalid") {
		t.Fatalf("a cursor of another epoch: %d %s", rec.Code, rec.Body)
	}
	if rec := xe.get(t, "/operations/"+id.New(id.Operation)+"/events"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown operation: %d", rec.Code)
	}
}

// §9.2 and execution and recovery §4.1: a machine's timeline is its entries in revision order,
// each with its kind and what it names, and the resource shows Desired, Applied and Observed as
// three values that agree once the operation completed.
func TestMachineTimelineAndObserved(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.completedOperation(t)

	p := decode[struct {
		Items []struct {
			Revision int64                      `json:"revision"`
			Kind     string                     `json:"kind"`
			Entry    map[string]json.RawMessage `json:"entry"`
		} `json:"items"`
		Next string `json:"next"`
	}](t, xe.get(t, "/machines/"+xe.machine+"/timeline?limit=500"), http.StatusOK)
	// The control: the stored entries hold both values, so the redaction is what removes them.
	for _, k := range []string{"text", "digest"} {
		if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND entry ? $2`, xe.machine, k); n == 0 {
			t.Fatalf("no stored entry holds %s: the redaction is not exercised", k)
		}
		for _, e := range p.Items {
			if _, has := e.Entry[k]; has {
				t.Fatalf("timeline entry %d (%s) carries %s", e.Revision, e.Kind, k)
			}
		}
	}
	n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1`, xe.machine)
	if len(p.Items) != n || p.Next != "" {
		t.Fatalf("%d entries of %d", len(p.Items), n)
	}
	for i := 1; i < len(p.Items); i++ {
		if p.Items[i-1].Revision >= p.Items[i].Revision {
			t.Fatalf("revisions %d then %d", p.Items[i-1].Revision, p.Items[i].Revision)
		}
	}
	kinds := map[string]bool{}
	for _, e := range p.Items {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"plan", "approval", "commitment", "attempt", "response", "applied-change"} {
		if !kinds[k] {
			t.Fatalf("no %s entry on the timeline", k)
		}
	}
	if rec := xe.get(t, "/machines/"+id.New(id.Machine)+"/timeline"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", rec.Code)
	}

	m := decode[struct {
		Desired  *string `json:"desired"`
		Applied  *struct{ Release string }
		Observed *struct {
			Release     *string `json:"release"`
			Observation string  `json:"observation"`
		} `json:"observed"`
	}](t, xe.get(t, "/machines/"+xe.machine), http.StatusOK)
	if m.Desired == nil || m.Applied == nil || m.Observed == nil || m.Observed.Release == nil || m.Observed.Observation == "" ||
		*m.Desired != xe.target.rel || m.Applied.Release != xe.target.rel || *m.Observed.Release != xe.target.rel {
		t.Fatalf("machine %+v", m)
	}
}

// Execution and recovery §4.1: of two observations the one of the higher basis decides, whichever
// was recorded last. B's read began after A's but was recorded before it, and shows another
// configuration: Observed is B's, matching no release.
func TestMachineObservedHighestBasis(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.completedOperation(t)

	tx, err := xe.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	next := func() int64 {
		var n int64
		if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
			xe.machine).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	startA, startB := next(), next()
	for _, s := range []int64{startA, startB} {
		mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, i.epoch, 'observation-started', jsonb_build_object('purpose', 'drift'::text, 'plan', NULL::text,
				'endpoint', s.endpoint, 'controller', s.controller), now()
			FROM installation_state i, LATERAL (SELECT endpoint, controller FROM observation_start WHERE machine = $1
				ORDER BY revision DESC LIMIT 1) s`, xe.machine, s)
		mustExec(t, tx, `INSERT INTO observation_start (machine, revision, purpose, endpoint, controller)
			SELECT $1, $2, 'drift', endpoint, controller FROM observation_start WHERE machine = $1
			ORDER BY revision DESC LIMIT 1`, xe.machine, s)
	}
	record := func(basis int64, digest string) string {
		rev, obs := next(), id.New(id.Observation)
		mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', 'drift'::text, 'basis', $4::bigint),
				now() FROM installation_state`, xe.machine, rev, obs, basis)
		mustExec(t, tx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
				smbios_uuid, talos_node_id, talos_cluster_id, assignment_evidence, running_version, configuration_digest,
				resource_version, unread, at)
			SELECT $1, machine, $3, $4, access_path, access_version, access_created, smbios_uuid, talos_node_id, talos_cluster_id,
				assignment_evidence, running_version, decode($5, 'hex'), resource_version, unread, now()
			FROM observation WHERE machine = $2 ORDER BY revision DESC LIMIT 1`, obs, xe.machine, basis, rev, digest)
		return obs
	}
	b := record(startB, strings.Repeat("ab", 32))
	record(startA, strings.Repeat("cd", 32))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	m := decode[struct {
		Observed *struct {
			Release     *string `json:"release"`
			Observation string  `json:"observation"`
		} `json:"observed"`
	}](t, xe.get(t, "/machines/"+xe.machine), http.StatusOK)
	if m.Observed == nil || m.Observed.Observation != b || m.Observed.Release != nil {
		t.Fatalf("observed %+v, want observation %s with no release", m.Observed, b)
	}
}

// AP §7.1 (S8 step 3): an event stream ends no later than its token's expiry and resumes with
// Last-Event-ID under a fresh token; once the identity is revoked the same token's resumption is
// refused. An id of another epoch is answered with a reset and the timeline from its start.
func TestEventStreamEndsAtTokenExpiry(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	r, err := ce.a.commitPlan(t.Context(), ce.pid) // committed, and no loop to move it on
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ce.a)
	t.Cleanup(srv.Close)

	short, err := auth.NewStore(ce.db, ce.cfg).Issue(t.Context(), "watch", []auth.Role{auth.Viewer}, 2*time.Second, "h-all", "h-all")
	if err != nil {
		t.Fatal(err)
	}
	open := func(token, lastID string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+prefix+"/operations/"+r.Operation+"/events", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "text/event-stream")
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	frames := func(resp *http.Response) (ids, events []string) {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			switch line := sc.Text(); {
			case strings.HasPrefix(line, "id: "):
				ids = append(ids, strings.TrimPrefix(line, "id: "))
			case strings.HasPrefix(line, "event: "):
				events = append(events, strings.TrimPrefix(line, "event: "))
			}
		}
		return ids, events
	}

	started := time.Now()
	resp := open(short.Token, "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	ids, events := frames(resp) // returns when the server ends the stream
	if d := time.Since(started); d < time.Second || d > 6*time.Second {
		t.Fatalf("the stream lasted %s on a token of 2s", d)
	}
	if len(ids) != 1 || events[0] != "commitment" || ids[0] != epoch(t, ce.db)+":"+strconv.FormatInt(r.Revision, 10) {
		t.Fatalf("frames %v %v", ids, events)
	}

	// Resumption with the id of the last event shows nothing new; with another epoch's id it resets.
	fresh, err := auth.NewStore(ce.db, ce.cfg).Issue(t.Context(), "watch2", []auth.Role{auth.Viewer}, time.Hour, "h-all", "h-all")
	if err != nil {
		t.Fatal(err)
	}
	foreign := open(fresh.Token, id.New(id.Epoch)+":1")
	if foreign.StatusCode != http.StatusOK {
		t.Fatalf("resumption of another epoch: %d", foreign.StatusCode)
	}
	var first, second string
	sc := bufio.NewScanner(foreign.Body)
	for sc.Scan() && second == "" {
		if line := sc.Text(); strings.HasPrefix(line, "event: ") && first == "" {
			first = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "event: ") {
			second = strings.TrimPrefix(line, "event: ")
		}
	}
	_ = foreign.Body.Close() // the operation is not terminal: the stream stays open
	if first != "reset" || second != "commitment" {
		t.Fatalf("events after another epoch's id: %q then %q", first, second)
	}

	// A revoked identity's token is refused on resumption.
	if rec := revoke(ce.env, ce.human("h-recovery"), ce.key(), `{"identity":"`+fresh.Identity+`","reason":"retired"}`); rec.Code != http.StatusCreated {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if resp := open(fresh.Token, ids[0]); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("resumption after revocation: %d", resp.StatusCode)
	}
}

// §9.1: an event stream takes no query (it resumes with Last-Event-ID), and an operation is read
// whole: its row is locked FOR SHARE before its attempts are read, so a transaction that holds it
// is waited for and never seen between its statements.
func TestOperationReadFollowsItsLock(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	op := xe.completedOperation(t)

	req := httptest.NewRequest(http.MethodGet, prefix+"/operations/"+op+"/events?limit=5", nil)
	req.Header.Set("Authorization", "Bearer "+xe.robot)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	xe.env.api.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid-request") {
		t.Fatalf("an event stream with a query: %d %s", rec.Code, rec.Body)
	}

	tx, err := xe.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`SELECT 1 FROM operation WHERE id = $1 FOR UPDATE`, op); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- xe.get(t, "/operations/"+op).Code }()
	select {
	case code := <-done:
		t.Fatalf("the read returned %d while the operation row was held", code)
	case <-time.After(500 * time.Millisecond):
	}
	_ = tx.Rollback()
	if code := <-done; code != http.StatusOK {
		t.Fatalf("the read after the lock: %d", code)
	}
}

// The first read of a stream is bounded by the token's expiry too: held behind a lock, it ends
// with the token and not with the lock.
func TestEventStreamFirstReadEndsAtTokenExpiry(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	r, err := ce.a.commitPlan(t.Context(), ce.pid)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(ce.a)
	t.Cleanup(srv.Close)
	short, err := auth.NewStore(ce.db, ce.cfg).Issue(t.Context(), "watch", []auth.Role{auth.Viewer}, 1*time.Second, "h-all", "h-all")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := ce.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`SELECT 1 FROM installation_state FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(4*time.Second, func() { _ = tx.Rollback() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+prefix+"/operations/"+r.Operation+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+short.Token)
	req.Header.Set("Accept", "text/event-stream")
	started := time.Now()
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Fatalf("the stream's first read lasted %s on a token of 1s", d)
	}
}

// A read that finishes after the token's expiry writes no frame: the stream ends as the token does.
func TestEventStreamWritesNothingAfterTokenExpiry(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	r, err := ce.a.commitPlan(t.Context(), ce.pid)
	if err != nil {
		t.Fatal(err)
	}
	slow := observer(t, ce.env, &fakeExecutor{}, options{beforeRead: func() { time.Sleep(1500 * time.Millisecond) }})
	srv := httptest.NewServer(slow)
	t.Cleanup(srv.Close)
	short, err := auth.NewStore(ce.db, ce.cfg).Issue(t.Context(), "watch", []auth.Role{auth.Viewer}, 1*time.Second, "h-all", "h-all")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+prefix+"/operations/"+r.Operation+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+short.Token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // the connection ended at the write deadline before any byte: nothing was written
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "event: commitment") {
		t.Fatalf("a frame was written after the token expired: %q", body)
	}
}

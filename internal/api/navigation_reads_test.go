package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// walk follows path's pages one item at a time to the end and answers the identifiers listed, in
// order.
func walk[T any](t *testing.T, get func(string) *httptest.ResponseRecorder, path string, idOf func(T) string) []string {
	t.Helper()
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var ids []string
	cursor := ""
	for range 20 {
		q := path + sep + "limit=1"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		p := decode[listPage[T]](t, get(q), http.StatusOK)
		if len(p.Items) > 1 {
			t.Fatalf("%s: %d items on a page of one", q, len(p.Items))
		}
		for _, it := range p.Items {
			ids = append(ids, idOf(it))
		}
		if p.Next == "" {
			return ids
		}
		cursor = p.Next
	}
	t.Fatalf("%s: no last page", path)
	return nil
}

func sorted(s ...string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// §9.2 (A4, G3): GET /ingestions lists the ingestions in identifier order, filtered by the state
// every read serves and by machine, exactly the matching items across pages; an unknown or
// malformed filter is refused 400 and a cursor of an earlier epoch is refused (§9.1). An ingest
// operation names its ingestion.
func TestIngestionsList(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	get := func(p string) *httptest.ResponseRecorder {
		return ie.do(ie.api, call{method: "GET", path: prefix + p, token: author})
	}
	start := func(k string) string {
		rec := ie.do(ie.api, ingestCall(author, k, ie.etag, ie.body(nil)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		return strings.TrimPrefix(rec.Header().Get("Location"), prefix+"/operations/")
	}
	firstOp := start(key)
	// The first claim's lease lapses, so the next start writes it abandoned (compilation §3.5).
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second'`)
	secondOp := start("k-second-0123456789")
	claimOf := func(op string) string {
		var c string
		if err := ie.db.QueryRow(`SELECT ingestion FROM operation WHERE id = $1`, op).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	abandoned, held := claimOf(firstOp), claimOf(secondOp)
	ingestionID := func(b ingestionBody) string { return b.ID }

	for q, want := range map[string][]string{
		"/ingestions":                                  sorted(abandoned, held),
		"/ingestions?state=held":                       {held},
		"/ingestions?state=abandoned":                  {abandoned},
		"/ingestions?state=released":                   nil,
		"/ingestions?machine=" + ie.machine:            sorted(abandoned, held),
		"/ingestions?machine=" + ie.otherMachine:       nil,
		"/ingestions?state=held&machine=" + ie.machine: {held},
	} {
		if got := walk(t, get, q, ingestionID); !slices.Equal(got, want) {
			t.Fatalf("%s: %v, want %v", q, got, want)
		}
	}
	for _, q := range []string{"?state=nonesuch", "?machine=not-a-machine", "?machine=" + held, "?nonesuch=1",
		"?state=held&state=held", "?limit=0"} {
		wantProblem(t, get("/ingestions"+q), http.StatusBadRequest, "invalid-request")
	}

	for op, claim := range map[string]string{firstOp: abandoned, secondOp: held} {
		b := decode[operationBody](t, get("/operations/"+op), http.StatusOK)
		if b.Kind != "ingest" || b.Subject == nil || b.Subject.Ingestion != claim {
			t.Fatalf("operation %s names %+v, want ingestion %s", op, b.Subject, claim)
		}
		list := decode[listPage[operationBody]](t, get("/operations?kind=ingest"), http.StatusOK)
		if !slices.ContainsFunc(list.Items, func(o operationBody) bool {
			return o.ID == op && o.Subject != nil && o.Subject.Ingestion == claim
		}) {
			t.Fatalf("the listed operation %s does not name ingestion %s: %+v", op, claim, list.Items)
		}
	}

	page := decode[listPage[ingestionBody]](t, get("/ingestions?limit=1"), http.StatusOK)
	if page.Next == "" {
		t.Fatal("no cursor after the first of two ingestions")
	}
	newEpoch(t, ie.db)
	wantProblem(t, get("/ingestions?cursor="+url.QueryEscape(page.Next)), http.StatusBadRequest, "cursor-invalid")
}

// §9.2 (A4, G9): GET /plans filters by the state the read serves, a derived expired included, and
// by machine, and answers exactly the matching plans across pages.
func TestPlansListFilters(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	create := func(k string) string {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, "")), http.StatusCreated).ID
	}
	proposed, cancelled, expired := create("k-plan-a-0123456789"), create("k-plan-b-0123456789"), create("k-plan-c-0123456789")
	decode[cancellationBody](t, p.cancel(p.robot, "k-cancel-0123456789ab", cancelled, `{"reason":"superseded"}`), http.StatusOK)
	mustExec(t, p.db, `ALTER TABLE plan DISABLE TRIGGER immutable_rows`)
	mustExec(t, p.db, `UPDATE plan SET created_at = now() - interval '1 hour', expires_at = now() - interval '1 second' WHERE id = $1`, expired)
	mustExec(t, p.db, `ALTER TABLE plan ENABLE TRIGGER immutable_rows`)
	get := func(q string) *httptest.ResponseRecorder {
		return p.do(p.api, call{method: "GET", path: prefix + q, token: p.robot})
	}
	planID := func(b planBody) string { return b.ID }
	for q, want := range map[string][]string{
		"/plans":                                    sorted(proposed, cancelled, expired),
		"/plans?state=proposed":                     {proposed},
		"/plans?state=cancelled":                    {cancelled},
		"/plans?state=expired":                      {expired},
		"/plans?state=approved":                     nil,
		"/plans?machine=" + p.machine:               sorted(proposed, cancelled, expired),
		"/plans?machine=" + p.target.machine2:       nil,
		"/plans?state=expired&machine=" + p.machine: {expired},
	} {
		if got := walk(t, get, q, planID); !slices.Equal(got, want) {
			t.Fatalf("%s: %v, want %v", q, got, want)
		}
	}
	for _, q := range []string{"?state=nonesuch", "?machine=not-a-machine", "?machine=" + proposed, "?nonesuch=1"} {
		wantProblem(t, get("/plans"+q), http.StatusBadRequest, "invalid-request")
	}
}

// §9.2 (A4, G10): GET /machines filters by cluster, exactly the cluster's machines across pages.
func TestMachinesListCluster(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	third := decode[machineBody](t, ie.do(ie.api, machineCall(author, "k-machine-third-0123", ie.cluster,
		"2d7c8e3a-4b5f-4a71-8cbd-2e3f4a5b6c7d")), http.StatusCreated).ID
	var other string
	if err := ie.db.QueryRow(`SELECT cluster FROM machine WHERE id = $1`, ie.otherMachine).Scan(&other); err != nil {
		t.Fatal(err)
	}
	get := func(q string) *httptest.ResponseRecorder {
		return ie.do(ie.api, call{method: "GET", path: prefix + q, token: author})
	}
	machineID := func(b machineBody) string { return b.ID }
	for q, want := range map[string][]string{
		"/machines":                               sorted(ie.machine, third, ie.otherMachine),
		"/machines?cluster=" + ie.cluster:         sorted(ie.machine, third),
		"/machines?cluster=" + other:              {ie.otherMachine},
		"/machines?cluster=" + id.New(id.Cluster): nil,
	} {
		if got := walk(t, get, q, machineID); !slices.Equal(got, want) {
			t.Fatalf("%s: %v, want %v", q, got, want)
		}
	}
	for _, q := range []string{"?cluster=not-a-cluster", "?cluster=" + ie.machine, "?nonesuch=1"} {
		wantProblem(t, get("/machines"+q), http.StatusBadRequest, "invalid-request")
	}
}

// pending reads machine m through the API's list and its resource, which must agree, and answers
// its pendingConvergence. Neither carries a digest (choice §17.38).
func pending(t *testing.T, e *env, token, m string) bool {
	t.Helper()
	rec := e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m, token: token})
	b := decode[machineBody](t, rec, http.StatusOK)
	if strings.Contains(strings.ToLower(rec.Body.String()), "digest") {
		t.Fatalf("the machine carries a digest: %s", rec.Body)
	}
	list := decode[listPage[machineBody]](t, e.do(e.api, call{method: "GET", path: prefix + "/machines?limit=500", token: token}), http.StatusOK)
	i := slices.IndexFunc(list.Items, func(x machineBody) bool { return x.ID == m })
	if i < 0 || list.Items[i].PendingConvergence != b.PendingConvergence {
		t.Fatalf("the list disagrees with the resource on %s: %+v", m, list.Items)
	}
	return b.PendingConvergence
}

// G24: pendingConvergence is true while the Desired artifact's digest differs from Applied's, and
// false once a normal apply makes them equal.
func TestPendingConvergenceApply(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	if !pending(t, xe.env, xe.robot, xe.machine) {
		t.Fatal("a Desired artifact other than Applied's is not pending")
	}
	xe.completedOperation(t)
	if pending(t, xe.env, xe.robot, xe.machine) {
		t.Fatal("pending after the apply")
	}
}

// G24: a machine with a Desired release and no Applied is pending; with no Desired it is not.
func TestPendingConvergenceNoApplied(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	mustExec(t, p.db, `UPDATE machine_state SET applied_release = NULL, applied_digest = NULL, applied_source = NULL,
		baseline_revision = NULL WHERE machine = $1`, p.machine)
	if !pending(t, p.env, p.robot, p.machine) {
		t.Fatal("Desired with no Applied is not pending")
	}
	mustExec(t, p.db, `UPDATE machine_state SET desired = NULL WHERE machine = $1`, p.machine)
	if pending(t, p.env, p.robot, p.machine) {
		t.Fatal("no Desired is pending")
	}
}

// G24, journeys §5.3/§5.8: an adoption makes Applied the import base's digest. When the release
// compiled the same bytes the machine has converged; when it compiled other bytes the machine is
// pending, though Applied and Desired name the same release.
func TestPendingConvergenceAdoption(t *testing.T) {
	t.Parallel()
	for _, differ := range []bool{false, true} {
		ae := newAdoptEnv(t, "")
		if differ {
			mustExec(t, ae.db, `ALTER TABLE release_machine DISABLE TRIGGER immutable_rows`)
			mustExec(t, ae.db, `UPDATE release_machine SET configuration_digest = $3 WHERE release = $1 AND machine = $2`,
				ae.target.rel, ae.machine, bytes.Repeat([]byte{9}, 32))
			mustExec(t, ae.db, `ALTER TABLE release_machine ENABLE TRIGGER immutable_rows`)
		}
		ae.approveIt(t)
		ae.observed(t, nil)
		if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
			t.Fatal(err)
		}
		b := decode[machineBody](t, ae.do(ae.api, call{method: "GET", path: prefix + "/machines/" + ae.machine, token: ae.robot}), http.StatusOK)
		if b.Desired == nil || b.Applied == nil || *b.Desired != b.Applied.Release || b.Applied.Source != "adoption" {
			t.Fatalf("after the adoption: %+v", b)
		}
		if got := pending(t, ae.env, ae.robot, ae.machine); got != differ {
			t.Fatalf("compiled bytes differ %t: pendingConvergence %t", differ, got)
		}
	}
}

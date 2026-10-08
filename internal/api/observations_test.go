package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// PA §9.2: any role reads a machine's observations, paged in identifier order (§9.1), each with
// its start (purpose, plan, operation, endpoint, controller, time) and what it read, or which
// values it did not read and why (execution-recovery.md §4.1). It answers no configuration and no
// configuration digest, as a plan answers none (§9.1), and nothing of the talosconfig. An unknown
// machine, or another entity's identifier, is 404.
func TestObservationsRead(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	a := observer(t, oe.env, oe.x, options{})
	read, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"})
	if err != nil {
		t.Fatal(err)
	}
	oe.x.err = fmt.Errorf("provider: GET: %w", provider.ErrDenied)
	denied, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "recovery"})
	if err != nil {
		t.Fatal(err)
	}
	viewer := oe.human("h-viewer")
	get := func(p string) *httptest.ResponseRecorder {
		return oe.do(a, call{method: "GET", path: prefix + "/machines/" + p, token: viewer})
	}

	rec := get(oe.machine + "/observations")
	pg := decode[listPage[observationBody]](t, rec, http.StatusOK)
	want := []string{read.ID, denied.ID}
	slices.Sort(want)
	if len(pg.Items) != 2 || pg.Next != "" || pg.Items[0].ID != want[0] || pg.Items[1].ID != want[1] {
		t.Fatalf("observations %+v; want %v", pg, want)
	}
	byID := map[string]observationBody{}
	for _, it := range pg.Items {
		byID[it.ID] = it
	}

	r := byID[read.ID]
	if r.Machine != oe.machine || r.Basis != read.Basis || r.Revision != read.Revision || r.Purpose != "drift" || r.Plan != nil ||
		r.Operation != nil || r.Endpoint != oe.node.Endpoint || r.Controller != a.d.owner.ID || r.StartedAt.IsZero() ||
		!r.StartedAt.Before(r.At) {
		t.Fatalf("observation %+v", r)
	}
	if r.Access == nil || r.Access.Path != oe.accessOf.Path || r.Access.Version != oe.accessOf.Version ||
		r.Access.Created != oe.accessOf.CreatedTime.Format(time.RFC3339Nano) {
		t.Fatalf("access %+v; want %+v", r.Access, oe.accessOf)
	}
	is := func(p *string, v string) bool { return p != nil && *p == v }
	if !is(r.SMBIOSUUID, uuidA) || !is(r.TalosNodeID, nodeA) || !is(r.TalosClusterID, oe.clusterID) ||
		!is(r.AssignmentEvidence, oe.head) || !is(r.RunningVersion, "v1.13.6") || r.ResourceVersion == nil || *r.ResourceVersion == "" ||
		nonNull(r.Health) {
		t.Fatalf("values %+v", r)
	}
	if got := unreadOf(r); fmt.Sprint(got) != fmt.Sprint(map[string]map[string]string{"health": {"cause": "none-bound"}}) {
		t.Fatalf("unread %v", got)
	}

	d := byID[denied.ID]
	if d.Purpose != "recovery" || d.Plan != nil || d.Basis != denied.Basis || d.Revision != denied.Revision || d.Access != nil ||
		d.SMBIOSUUID != nil || d.TalosNodeID != nil || d.TalosClusterID != nil || d.AssignmentEvidence != nil ||
		d.RunningVersion != nil || d.ResourceVersion != nil || nonNull(d.Health) {
		t.Fatalf("denied observation %+v", d)
	}
	if got := unreadOf(d); fmt.Sprint(got) != fmt.Sprint(unreadAll("denied", false)) {
		t.Fatalf("denied unread %v; want %v", got, unreadAll("denied", false))
	}

	digest := talos.ConfigurationDigest([]byte(standInConfig))
	body := rec.Body.String()
	for what, s := range map[string]string{
		"the digest in hex":       hex.EncodeToString(digest[:]),
		"the digest in base64":    base64.StdEncoding.EncodeToString(digest[:]),
		"the digest in base64url": base64.RawURLEncoding.EncodeToString(digest[:]),
		"a digest member":         "igest",
		"the configuration":       standInConfig[:min(24, len(standInConfig))],
	} {
		if strings.Contains(body, s) {
			t.Fatalf("the response answers %s: %s", what, body)
		}
	}
	for _, c := range append(oe.keyCanaries(), runToken) {
		if strings.Contains(body, c) {
			t.Fatalf("the response answers the talosconfig's key: %s", body)
		}
	}

	p1 := decode[listPage[observationBody]](t, get(oe.machine+"/observations?limit=1"), http.StatusOK)
	if len(p1.Items) != 1 || p1.Items[0].ID != want[0] || p1.Next == "" {
		t.Fatalf("first page %+v", p1)
	}
	p2 := decode[listPage[observationBody]](t, get(oe.machine+"/observations?limit=1&cursor="+p1.Next), http.StatusOK)
	if len(p2.Items) != 1 || p2.Items[0].ID != want[1] || p2.Next != "" {
		t.Fatalf("second page %+v", p2)
	}

	// Another machine's observations are not this one's; a machine with none answers an empty page.
	other := decode[machineBody](t, oe.do(oe.api, machineCall(oe.human("h-author"), "k-machine-none-01234", oe.cluster,
		"1c6b7d2f-5a9e-4c3b-8f21-6d0e9a7b4c13")), http.StatusCreated).ID
	if none := decode[listPage[observationBody]](t, get(other+"/observations"), http.StatusOK); none.Items == nil || len(none.Items) != 0 {
		t.Fatalf("other machine %+v", none)
	}
	for _, m := range []string{id.New(id.Machine), oe.cluster, "x"} {
		wantProblem(t, get(m+"/observations"), http.StatusNotFound, "not-found")
	}
	wantProblem(t, get(oe.machine+"/observations?purpose=drift"), http.StatusBadRequest, "invalid-request")
	wantProblem(t, get(oe.machine+"/observations?cursor=x"), http.StatusBadRequest, "cursor-invalid")
}

// nonNull reports whether a JSON member decoded as a raw message holds a value: a JSON null
// decodes to the text null, an absent member to nothing.
func nonNull(m json.RawMessage) bool { return len(m) != 0 && string(m) != "null" }

// unreadOf is an observation body's unread object in unreadAll's form.
func unreadOf(b observationBody) map[string]map[string]string {
	out := map[string]map[string]string{}
	for k, v := range b.Unread {
		m := map[string]string{"cause": v.Cause}
		if v.Code != "" {
			m["code"] = v.Code
		}
		out[k] = m
	}
	return out
}

// PA §3.3: a KV version is identified by its created_time too, which the provider gives to the
// nanosecond; an observation records and answers it exactly, not rounded to microseconds.
func TestObservationAccessCreatedExact(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	v := oe.accessOf
	v.CreatedTime = time.Date(2026, 10, 8, 10, 11, 12, 123456789, time.UTC)
	oe.x.access = provider.NewTalosAccess(v, oe.pki.Talosconfig("", "", ""))
	a := observer(t, oe.env, oe.x, options{})
	if _, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err != nil {
		t.Fatal(err)
	}
	pg := decode[listPage[observationBody]](t, oe.do(a, call{method: "GET", path: prefix + "/machines/" + oe.machine + "/observations",
		token: oe.human("h-viewer")}), http.StatusOK)
	if len(pg.Items) != 1 || pg.Items[0].Access == nil || pg.Items[0].Access.Created != "2026-10-08T10:11:12.123456789Z" {
		t.Fatalf("observations %+v", pg)
	}
}

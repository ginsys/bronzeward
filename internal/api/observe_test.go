package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// fakeExecutor answers access, or err, as the executor identity's read of a cluster's Talos
// access; onRead, when set, runs at each read before it answers.
type fakeExecutor struct {
	mu     sync.Mutex
	access provider.TalosAccess
	err    error
	reads  []string
	onRead func()
}

func (x *fakeExecutor) TalosAccess(_ context.Context, cluster string) (provider.TalosAccess, error) {
	x.mu.Lock()
	x.reads = append(x.reads, cluster)
	hook, acc, err := x.onRead, x.access, x.err
	x.mu.Unlock()
	if hook != nil {
		hook()
	}
	return acc, err
}

// observer is an API over e's database whose executor is x, owning under the current epoch.
func observer(t *testing.T, e *env, x Executor, o options) *API {
	t.Helper()
	d := e.d
	d.exe = x
	d.owner = staging.Owner{ID: "a/1/" + rand.Text(), Epoch: epoch(t, e.db)}
	return e.buildWith(d, o)
}

// obsEnv is a machineEnv whose machine has an assignment, observed through an executor that
// answers the stand-in's talosconfig at version 3.
type obsEnv struct {
	*machineEnv
	x        *fakeExecutor
	head     string // the machine's assignment head revision
	accessOf provider.TalosAccessVersion
}

func newObsEnv(t *testing.T) *obsEnv {
	t.Helper()
	oe := &obsEnv{machineEnv: newMachineEnv(t)}
	oe.accessOf = oe.accessVersion()
	oe.x = &fakeExecutor{access: provider.NewTalosAccess(oe.accessOf, oe.pki.Talosconfig("", "", ""))}
	author := id.New(id.Principal)
	mustExec(t, oe.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'obs', now())`, author)
	oe.head = id.New(id.AssignmentRevision)
	mustExec(t, oe.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		oe.head, oe.cluster, oe.machine, author)
	mustExec(t, oe.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, id.New(id.Assignment), oe.cluster, oe.machine, oe.head)
	return oe
}

// obsRow is one observation row and its start, as stored.
type obsRow struct {
	id                                     string
	basis, revision                        int64
	purpose, endpoint, controller          string
	plan, operation                        sql.NullString
	accessPath, accessCreated              sql.NullString
	accessVersion                          sql.NullInt64
	smbios, nodeID, clusterID, assignment  sql.NullString
	runningVersion, resourceVersion        sql.NullString
	digest                                 []byte
	health                                 sql.NullString
	unread                                 map[string]map[string]string
	startKind, obsKind                     string
	startEntry, obsEntry                   map[string]any
	startEpoch, obsEpoch, installationEpoc string
}

// readObs reads machine's only observation, its start and both timeline entries.
func readObs(t *testing.T, db *sql.DB, machine string) obsRow {
	t.Helper()
	var r obsRow
	var unread, startEntry, obsEntry []byte
	err := db.QueryRow(`SELECT o.id, o.basis, o.revision, s.purpose, s.endpoint, s.controller, s.plan, s.operation,
			o.access_path, to_char(o.access_created AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), o.access_version,
			o.smbios_uuid::text, o.talos_node_id, o.talos_cluster_id, o.assignment_evidence, o.running_version, o.resource_version,
			o.configuration_digest, o.health::text, o.unread, se.kind, oe.kind, se.entry, oe.entry, se.epoch, oe.epoch, i.epoch
		FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
		JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
		JOIN machine_event oe ON oe.machine = o.machine AND oe.revision = o.revision, installation_state i
		WHERE o.machine = $1`, machine).Scan(&r.id, &r.basis, &r.revision, &r.purpose, &r.endpoint, &r.controller, &r.plan, &r.operation,
		&r.accessPath, &r.accessCreated, &r.accessVersion, &r.smbios, &r.nodeID, &r.clusterID, &r.assignment, &r.runningVersion,
		&r.resourceVersion, &r.digest, &r.health, &unread, &r.startKind, &r.obsKind, &startEntry, &obsEntry, &r.startEpoch, &r.obsEpoch,
		&r.installationEpoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range []struct {
		b []byte
		v any
	}{{unread, &r.unread}, {startEntry, &r.startEntry}, {obsEntry, &r.obsEntry}} {
		if err := json.Unmarshal(j.b, j.v); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// unreadAll is the unread object of an observation that read no node value for cause, with the
// access read when accessRead: every node value then carries the cause, and assignment evidence
// too when the access read failed.
func unreadAll(cause string, accessRead bool) map[string]map[string]string {
	u := map[string]map[string]string{"health": {"cause": "none-bound"}}
	names := []string{"identity", "runningVersion", "configuration"}
	if !accessRead {
		names = append(names, "assignmentEvidence")
	}
	for _, n := range names {
		u[n] = map[string]string{"code": "talos-access-unavailable", "cause": cause}
	}
	return u
}

// stored is every text the observations of machine left in the database and the log.
func (oe *obsEnv) stored(t *testing.T) []string {
	t.Helper()
	rows, err := oe.db.Query(`SELECT row_to_json(o)::text FROM observation o WHERE machine = $1
		UNION ALL SELECT row_to_json(s)::text FROM observation_start s WHERE machine = $1
		UNION ALL SELECT row_to_json(e)::text FROM machine_event e WHERE machine = $1`, oe.machine)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	oe.mu.Lock()
	defer oe.mu.Unlock()
	return append(out, oe.logs...)
}

// leaks reports whether any of the talosconfig's key, or the configuration's schema secret, is in
// the database's observation records or the log.
func (oe *obsEnv) leaks(t *testing.T) bool {
	t.Helper()
	canaries := append(oe.keyCanaries(), runToken)
	for _, s := range oe.stored(t) {
		for _, c := range canaries {
			if strings.Contains(s, c) {
				return true
			}
		}
	}
	return false
}

// ER §4.1, PA §3.3: a drift observation records its start (purpose, no plan, the machine's current
// endpoint, the controller) as one timeline entry before the read, then what the read returned
// as the next: the access version, the identity as reported, the assignment head it was taken
// under, the running version, the configuration's digest and resource version, and health unread
// as none bound. Neither the talosconfig nor the configuration is stored or logged, and no request
// carries node metadata.
func TestObserveDrift(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	a := observer(t, oe.env, oe.x, options{})
	var before int64
	if err := oe.db.QueryRow(`SELECT revision_counter FROM machine WHERE id = $1`, oe.machine).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"})
	if err != nil {
		t.Fatal(err)
	}
	r := readObs(t, oe.db, oe.machine)
	want := talos.ConfigurationDigest([]byte(standInConfig))
	if r.id != got.ID || id.MustHave(r.id, id.Observation) != nil || r.basis != before+1 || r.revision != before+2 ||
		got.Basis != r.basis || got.Revision != r.revision {
		t.Fatalf("observation %s at %d/%d (returned %+v); counter was %d", r.id, r.basis, r.revision, got, before)
	}
	if r.purpose != "drift" || r.plan.Valid || r.operation.Valid || r.endpoint != oe.node.Endpoint || r.controller != a.d.owner.ID {
		t.Fatalf("start %+v", r)
	}
	if r.startKind != "observation-started" || r.obsKind != "observation" || r.startEpoch != r.installationEpoc ||
		r.obsEpoch != r.installationEpoc {
		t.Fatalf("entries %s %s under %s, %s (installation %s)", r.startKind, r.obsKind, r.startEpoch, r.obsEpoch, r.installationEpoc)
	}
	if fmt.Sprint(r.startEntry) != fmt.Sprint(map[string]any{"purpose": "drift", "plan": nil, "operation": nil,
		"endpoint": oe.node.Endpoint, "controller": a.d.owner.ID}) {
		t.Fatalf("start entry %v", r.startEntry)
	}
	if fmt.Sprint(r.obsEntry) != fmt.Sprint(map[string]any{"observation": r.id, "purpose": "drift", "basis": float64(r.basis)}) {
		t.Fatalf("observation entry %v", r.obsEntry)
	}
	if r.accessPath.String != "access/talos/"+oe.cluster || r.accessVersion.Int64 != 3 ||
		r.accessCreated.String != "2026-10-02T09:00:00.123456Z" {
		t.Fatalf("access %v %v %v", r.accessPath, r.accessVersion, r.accessCreated)
	}
	if r.smbios.String != uuidA || r.nodeID.String != nodeA || r.clusterID.String != oe.clusterID || r.assignment.String != oe.head ||
		r.runningVersion.String != "v1.13.6" || string(r.digest) != string(want[:]) || r.resourceVersion.String == "" || r.health.Valid {
		t.Fatalf("values %+v", r)
	}
	if fmt.Sprint(r.unread) != fmt.Sprint(map[string]map[string]string{"health": {"cause": "none-bound"}}) {
		t.Fatalf("unread %v", r.unread)
	}
	if got.Digest == nil || *got.Digest != want || got.AssignmentEvidence != oe.head || got.Access == nil || *got.Access != oe.accessOf {
		t.Fatalf("returned %+v", got)
	}
	for _, md := range oe.node.Seen() {
		if len(md.Get("node")) != 0 || len(md.Get("nodes")) != 0 {
			t.Fatalf("a request carried routing metadata: %v", md)
		}
	}
	if len(oe.node.Seen()) == 0 {
		t.Fatal("the node was never read")
	}
	if oe.leaks(t) {
		t.Fatal("the talosconfig's key or the configuration is stored or logged")
	}
}

// ER §4.1 ordering, ruling P54: the start entry is committed before the access is read and before
// the node is dialled, as another connection sees it at each.
func TestObserveStartCommittedBeforeRead(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	started := func() int {
		return count(t, oe.db, `SELECT count(*) FROM observation_start s JOIN machine_event e
			ON e.machine = s.machine AND e.revision = s.revision WHERE s.machine = $1`, oe.machine)
	}
	var atRead, atDial int
	oe.x.onRead = func() { atRead = started() }
	dial := func(ctx context.Context, tc []byte, endpoint string) (talos.Reader, error) {
		atDial = started()
		return talos.Dial(ctx, tc, endpoint)
	}
	a := observer(t, oe.env, oe.x, options{dial: dial})
	if _, err := a.observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err != nil {
		t.Fatal(err)
	}
	if atRead != 1 || atDial != 1 {
		t.Fatalf("committed starts seen at the access read %d, at the dial %d; want 1 each", atRead, atDial)
	}
}

// ER §4.1, PA §3.3: a failed access read is still an observation: its start and an observation
// recording no access version, no node value and no assignment evidence, each named unread with
// talos-access-unavailable and the provider-side cause; the node is never dialled.
func TestObserveAccessReadFails(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		err   error
		cause string
	}{
		"absent":              {fmt.Errorf("provider: GET: %w", provider.ErrAbsent), "absent"},
		"denied":              {fmt.Errorf("provider: GET: %w", provider.ErrDenied), "denied"},
		"malformed":           {fmt.Errorf("provider: GET: %w", provider.ErrProtocol), "malformed"},
		"sealed":              {fmt.Errorf("provider: GET: %w", provider.ErrUnavailable), "provider-unavailable"},
		"unclassified status": {fmt.Errorf("provider: GET: %w", provider.ErrStatus), "provider-unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			oe := newObsEnv(t)
			oe.x.err = c.err
			got, err := observer(t, oe.env, oe.x, options{}).observe(t.Context(), oe.machine, observeFor{purpose: "drift"})
			if err != nil {
				t.Fatal(err)
			}
			r := readObs(t, oe.db, oe.machine)
			if r.accessPath.Valid || r.accessVersion.Valid || r.accessCreated.Valid || r.smbios.Valid || r.nodeID.Valid ||
				r.clusterID.Valid || r.assignment.Valid || r.runningVersion.Valid || r.digest != nil || r.resourceVersion.Valid || r.health.Valid {
				t.Fatalf("values recorded: %+v", r)
			}
			if fmt.Sprint(r.unread) != fmt.Sprint(unreadAll(c.cause, false)) {
				t.Fatalf("unread %v; want %v", r.unread, unreadAll(c.cause, false))
			}
			if got.Access != nil || got.Digest != nil || got.ID != r.id {
				t.Fatalf("returned %+v", got)
			}
			if n := len(oe.node.Seen()); n != 0 {
				t.Fatalf("the node was dialled: %d requests", n)
			}
		})
	}
}

// ER §4.1, PA §3.3: after the access read, a failed connection records the access version and
// the assignment evidence, and names every node value unread with talos-access-unavailable and its
// cause; a value whose own read fails is named unread with its cause, the others read. Neither
// the talosconfig's key nor the configuration is stored or logged.
func TestObserveNodeReadFails(t *testing.T) {
	t.Parallel()
	all := []string{"identity", "runningVersion", "configuration"}
	for name, c := range map[string]struct {
		set    func(t *testing.T, oe *obsEnv)
		cause  string
		unread []string // the node values unread for cause; all when every value
	}{
		"malformed talosconfig": {func(_ *testing.T, oe *obsEnv) {
			bad := strings.Replace(string(oe.pki.Talosconfig("", "", "")), "ca: ", "ca: not-base64-", 1)
			oe.x.access = provider.NewTalosAccess(oe.accessOf, []byte(bad))
		}, "talosconfig", all},
		"a credential Talos refuses": {func(t *testing.T, oe *obsEnv) {
			oe.x.access = provider.NewTalosAccess(oe.accessOf, talostest.NewPKI(t).Talosconfig("", "", ""))
		}, "node-unreachable", all},
		"an endpoint that does not answer": {func(t *testing.T, oe *obsEnv) {
			oe.setEndpoint(t, oe.machine, silentEndpoint(t))
		}, "node-unreachable", all},
		"identity read refused": {func(_ *testing.T, oe *obsEnv) {
			oe.node.Fail(talostest.IdentityType, codes.Internal)
		}, "identity-read", []string{"identity"}},
		"identity outside the recorded forms": {func(t *testing.T, oe *obsEnv) {
			oe.node.SetClusterID(t, "not-a-talos-cluster-id")
		}, "identity-read", []string{"identity"}},
		"configuration read refused": {func(_ *testing.T, oe *obsEnv) {
			oe.node.Fail(talostest.MachineConfigType, codes.PermissionDenied)
		}, "machine-config", []string{"configuration"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			oe := newObsEnv(t)
			c.set(t, oe)
			if _, err := observer(t, oe.env, oe.x, options{nodeTimeout: 500 * time.Millisecond}).observe(t.Context(), oe.machine,
				observeFor{purpose: "drift"}); err != nil {
				t.Fatal(err)
			}
			r := readObs(t, oe.db, oe.machine)
			if r.accessVersion.Int64 != 3 || r.assignment.String != oe.head || r.health.Valid {
				t.Fatalf("access %v, assignment %v, health %v", r.accessVersion, r.assignment, r.health)
			}
			want := map[string]map[string]string{"health": {"cause": "none-bound"}}
			for _, n := range c.unread {
				want[n] = map[string]string{"code": "talos-access-unavailable", "cause": c.cause}
			}
			if fmt.Sprint(r.unread) != fmt.Sprint(want) {
				t.Fatalf("unread %v; want %v", r.unread, want)
			}
			read := map[string]bool{"identity": r.smbios.Valid && r.nodeID.Valid && r.clusterID.Valid,
				"runningVersion": r.runningVersion.Valid, "configuration": r.digest != nil && r.resourceVersion.Valid}
			for n, ok := range read {
				if _, isUnread := want[n]; ok == isUnread {
					t.Fatalf("%s read %v, unread %v", n, ok, isUnread)
				}
			}
			if oe.leaks(t) {
				t.Fatal("the talosconfig's key or the configuration is stored or logged")
			}
		})
	}
}

// Ruling P52: a machine with no assignment is observed with its assignment evidence named unread,
// unassigned, and every node value read.
func TestObserveUnassigned(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	mustExec(t, oe.db, `DELETE FROM assignment WHERE machine = $1`, oe.machine)
	if _, err := observer(t, oe.env, oe.x, options{}).observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err != nil {
		t.Fatal(err)
	}
	r := readObs(t, oe.db, oe.machine)
	want := map[string]map[string]string{"health": {"cause": "none-bound"}, "assignmentEvidence": {"cause": "unassigned"}}
	if r.assignment.Valid || fmt.Sprint(r.unread) != fmt.Sprint(want) || !r.nodeID.Valid || !r.runningVersion.Valid || r.digest == nil {
		t.Fatalf("observation %+v", r)
	}
}

// §16's pass-through control: a dial whose error quotes the talosconfig is recorded by its cause
// alone; under quoteErrors, which logs the error text, the scan catches the key, so it can fail.
func TestObserveErrorTextControl(t *testing.T) {
	t.Parallel()
	quoting := func(_ context.Context, tc []byte, _ string) (talos.Reader, error) {
		return nil, errors.New("talos: cannot parse " + string(tc))
	}
	for _, quote := range []bool{false, true} {
		t.Run(fmt.Sprint("quoteErrors=", quote), func(t *testing.T) {
			t.Parallel()
			oe := newObsEnv(t)
			if _, err := observer(t, oe.env, oe.x, options{dial: quoting, quoteErrors: quote}).observe(t.Context(), oe.machine,
				observeFor{purpose: "drift"}); err != nil {
				t.Fatal(err)
			}
			if got := oe.leaks(t); got != quote {
				t.Fatalf("leaked %v with quoteErrors %v", got, quote)
			}
		})
	}
}

// PA §5.1: a process whose epoch is no longer the installation's, or that owns as no controller,
// records nothing and reads nothing.
func TestObserveRefusesStaleOwner(t *testing.T) {
	t.Parallel()
	oe := newObsEnv(t)
	for name, owner := range map[string]staging.Owner{
		"stale epoch":   {ID: "a/1/" + rand.Text(), Epoch: id.New(id.Epoch)},
		"no controller": {ID: "", Epoch: epoch(t, oe.db)},
	} {
		d := oe.d
		d.exe, d.owner = oe.x, owner
		if _, err := oe.buildWith(d, options{}).observe(t.Context(), oe.machine, observeFor{purpose: "drift"}); err == nil {
			t.Fatalf("%s: observed", name)
		}
	}
	if n := count(t, oe.db, `SELECT count(*) FROM observation_start WHERE machine = $1`, oe.machine); n != 0 {
		t.Fatalf("%d starts recorded", n)
	}
	if len(oe.x.reads) != 0 || len(oe.node.Seen()) != 0 {
		t.Fatalf("read the access %d times, the node %d", len(oe.x.reads), len(oe.node.Seen()))
	}
}

// PA §3.3 and its §16 check (AP §7.1, #25): after the machine's endpoint is replaced from A to
// B, an evidence observation for a plan created at A dials A and records it, the plan's route; a
// drift observation, for no plan, dials the machine's current endpoint B (the control).
func TestObservePlanRoute(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	pki := talostest.NewPKI(t)
	nodeA, nodeB := talostest.Serve(t, pki), talostest.Serve(t, pki)
	mustExec(t, p.db, `UPDATE machine SET talos_endpoint = $2 WHERE id = $1`, p.machine, nodeA.Endpoint)
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), 201)
	if plan.Route != nodeA.Endpoint {
		t.Fatalf("plan route %s; want %s", plan.Route, nodeA.Endpoint)
	}
	mustExec(t, p.db, `UPDATE machine SET talos_endpoint = $2 WHERE id = $1`, p.machine, nodeB.Endpoint)
	x := &fakeExecutor{access: provider.NewTalosAccess(provider.TalosAccessVersion{Path: "access/talos/" + p.cluster, Version: 1,
		CreatedTime: accessCreated}, pki.Talosconfig("", "", ""))}
	a := observer(t, p.env, x, options{})
	ev, err := a.observe(t.Context(), p.machine, observeFor{purpose: "evidence", plan: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeA.Seen()) == 0 || len(nodeB.Seen()) != 0 || ev.Endpoint != nodeA.Endpoint {
		t.Fatalf("evidence dialled %s: A %d requests, B %d", ev.Endpoint, len(nodeA.Seen()), len(nodeB.Seen()))
	}
	var stored, storedPlan string
	if err := p.db.QueryRow(`SELECT endpoint, plan FROM observation_start WHERE machine = $1 AND revision = $2`, p.machine,
		ev.Basis).Scan(&stored, &storedPlan); err != nil {
		t.Fatal(err)
	}
	if stored != nodeA.Endpoint || storedPlan != plan.ID {
		t.Fatalf("start records %s for %s", stored, storedPlan)
	}
	seenA := len(nodeA.Seen())
	drift, err := a.observe(t.Context(), p.machine, observeFor{purpose: "drift"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeB.Seen()) == 0 || len(nodeA.Seen()) != seenA || drift.Endpoint != nodeB.Endpoint {
		t.Fatalf("drift dialled %s: A %d requests, B %d", drift.Endpoint, len(nodeA.Seen())-seenA, len(nodeB.Seen()))
	}
	// A plan of another machine is refused before anything is recorded.
	other := decode[machineBody](t, p.do(p.api, machineCall(p.human("h-author"), "k-machine-other-0123", p.cluster,
		"1c6b7d2f-3a4e-4f60-9bac-1d2e3f4a5b6c")), 201).ID
	if _, err := a.observe(t.Context(), other, observeFor{purpose: "evidence", plan: plan.ID}); err == nil {
		t.Fatal("observed another machine for the plan")
	}
	if n := count(t, p.db, `SELECT count(*) FROM observation_start WHERE machine = $1`, other); n != 0 {
		t.Fatalf("%d starts recorded on the other machine", n)
	}
}

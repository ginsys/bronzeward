package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/compile"
)

// The Kubernetes node name a release records for each machine (persistence-api.md §9.3, API revision 18):
// recorded at publication, served on the release and its review, compared by publication and read by the
// position as the machine that holds it. The names here are synthetic.

// setNode rewrites a seeded release machine's node name record, which the immutable table refuses
// otherwise: a test seeds the rows a release made before or after the record would hold.
func (d *draftEnv) setNode(release, machine string, recorded bool, name *string) {
	d.t.Helper()
	mustExec(d.t, d.db, `ALTER TABLE release_machine DISABLE TRIGGER immutable_rows`)
	mustExec(d.t, d.db, `UPDATE release_machine SET node_name_recorded = $3, node_name = $4 WHERE release = $1 AND machine = $2`,
		release, machine, recorded, name)
}

// A release and its review answer each machine's node name in three states: a string, null (recorded and
// not compared) and absent (not recorded, a release made before the revision), never a hostname.
func TestReleaseServesNodeName(t *testing.T) {
	d := newDraftEnv(t)
	node := "node-a"
	d.nodeNames = [2]nodeRecord{{true, &node}, {true, nil}}
	s := d.release(d.draft, 1, releaseProvenance)
	r := decode[releaseBody](t, d.get("/releases/"+s.rel), http.StatusOK)
	want := map[string]nodeName{d.machine: {state: nodeNameSet, value: node}, s.machine2: {state: nodeNameNull}}
	for _, m := range r.Machines {
		if m.Hostname.NodeName != want[m.Machine] {
			t.Errorf("release machine %s: nodeName %+v; want %+v", m.Machine, m.Hostname.NodeName, want[m.Machine])
		}
		rec := d.get("/releases/" + s.rel + "/machines/" + m.Machine + "/review")
		if v := decode[reviewBody](t, rec, http.StatusOK); v.Hostname.NodeName != want[m.Machine] {
			t.Errorf("review of %s: nodeName %+v; want %+v", m.Machine, v.Hostname.NodeName, want[m.Machine])
		}
		if m.Machine == d.machine && !strings.Contains(rec.Body.String(), `"nodeName":"node-a"`) {
			t.Errorf("review of %s: %s", m.Machine, rec.Body)
		}
		if m.Machine == s.machine2 && !strings.Contains(rec.Body.String(), `"nodeName":null`) {
			t.Errorf("review of %s: a recorded null is not answered as null: %s", m.Machine, rec.Body)
		}
	}
	// Not recorded: a release made before the revision answers no member at all, never null.
	d2 := newDraftEnv(t)
	s2 := d2.release(d2.draft, 1, releaseProvenance)
	rec := d2.get("/releases/" + s2.rel + "/machines/" + d2.machine + "/review")
	if v := decode[reviewBody](t, rec, http.StatusOK); v.Hostname.NodeName.state != nodeNameAbsent || strings.Contains(rec.Body.String(), "nodeName") {
		t.Errorf("a release made before the revision answers %s", rec.Body)
	}
}

// A node name stored for a release that never recorded one, or an empty one, is a 500 as a provenance
// record outside its shape is, and its content reaches neither the answer nor the log.
func TestReleaseNodeNameOutsideTheRecord(t *testing.T) {
	d := newDraftEnv(t)
	empty := ""
	mustExec(t, d.db, `ALTER TABLE release_machine DROP CONSTRAINT release_machine_node_name, DROP CONSTRAINT release_machine_node_name_check`)
	d.nodeNames = [2]nodeRecord{{false, str("STORED-NODE-STAND-IN")}, {true, &empty}}
	s := d.release(d.draft, 1, releaseProvenance)
	for _, m := range []string{d.machine, s.machine2} {
		rec := d.get("/releases/" + s.rel + "/machines/" + m + "/review")
		wantProblem(t, rec, http.StatusInternalServerError, "internal-error")
		if strings.Contains(rec.Body.String(), "STORED-NODE-STAND-IN") || d.logged("STORED-NODE-STAND-IN") {
			t.Errorf("the stored content reached the answer or the log: %s", rec.Body)
		}
	}
}

// Publication records the node name its unit carries and that it was recorded: a string for a known
// name, a recorded null for none.
func TestPublishCommitRecordsNodeName(t *testing.T) {
	for _, c := range []struct {
		label string
		node  *string
	}{{"a node name", str("node-a")}, {"no node name to compare", nil}} {
		p := newPublishEnv(t)
		p.unit.machines[0].nodeName = c.node
		rel, ref := p.commit()
		if ref != nil {
			t.Fatalf("%s: refused: %v", c.label, ref)
		}
		var recorded bool
		var node *string
		if err := p.db.QueryRow(`SELECT node_name_recorded, node_name FROM release_machine WHERE release = $1 AND machine = $2`,
			rel, p.machine).Scan(&recorded, &node); err != nil {
			t.Fatal(err)
		}
		if !recorded || (node == nil) != (c.node == nil) || (node != nil && *node != *c.node) {
			t.Errorf("%s: stored recorded %v, name %v", c.label, recorded, node)
		}
	}
}

// The unit a build makes carries the node name the review shows, from the compiled machine: the filled
// name of a named machine, none for an unnamed one.
func TestBuildReleaseRecordsNodeName(t *testing.T) {
	b := newBuildEnv(t)
	u, ref := b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	if m := u.machines[0]; m.nodeName != nil {
		t.Fatalf("unnamed machine: nodeName %q", *m.nodeName)
	}
	mustExec(t, b.db, `UPDATE machine SET name = 'web-1' WHERE id = $1`, b.machine)
	u, ref = b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	if m := u.machines[0]; m.nodeName == nil || *m.nodeName != "web-1" {
		t.Fatalf("named machine: nodeName %v; want web-1", m.nodeName)
	}
}

// The publication comparison reads the recorded node name of each machine's Applied release
// (compilation.md §6.1 item 6): a release that gives machine M a node name another machine's Applied
// release recorded, and M's own does not hold, is refused 422 `hostname` naming both machines and no
// configured value. Null and absent are never compared; a name M's own Applied already holds is not new.
func TestPublishCommitNodeNameSwap(t *testing.T) {
	node, other := "node-a", "node-b"
	for _, c := range []struct {
		label    string
		hostname compile.Hostname
		ours     *string // the node name the unit gives machine 1; nil is not compared
		holder   nodeRecord
		hostRec  hostRecord // the holder's hostname record
		own      nodeRecord // machine 1's own Applied record
		refused  string     // a fragment of the refusal's words, "" for accepted
	}{
		{"an explicit hostname gives the name another machine holds", compile.Hostname{Source: compile.HostnameConfiguration}, &node,
			nodeRecord{true, &node}, hostRecord{"configuration", nil}, nodeRecord{}, "configured hostname gives the Kubernetes node name node-a"},
		{"a filled name gives the name another machine holds", compile.Hostname{Source: compile.HostnameMachineName, Name: "node-a"}, &node,
			nodeRecord{true, &node}, hostRecord{"configuration", nil}, nodeRecord{}, "still gives the Kubernetes node name node-a"},
		{"the holder recorded another name", compile.Hostname{Source: compile.HostnameConfiguration}, &node,
			nodeRecord{true, &other}, hostRecord{"configuration", nil}, nodeRecord{}, ""},
		{"the holder's name is not compared", compile.Hostname{Source: compile.HostnameConfiguration}, &node,
			nodeRecord{true, nil}, hostRecord{"configuration", nil}, nodeRecord{}, ""},
		{"the holder's name is not recorded", compile.Hostname{Source: compile.HostnameConfiguration}, &node,
			nodeRecord{}, hostRecord{"configuration", nil}, nodeRecord{}, ""},
		{"this machine's name is not compared", compile.Hostname{Source: compile.HostnameConfiguration}, nil,
			nodeRecord{true, &node}, hostRecord{"configuration", nil}, nodeRecord{}, ""},
		{"this machine's own Applied release already holds the name", compile.Hostname{Source: compile.HostnameConfiguration}, &node,
			nodeRecord{true, &node}, hostRecord{"configuration", nil}, nodeRecord{true, &node}, ""},
		{"the holder recorded no node name and filled this machine's name: compared by the recorded name as before",
			compile.Hostname{Source: compile.HostnameMachineName, Name: "node-a"}, &node,
			nodeRecord{}, hostRecord{"machine-name", &node}, nodeRecord{}, "still sets that hostname"},
	} {
		p := newPublishEnv(t)
		p.draftEnv.nodeNames = [2]nodeRecord{c.own, c.holder}
		s := p.applied(1, [2]hostRecord{{"none", nil}, c.hostRec}, func(s releaseSeed) string { return s.machine2 })
		p.cover(s, s.machine2, nil)
		if c.own.recorded { // machine 1's own Applied release is the seeded one, which records c.own
			mustExec(t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
				baseline_revision = 1 WHERE machine = $1`, p.machine, s.rel, make([]byte, 32))
		}
		p.unit.machines[0].hostname, p.unit.machines[0].nodeName = c.hostname, c.ours
		if c.hostname.Source == compile.HostnameMachineName {
			p.named(&c.hostname.Name, &c.hostname.Name)
		}
		rel, ref := p.commit()
		switch {
		case c.refused == "" && (ref != nil || rel == ""):
			t.Errorf("%s: commit %q %v", c.label, rel, ref)
		case c.refused != "" && (ref == nil || ref.status != 422 || ref.code != "validation-failed" ||
			!strings.Contains(ref.detail, c.refused) || ref.extra["rule"] != "hostname" || ref.extra["other"] != s.machine2):
			t.Errorf("%s: got %+v, want 422 validation-failed with %q", c.label, ref, c.refused)
		}
	}
}

// hostnameHeld is a plan env whose machine 1 has a proposed apply-config plan for the target release,
// and whose second machine holds the target release's record of itself as its Applied release. The
// records are the node names given: machine 1's own Applied record, machine 1's record in the target
// release, and the holder's record (the target release's record of machine 2).
func hostnameHeld(t *testing.T, appliedOwn, target, holder nodeRecord) *planEnv {
	t.Helper()
	p := newPlanEnv(t)
	p.setNode(p.applied.rel, p.machine, appliedOwn.recorded, appliedOwn.name)
	p.setNode(p.target.rel, p.machine, target.recorded, target.name)
	p.setNode(p.target.rel, p.target.machine2, holder.recorded, holder.name)
	mustExec(t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, p.target.machine2, p.target.rel, make([]byte, 32))
	decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	return p
}

// The position names the other machine whose Applied release holds the node name the plan's release gives
// this machine (execution-recovery.md §3.2 comparison 6a), by its name or null, and no hostname.
func TestPositionHostnameHeldBy(t *testing.T) {
	a, b := "node-a", "node-b"
	for _, c := range []struct {
		label                  string
		appliedOwn, target, by nodeRecord
		holderName             *string // the holder's name now
		held                   bool
	}{
		{"a named holder", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &a}, str("web-9"), true},
		{"a holder with no name", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &a}, nil, true},
		{"this machine's own Applied holds the name", nodeRecord{true, &a}, nodeRecord{true, &a}, nodeRecord{true, &a}, str("web-9"), false},
		{"this machine's own Applied holds another name", nodeRecord{true, &b}, nodeRecord{true, &a}, nodeRecord{true, &a}, str("web-9"), true},
		{"the holder records another name", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &b}, str("web-9"), false},
		{"the holder's name is not compared", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, nil}, str("web-9"), false},
		{"the holder's name is not recorded", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{}, str("web-9"), false},
		{"this machine's name is not compared", nodeRecord{}, nodeRecord{true, nil}, nodeRecord{true, &a}, str("web-9"), false},
		{"this machine's name is not recorded", nodeRecord{}, nodeRecord{}, nodeRecord{true, &a}, str("web-9"), false},
	} {
		p := hostnameHeld(t, c.appliedOwn, c.target, c.by)
		if c.holderName != nil {
			mustExec(t, p.db, `UPDATE machine SET name = $2 WHERE id = $1`, p.target.machine2, *c.holderName)
		}
		got := p.position(t, p.machine).HostnameHeldBy
		switch {
		case !c.held && got != nil:
			t.Errorf("%s: hostnameHeldBy %+v; want null", c.label, got)
		case c.held && (got == nil || got.ID != p.target.machine2 || (got.Name == nil) != (c.holderName == nil) ||
			(got.Name != nil && *got.Name != *c.holderName)):
			t.Errorf("%s: hostnameHeldBy %+v; want machine %s named %v", c.label, got, p.target.machine2, c.holderName)
		}
	}
}

// With no plan the member is null though another machine holds the name; with an apply-config plan it
// names the holder.
func TestPositionHostnameHeldByNeedsAPlan(t *testing.T) {
	a := "node-a"
	p := newPlanEnv(t)
	p.setNode(p.target.rel, p.machine, true, &a)
	p.setNode(p.target.rel, p.target.machine2, true, &a)
	mustExec(t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, p.target.machine2, p.target.rel, make([]byte, 32))
	if got := p.position(t, p.machine); got.Plan != nil || got.HostnameHeldBy != nil {
		t.Fatalf("no plan: plan %+v, hostnameHeldBy %+v", got.Plan, got.HostnameHeldBy)
	}
	decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	if got := p.position(t, p.machine); got.HostnameHeldBy == nil {
		t.Fatal("an apply-config plan whose name another machine holds: hostnameHeldBy null")
	}
}

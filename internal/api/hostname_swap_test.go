package api

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/dbtest"
)

// applied seeds a release whose two machines carry the hostname records given (the draft env's machine
// first, the second machine after) and makes it the Applied release of the machine named.
func (p *publishEnv) applied(draft int, records [2]hostRecord, machine func(releaseSeed) string) releaseSeed {
	p.t.Helper()
	d := p.draftEnv
	d.hostnames = records
	s := d.release(d.newReleaseDraft(draft), 1, releaseProvenance)
	mustExec(p.t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, machine(s), s.rel, make([]byte, 32))
	return s
}

// cover adds machine, whose applied release is s, to the unit as a release covers it: every machine of
// the cluster with an import base (persistence-api.md choice §17.33), with the import base that
// release recorded and the name the machine holds now.
func (p *publishEnv) cover(s releaseSeed, machine string, name *string) {
	p.t.Helper()
	var ibr string
	if err := p.db.QueryRow(`SELECT import_base_revision FROM release_machine WHERE release = $1 AND machine = $2`,
		s.rel, machine).Scan(&ibr); err != nil {
		p.t.Fatal(err)
	}
	m := p.unit.machines[0]
	m.machine, m.importBase, m.assignment, m.name = machine, ibr, "", ""
	if name != nil {
		m.name = *name
	}
	m.hostname = compile.Hostname{Source: compile.HostnameNone}
	m.observation = dbtest.PublicationObservation(p.t, p.db, machine, p.job.op)
	m.reproduction, m.provenance, m.effective = nil, nil, nil // the seeded import base declares no reference
	p.unit.machines = append(p.unit.machines, m)
}

// A release that fills a hostname from a name another machine's last applied release recorded is
// refused `422 validation-failed` with the rule `hostname` (compilation.md §6.1 item 6), and the other
// machine is named by its current name, or by its identifier where it has none.
func TestPublishCommitHostnameSwap(t *testing.T) {
	web := "web-1"
	for _, c := range []struct {
		label   string
		current *string // the other machine's current name
		holder  func(s releaseSeed) string
	}{
		{"the other machine was renamed", str("web-2"), func(releaseSeed) string { return "web-2" }},
		{"the other machine has no name now", nil, func(s releaseSeed) string { return s.machine2 }},
	} {
		p := newPublishEnv(t)
		s := p.applied(1, [2]hostRecord{{"none", nil}, {"machine-name", &web}}, func(s releaseSeed) string { return s.machine2 })
		if c.current != nil {
			mustExec(t, p.db, `UPDATE machine SET name = $2 WHERE id = $1`, s.machine2, *c.current)
		}
		p.cover(s, s.machine2, c.current)
		p.unit.machines[0].hostname = compile.Hostname{Source: compile.HostnameMachineName, Name: web}
		p.named(&web, &web)
		_, ref := p.commit()
		want := "Machine web-1 would take the hostname web-1 from its name, but the last release applied to machine " + c.holder(s) +
			" still sets that hostname. Give machine web-1 another name for now, apply the release for machine " + c.holder(s) +
			", then give machine web-1 the name web-1."
		if ref == nil || ref.status != 422 || ref.code != "validation-failed" || ref.detail != want ||
			ref.extra["rule"] != "hostname" || ref.extra["machine"] != p.machine || ref.extra["other"] != s.machine2 {
			t.Errorf("%s: got %+v, want 422 validation-failed %q with the rule hostname", c.label, ref, want)
		}
	}
}

// What is accepted: a machine republished with its own name, whose own applied record holds it (the
// steady state, the most common path: a swap check that compared a machine with its own record would
// refuse every republish); another machine's applied record holding another name; a record that is not
// of source machine-name; and a machine whose unit is not filled.
func TestPublishCommitHostnameSwapAccepts(t *testing.T) {
	web, other := "web-1", "web-9"
	for _, c := range []struct {
		label   string
		records [2]hostRecord
		holder  func(releaseSeed) string
		filled  bool
	}{
		{"the machine's own applied record holds its name", [2]hostRecord{{"machine-name", &web}, {"none", nil}},
			func(releaseSeed) string { return "" }, true},
		{"another machine applied another name", [2]hostRecord{{"none", nil}, {"machine-name", &other}},
			func(s releaseSeed) string { return s.machine2 }, true},
		{"another machine's record is of source configuration", [2]hostRecord{{"none", nil}, {"configuration", nil}},
			func(s releaseSeed) string { return s.machine2 }, true},
		{"the machine takes no fill", [2]hostRecord{{"none", nil}, {"machine-name", &web}},
			func(s releaseSeed) string { return s.machine2 }, false},
	} {
		p := newPublishEnv(t)
		s := p.applied(1, c.records, func(s releaseSeed) string {
			if h := c.holder(s); h != "" {
				return h
			}
			return p.machine
		})
		if c.holder(s) != "" {
			p.cover(s, s.machine2, nil)
		}
		if c.filled {
			p.unit.machines[0].hostname = compile.Hostname{Source: compile.HostnameMachineName, Name: web}
			p.named(&web, &web)
		}
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Errorf("%s: commit %q %v", c.label, rel, ref)
		}
	}
}

// swapRoute is a cluster of two machines whose last applied releases record the hostnames filled from
// their names and the node names derived from them, as a release covers every machine of the cluster (persistence-api.md choice §17.33). The
// first is the draft env's machine, the second the seed's.
type swapRoute struct {
	p      *publishEnv
	second string
	seed   string // the import base of the first machine's seeded release, the one it has Applied at the start
}

func newSwapRoute(t *testing.T, first, second string) *swapRoute {
	t.Helper()
	p := newPublishEnv(t)
	p.draftEnv.nodeNames = [2]nodeRecord{{true, &first}, {true, &second}} // the seeded release records each node name
	s := p.applied(1, [2]hostRecord{{"machine-name", &first}, {"machine-name", &second}}, func(s releaseSeed) string { return p.machine })
	mustExec(t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, s.machine2, s.rel, make([]byte, 32))
	r := &swapRoute{p: p, second: s.machine2, seed: s.ibr}
	mustExec(t, p.db, `UPDATE machine SET name = $2 WHERE id = $1`, p.machine, first)
	mustExec(t, p.db, `UPDATE machine SET name = $2 WHERE id = $1`, s.machine2, second)
	p.cover(s, s.machine2, &second)
	return r
}

// publish publishes a release naming the two machines as given, each filled from its name.
func (r *swapRoute) publish(first, second string) (string, *refusal) {
	r.p.t.Helper()
	mustExec(r.p.t, r.p.db, `UPDATE machine SET name = $2 WHERE id = $1`, r.p.machine, first)
	mustExec(r.p.t, r.p.db, `UPDATE machine SET name = $2 WHERE id = $1`, r.second, second)
	for i, name := range []string{first, second} {
		m := &r.p.unit.machines[i]
		m.name = name
		m.hostname = compile.Hostname{Source: compile.HostnameMachineName, Name: name}
		m.nodeName = &name // the release records the node name Talos derives from it, and the check compares that
	}
	return r.p.commit()
}

// apply records the release as the Applied one of the machine, as its plan does.
func (r *swapRoute) apply(machine, release string) {
	r.p.t.Helper()
	mustExec(r.p.t, r.p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation',
		baseline_revision = 1 WHERE machine = $1`, machine, release, make([]byte, 32))
	r.p.rebind()
}

// The route the refusal's text names (compilation.md §6.1 item 6) works, and the two machines exchange their
// names through a third, every release publishing: (1) the first machine is renamed to a new name; (2) until
// that release is applied the second may not take the old name, and after it may; (3) the first then takes the
// name the second gave up. Names are unique in a cluster, so the exchange is never one rename. Each release
// covers both machines.
func TestPublishCommitHostnameExchange(t *testing.T) {
	r := newSwapRoute(t, "web-1", "web-2")
	p := r.p

	rel1, ref := r.publish("web-3", "web-2") // the first machine takes a third name
	if ref != nil {
		t.Fatalf("step 1, the first machine renamed web-3: %+v", ref)
	}
	p.rebind()
	// Before the first machine's release is applied, the second still may not take the name it gave up. A draft
	// with no import base entry compiles on its machine's Applied release's (§3.2), which is still the seed's.
	first := &p.unit.machines[0]
	fixture := first.importBase
	first.importBase, first.reproduction[0].source = r.seed, r.seed
	_, ref = r.publish("web-3", "web-1")
	first.importBase, first.reproduction[0].source = fixture, fixture
	if ref == nil || ref.status != 422 || ref.extra["rule"] != "hostname" {
		t.Fatalf("step 2 before step 1 is applied: got %+v, want 422 with the rule hostname", ref)
	}
	r.apply(p.machine, rel1)
	rel2, ref := r.publish("web-3", "web-1") // the second takes the name the first gave up
	if ref != nil {
		t.Fatalf("step 2, the second machine renamed web-1: %+v", ref)
	}
	r.apply(r.second, rel2)
	if _, ref := r.publish("web-2", "web-1"); ref != nil { // the first takes the name the second gave up
		t.Fatalf("step 3, the first machine renamed web-2: %+v", ref)
	}
}

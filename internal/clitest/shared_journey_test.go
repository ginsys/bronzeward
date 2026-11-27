//go:build linux

package clitest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// addCluster adds a second cluster to e's server: its machine with a Talos stand-in of its own, imported,
// published with no assignment, and adopted, so that a later release of it can be planned with apply-config.
// The returned environment shares e's server, injector and client.
func (e *planEnv) addCluster(t *testing.T, id clusterIdent) *planEnv {
	t.Helper()
	b := &planEnv{f: e.f, in: e.in, ctl: e.ctl, ctlIn: e.ctlIn, bao: e.bao, s: e.f.seedAs(id)}
	b.st = e.f.newStandInAs(t, e.bao, b.s, id, e.ctl)
	h := []string{"--context", "human"}
	b.f.importMachine(t, b.s.machine, b.s.draft)
	b.f.want(b.ctl(append(h, "draft", "show", b.s.draft)...), 0)
	b.f.want(b.ctl(append(h, "publish", b.s.draft)...), 0)
	// A release is adopted only once the machine has an assignment, and one that selects nothing is enough.
	b.publishDraft(t, b.draftWith(t, "assignment", []string{"assignment", "set", b.s.machine, "--draft", "<draft>", "--none"}))
	plan := planID.FindString(b.ctl(b.create("adopt")...).out)
	if b.rel == "" || plan == "" {
		t.Fatalf("the second cluster's publication named release %q and plan %q", b.rel, plan)
	}
	b.approve(t, plan)
	adopted(t, b, plan)
	return b
}

// draftWith creates a draft of the cluster of e, imports its machine, and runs the steps in it, each a
// bronzewardctl command after "--context human" that names the draft by the placeholder "<draft>".
func (e *planEnv) draftWith(t *testing.T, title string, steps ...[]string) string {
	t.Helper()
	h := []string{"--context", "human"}
	d := draftID.FindString(e.ctl(append(h, "draft", "create", "--cluster", e.s.cluster, "--title", title)...).out)
	if d == "" {
		t.Fatalf("draft create %q printed no draft", title)
	}
	e.f.importMachine(t, e.s.machine, d)
	e.f.want(e.ctl(append(h, "draft", "show", d)...), 0)
	for _, step := range steps {
		args := append([]string{}, h...)
		for _, a := range step {
			if a == "<draft>" {
				a = d
			}
			args = append(args, a)
		}
		e.f.want(e.ctl(args...), 0)
	}
	return d
}

// publishDraft publishes draft as e's cluster, which becomes the machine's Desired release, and returns it.
func (e *planEnv) publishDraft(t *testing.T, draft string) string {
	t.Helper()
	r := e.ctl("--context", "human", "publish", draft)
	e.f.want(r, 0)
	rel := regexp.MustCompile(`rel_[a-z2-7]{26}`).FindString(r.out)
	if rel == "" {
		t.Fatalf("publish %s named no release:\n%s", draft, r.out)
	}
	e.rel = rel
	return rel
}

// TestSharedJourney is the client journey of criteria 1 and 2 of ginsys/bronzeward#1193, through
// bronzewardctl against one server holding two clusters, each with a machine behind a Talos stand-in: a shared
// fragment is published from cluster A; cluster B reads nothing of a later revision in its plan until its own draft
// adopts it, and every cluster and machine a change reaches is listed before and after publishing.
// The stand-ins stand for the nodes: no apply to real nodes of an adopted release is run here.
func TestSharedJourney(t *testing.T) {
	a := newPlanEnv(t)
	b := a.addCluster(t, secondCluster)
	f := a.f
	dir := t.TempDir()
	doc := func(v string) string {
		p := filepath.Join(dir, v+".yaml")
		if err := os.WriteFile(p, []byte("apiVersion: v1alpha1\nkind: KubeNodeConfig\nlabels:\n  node.example.test/shared: "+v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	own := filepath.Join(dir, "own.yaml")
	if err := os.WriteFile(own, []byte("apiVersion: v1alpha1\nkind: KubeNodeConfig\nlabels:\n  node.example.test/own: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// What a plan of B holds: the plan create display carries the plan's whole diff.
	plan := func(e *planEnv) string {
		defer e.cancelAll(t)
		return e.ctl(e.create("apply-config")...).out
	}

	// Cluster A publishes the shared fragment, which moves A to it and no other cluster.
	dA1 := a.draftWith(t, "shared one", []string{"fragment", "set", "ntp", "--draft", "<draft>", "--layer", "global", "--file", doc("one"), "--library"})
	a.publishDraft(t, dA1)

	holds := func(out, what string) bool { return strings.Contains(out, what) }
	adoptions := func(behind bool) string {
		args := []string{"--context", "human", "fragment", "adoptions", "ntp"}
		if behind {
			args = append(args, "--behind")
		}
		r := b.ctl(args...)
		f.want(r, 0)
		return r.out
	}

	// B adopts it in a draft of its own: the first revision is in B's plan, and the draft listed the machine it reaches.
	dB1 := b.draftWith(t, "adopt ntp", []string{"fragment", "adopt", "ntp", "--draft", "<draft>"},
		[]string{"assignment", "set", b.s.machine, "--draft", "<draft>", "--fragment", "global=ntp"})
	f.want(b.ctl("--context", "human", "draft", "impact", dB1), 0, b.s.machine)
	b.publishDraft(t, dB1)
	if got := plan(b); !holds(got, "shared: one") {
		t.Fatalf("B's plan after adopting holds no shared change:\n%s", got)
	}
	if got := adoptions(false); !holds(got, a.s.cluster) || !holds(got, b.s.cluster) {
		t.Fatalf("the clusters using ntp do not list both:\n%s", got)
	}

	// A changes it. Before publishing, A's draft lists the other cluster as unchanged; afterwards B is behind, and listed.
	dA2 := a.draftWith(t, "shared two", []string{"fragment", "set", "ntp", "--draft", "<draft>", "--layer", "global", "--file", doc("two"), "--library"})
	f.want(a.ctl("--context", "human", "draft", "impact", dA2), 0, b.s.cluster)
	a.publishDraft(t, dA2)
	behind := adoptions(true)
	if !holds(behind, b.s.cluster) || !holds(behind, b.s.machine) || holds(behind, a.s.cluster) {
		t.Fatalf("the clusters behind ntp are not B alone, with its machine:\n%s", behind)
	}

	// B publishes something else: its plan holds nothing of A's new revision (criterion 1).
	dB2 := b.draftWith(t, "own labels", []string{"fragment", "set", "own", "--draft", "<draft>", "--layer", "role", "--file", own},
		[]string{"assignment", "set", b.s.machine, "--draft", "<draft>", "--fragment", "global=ntp", "--fragment", "role=own"})
	b.publishDraft(t, dB2)
	if got := plan(b); !holds(got, "own: b") || !holds(got, "shared: one") || holds(got, "shared: two") {
		t.Fatalf("B's plan before adopting the new revision is not its own change on the first revision:\n%s", got)
	}

	// B adopts the new revision: the change is in its plan before any approval, and B is no longer behind.
	dB3 := b.draftWith(t, "adopt ntp again", []string{"fragment", "adopt", "ntp", "--draft", "<draft>"})
	f.want(b.ctl("--context", "human", "draft", "impact", dB3), 0, b.s.machine)
	b.publishDraft(t, dB3)
	if got := plan(b); !holds(got, "shared: two") {
		t.Fatalf("B's plan after adopting the new revision holds no change of it:\n%s", got)
	}
	if got := adoptions(true); holds(got, b.s.cluster) {
		t.Fatalf("B is listed behind ntp after adopting its head:\n%s", got)
	}
}

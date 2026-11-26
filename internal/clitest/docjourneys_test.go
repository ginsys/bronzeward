//go:build linux

package clitest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

var (
	draftID   = regexp.MustCompile(`\bdrf_[a-z2-7]{26}\b`)
	releaseID = regexp.MustCompile(`\brel_[a-z2-7]{26}\b`)
	importID  = regexp.MustCompile(`\bing_[a-z2-7]{26}\b`)
)

// signedInAs adds the context "human", which becomes the current one, signed in as the fixture human
// who: the pages' commands name no context.
func (f *fixture) signedInAs(t *testing.T, who string) {
	t.Helper()
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", f.base, "--issuer", f.iss.URL,
		"--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0)
	f.store(f.humanFile(), f.mint(who, ""), "")
}

// file writes a file a page's command names into the directory the commands run in.
func (p *pageRun) file(name, body string) string {
	p.f.t.Helper()
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		p.f.t.Fatal(err)
	}
	return path
}

// bw runs bronzewardctl for a scenario's setup, which no page shows, and wants it to exit 0.
func (p *pageRun) bw(args ...string) string {
	p.f.t.Helper()
	r := p.f.exec(nil, "", args...)
	if r.code != 0 {
		p.f.t.Fatalf("setup: bronzewardctl %v exited %d:\n%s%s", args, r.code, r.err, r.out)
	}
	return r.out
}

// newDraft is a new open draft of the seeded cluster, whose version the client keeps.
func (p *pageRun) newDraft(cluster string) string {
	p.f.t.Helper()
	d := draftID.FindString(p.bw("draft", "create", "--cluster", cluster, "--title", "pages"))
	if d == "" {
		p.f.t.Fatal("setup: draft create printed no draft")
	}
	return d
}

// onPlanEnv is a page run on a plan environment: the seeded machine with its stand-in, published but
// not yet adopted.
func onPlanEnv(t *testing.T, e *planEnv, blocks map[string]string, ran map[string]bool) *pageRun {
	t.Helper()
	e.in.set(nil, nil)
	p := newPageRun(e.f, blocks, ran)
	p.secret = e.st.leaks
	p.set("<machine>", e.s.machine)
	p.set("<cluster>", e.s.cluster)
	return p
}

// adopted waits for the operation the approved plan started and wants it completed.
func adopted(t *testing.T, e *planEnv, plan string) string {
	t.Helper()
	op := (&watchEnv{planEnv: e}).operationOf(t, plan)
	if state := e.f.ended(op); state != "completed" {
		t.Fatalf("the operation %s of plan %s ended %s: %s", op, plan, state, e.f.events(op))
	}
	return op
}

// TestDocBlocks runs every journey page block marked {.bw-run .bw-db} and fails when one of them
// failed or was not run by a scenario. Without a database or an OpenBao it is skipped whole, as the
// harness is; mise run go-db requires both, and checks the record against the pages from outside the
// test, so that a skip there fails the task.
func TestDocBlocks(t *testing.T) {
	blocks, err := docBlocks(pagesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("no page marks a block")
	}
	// Skips here, not only in the scenarios: skipped scenarios would read as blocks that did not run.
	baotest.New(t)
	dbtest.New(t)
	if err := recordHeader(); err != nil {
		t.Fatal(err)
	}
	ran := map[string]bool{}
	for _, s := range []struct {
		name string
		run  func(*testing.T, map[string]string, map[string]bool)
	}{
		{"editing and recording a machine", docEditing},
		{"connect a cluster", docConnect},
		{"publication, import, adoption, tracking and plans", docPublication},
		{"drift", docDrift},
		{"recovery", docRecovery},
	} {
		t.Run(s.name, func(t *testing.T) { s.run(t, blocks, ran) })
	}
	if missing := unrun(blocks, ran); len(missing) > 0 {
		t.Errorf("marked blocks that did not run: %v", missing)
	}
}

func docEditing(t *testing.T, blocks map[string]string, ran map[string]bool) {
	f, _ := startProvider(t, "viewer")
	s := f.seed()
	f.signedInAs(t, "h-all")
	p := newPageRun(f, blocks, ran)
	// The page's profile and assignment name two fragments besides the one it saves.
	p.file("worker-labels.yaml", "machine:\n  nodeLabels:\n    node.example.test/pool: workers\n")
	sysctls := p.file("worker-sysctls.yaml", "machine:\n  sysctls:\n    vm.max_map_count: \"262144\"\n")
	base := p.file("cluster-base.yaml", "machine:\n  nodeLabels:\n    node.example.test/tier: base\n")
	p.bw("draft", "show", s.draft)
	p.bw("fragment", "set", "worker-sysctls", "--draft", s.draft, "--layer", "role", "--file", sysctls)
	p.bw("fragment", "set", "cluster-base", "--draft", s.draft, "--layer", "cluster", "--file", base)
	p.set("<cluster>", s.cluster)
	p.set("<draft>", s.draft)
	p.set("<machine>", s.machine)
	p.set("<node id>", standInNodeID)
	p.must("editing-db-1", "")
	p.must("inventory-db-1", "")
}

func docConnect(t *testing.T, blocks map[string]string, ran map[string]bool) {
	f, _ := startProvider(t, "viewer")
	f.signedInAs(t, "h-all")
	pki := talostest.NewPKI(t)
	node := talostest.Serve(t, pki)
	node.SetSMBIOSUUID(t, "0B5A6C1E-2F3D-4E5F-8A9B-0C1D2E3F4A5B", true)
	node.SetNodeID(t, standInNodeID)
	node.SetClusterID(t, seedClusterID)
	node.SetMembers(t, talostest.Member{NodeID: standInNodeID, Hostname: "bwsynth-cp-1", Addresses: []string{"127.0.0.1"}, ControlPlanePort: 6443})
	p := newPageRun(f, blocks, ran)
	p.file("talosconfig", string(pki.Talosconfig(node.Endpoint, "", "")))
	p.set("<name>", "a")
	p.set("<host|ip[:port]>", node.Endpoint)
	// The command also exits 0 for a node that lists nobody, which would show nothing of the page.
	if out := p.must("connect-a-cluster-db-1", ""); !strings.Contains(out, "bwsynth-cp-1") {
		t.Errorf("cluster discover listed no member:\n%s", out)
	}
}

func docPublication(t *testing.T, blocks map[string]string, ran map[string]bool) {
	e := newPlanEnv(t)
	e.st.node.AcceptApply(t)
	f := e.f
	p := onPlanEnv(t, e, blocks, ran)

	draft := p.newDraft(e.s.cluster)
	f.importMachine(t, e.s.machine, draft)
	p.bw("draft", "show", draft)
	p.set("<draft>", draft)
	p.must("publication-db-1", "", capture{"publish ", "<release>", releaseID})
	p.must("publication-db-2", "")

	p.set("<draft>", p.newDraft(e.s.cluster))
	p.must("protected-import-db-1", "", capture{"import start", "<import>", importID})
	p.must("protected-import-db-2", "")
	p.must("protected-import-db-3", "y\n", capture{"publish ", "<release>", releaseID}, capture{"plan create", "<plan>", planID})
	p.set("<operation>", adopted(t, e, p.values["<plan>"][0]))
	p.must("operation-tracking-db-1", "")
	p.must("operation-tracking-db-2", "")

	// A change to apply: each call publishes a release that differs from what the machine runs.
	release := func(assignment ...string) {
		d := p.newDraft(e.s.cluster)
		p.bw(append([]string{"assignment", "set", e.s.machine, "--draft", d}, assignment...)...)
		r := releaseID.FindString(p.bw("publish", d))
		if r == "" {
			t.Fatal("setup: publish printed no release")
		}
		p.set("<release>", r)
	}
	release("--profiles", "workers")
	p.must("plan-and-approval-db-1", "", capture{"plan create", "<plan>", planID})
	p.must("plan-and-approval-db-2", "y\n")
	adopted(t, e, p.values["<plan>"][0])
	// The two forms of apply are alternatives, each for a release that is not applied yet.
	release("--none")
	p.must("plan-and-approval-db-3", "y\n", capture{"apply ", "<plan>", planID})
	adopted(t, e, p.values["<plan>"][0])
	release("--profiles", "workers")
	p.must("plan-and-approval-db-4", "y\n")
}

func docDrift(t *testing.T, blocks map[string]string, ran map[string]bool) {
	e := newPlanEnvExec(t, ", driftInterval: 1s")
	p := onPlanEnv(t, e, blocks, ran)
	plan := planID.FindString(p.bw("plan", "create", "--release", e.rel, "--machine", e.s.machine, "--operation", "adopt"))
	e.approve(t, plan)
	adopted(t, e, plan)
	e.st.drift(t, 1)
	e.openDrift(t)
	p.set("<why>", "BWSYNTH drill")
	p.must("drift-db-1", "")
	p.must("drift-db-2", "")
	p.must("drift-db-3", "")
	p.set("<draft>", p.newDraft(e.s.cluster))
	p.must("drift-db-4", "")
	p.must("drift-db-5", "")
}

// docRecovery runs the recovery page on a server started for recovery with no entry yet. Its status
// block runs after the entry, since such a server refuses every other request before it. The two
// observations an accounting runs between are seeded, as TestRecoveryJourney seeds them.
func docRecovery(t *testing.T, blocks map[string]string, ran map[string]bool) {
	f, bao := startTimersExec(t, "viewer", "{instance: a, heartbeat: 5s, lease: 15s, absoluteExpiry: 10m, sweep: 15s}", "")
	s := f.seed()
	f.signedInAs(t, "h-all")
	// The stand-in's endpoint is set before the recovery start, which serves only the recovery entry.
	st := f.newStandIn(t, bao.b, s, func(args ...string) result { return f.exec(nil, "", args...) })
	// The server keeps its address across the restart, so the context still names it.
	f.restart("-recovery-start")
	f.store(f.humanFile(), f.mint("h-recovery", ""), "")
	p := newPageRun(f, blocks, ran)
	p.secret = st.leaks
	p.set("<machine>", s.machine)
	p.set("<why>", "BWSYNTH drill")
	p.set("<family>", "database")
	p.set("<backup id>", "BWSYNTH-pg")
	p.set("<RFC3339 time>", "2026-11-01T08:31:00Z", "2026-11-01T08:30:00Z")
	p.must("recovery-after-restore-db-2", "y\n")
	p.must("recovery-after-restore-db-1", "")
	p.set("<observation>", f.seedObservation(s.machine, "drift", "7", 20*time.Minute),
		f.seedObservation(s.machine, "recovery", "7", 10*time.Minute))
	p.must("recovery-after-restore-db-3", "y\n")
	p.must("recovery-after-restore-db-4", "")
	p.must("recovery-after-restore-db-5", "")
	p.must("recovery-after-restore-db-6", "y\n")
}

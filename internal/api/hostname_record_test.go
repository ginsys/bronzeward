package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/ginsys/bronzeward/internal/compile"
)

// newReleaseDraft opens another draft of d's cluster, for one more seeded release.
func (d *draftEnv) newReleaseDraft(n int) string {
	d.t.Helper()
	rec := d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"),
		key: fmt.Sprintf("k-draft-host-%010d", n), body: `{"cluster":"` + d.cluster + `","title":"hostname"}`})
	return decode[draftBody](d.t, rec, http.StatusCreated).ID
}

// A release and its review answer each machine's hostname record (persistence-api.md §9.2, choice
// §17.44): the source, and the name used for machine-name alone. A release made before the record
// existed reads unrecorded.
func TestReleaseServesHostname(t *testing.T) {
	d := newDraftEnv(t)
	name := "web-1"
	for i, c := range []struct{ first, second hostRecord }{
		{hostRecord{"machine-name", &name}, hostRecord{"configuration", nil}},
		{hostRecord{"none", nil}, hostRecord{"legacy-stable", nil}},
		{hostRecord{"unrecorded", nil}, hostRecord{"none", nil}},
	} {
		draft := d.draft
		if i > 0 {
			draft = d.newReleaseDraft(i)
		}
		d.hostnames = [2]hostRecord{c.first, c.second}
		s := d.release(draft, 1, releaseProvenance)
		want := map[string]hostRecord{d.machine: c.first, s.machine2: c.second}

		r := decode[releaseBody](t, d.get("/releases/"+s.rel), http.StatusOK)
		if len(r.Machines) != 2 {
			t.Fatalf("machines %+v", r.Machines)
		}
		for _, m := range r.Machines {
			w := want[m.Machine]
			wantBody := hostnameBody{Source: w.source}
			if w.name != nil {
				wantBody.Name = *w.name
			}
			if m.Hostname != wantBody {
				t.Fatalf("release %s machine %s: hostname %+v; want %+v", s.rel, m.Machine, m.Hostname, wantBody)
			}
			rec := d.get("/releases/" + s.rel + "/machines/" + m.Machine + "/review")
			if v := decode[reviewBody](t, rec, http.StatusOK); v.Hostname != wantBody {
				t.Fatalf("review of %s: hostname %+v; want %+v", m.Machine, v.Hostname, wantBody)
			}
			// Only machine-name carries a name: the member is absent, never null or empty, for the rest.
			if w.name == nil && strings.Contains(rec.Body.String(), `"name"`) {
				t.Fatalf("review of %s names a hostname it did not use: %s", m.Machine, rec.Body)
			}
		}
	}
}

// A stored record outside the enumeration, or whose name does not match its source, is a 500 as a
// provenance record is, and its content reaches neither the answer nor the log (persistence-api.md
// §9.2).
func TestReleaseHostnameOutsideTheEnumeration(t *testing.T) {
	const standIn = "STORED-HOSTNAME-STAND-IN"
	name := standIn
	empty := ""
	for i, c := range []struct {
		label string
		host  hostRecord
		bad   bool
	}{
		{"a source outside the enumeration", hostRecord{standIn, nil}, true},
		{"machine-name with no name", hostRecord{"machine-name", nil}, true},
		{"machine-name with an empty name", hostRecord{"machine-name", &empty}, true},
		{"a name on none", hostRecord{"none", &name}, true},
		{"a name on configuration", hostRecord{"configuration", &name}, true},
		{"a name on legacy-stable", hostRecord{"legacy-stable", &name}, true},
		{"a name on unrecorded", hostRecord{"unrecorded", &name}, true},
		{"control: machine-name with its name", hostRecord{"machine-name", &name}, false},
	} {
		d := newDraftEnv(t)
		// The table refuses these records at commit (internal/migrate TestReleaseConstraints); the
		// read's own guard is shown on a database without those three checks.
		mustExec(t, d.db, `ALTER TABLE release_machine DROP CONSTRAINT release_machine_hostname_source,
			DROP CONSTRAINT release_machine_hostname_name, DROP CONSTRAINT release_machine_hostname_name_check`)
		draft := d.draft
		if i > 0 {
			draft = d.newReleaseDraft(i)
		}
		d.hostnames = [2]hostRecord{c.host, {}}
		s := d.release(draft, 1, releaseProvenance)
		for _, path := range []string{"/releases/" + s.rel, "/releases/" + s.rel + "/machines/" + d.machine + "/review"} {
			rec := d.get(path)
			if !c.bad {
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: %s: %d %s", c.label, path, rec.Code, rec.Body)
				}
				continue
			}
			wantProblem(t, rec, http.StatusInternalServerError, "internal-error")
			if strings.Contains(rec.Body.String(), standIn) || d.logged(standIn) {
				t.Fatalf("%s: the stored content reached the answer or the log: %s", c.label, rec.Body)
			}
		}
	}
}

// publish a machine whose name the snapshot read as snapshot while the machine now holds stored.
func (p *publishEnv) named(snapshot, stored *string) {
	p.t.Helper()
	if snapshot != nil {
		p.unit.machines[0].name = *snapshot
	}
	if stored != nil {
		mustExec(p.t, p.db, `UPDATE machine SET name = $2 WHERE id = $1`, p.machine, *stored)
	}
}

// T3 reads each covered machine's name under the machine row lock the rename takes, and refuses the
// publication `409 stale-input` naming the machine with the name the snapshot read and the name now
// stored, each a string or null (persistence-api.md §6.2, §4.2, choice §17.45). It holds for every
// covered machine, whether or not its hostname was filled.
func TestPublishCommitRenamed(t *testing.T) {
	for _, c := range []struct {
		label            string
		snapshot, stored *string
		expected, actual any
	}{
		{"renamed to another name", str("web-1"), str("web-2"), "web-1", "web-2"},
		{"named at the snapshot, none now", str("web-1"), nil, "web-1", nil}, // the fixture's machine has no name
		{"none at the snapshot, named now", nil, str("web-1"), nil, "web-1"},
	} {
		p := newPublishEnv(t)
		p.named(c.snapshot, c.stored)
		wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"machine": p.machine, "expected": c.expected, "actual": c.actual})
	}
}

// The names are compared as stored: the same name passes, whether or not the hostname was filled
// (a filled machine and an unfilled one), and no name on either side passes (§6.2).
func TestPublishCommitNameUnmoved(t *testing.T) {
	for _, c := range []struct {
		label string
		host  compile.Hostname
		name  *string
	}{
		{"no name at the snapshot, none now", compile.Hostname{Source: compile.HostnameNone}, nil},
		{"the same name, filled", compile.Hostname{Source: compile.HostnameMachineName, Name: "web-1"}, str("web-1")},
		{"the same name, hostname set by the configuration", compile.Hostname{Source: compile.HostnameConfiguration}, str("web-1")},
	} {
		p := newPublishEnv(t)
		p.unit.machines[0].hostname = c.host
		p.named(c.name, c.name)
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("%s: commit %s %v", c.label, rel, ref)
		}
	}
}

// A rename committed while the commit waits for the machine row is found: the commit reads the
// name after the rename's lock is released (persistence-api.md §6.2, §3.5).
func TestPublishCommitRenamedConcurrently(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE machine SET name = 'web-1' WHERE id = $1`, p.machine)
	p.unit.machines[0].name = "web-1"
	p.hold(`UPDATE machine SET name = 'web-2' WHERE id = $1`, p.machine)
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"machine": p.machine, "expected": "web-1", "actual": "web-2"})
}

// One refusal names every moved input (§4.2): a head that moved and a rename together.
func TestPublishCommitRenamedWithAMovedHead(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE fragment SET head_revision = 2 WHERE id = $1`, p.network)
	p.named(str("web-1"), str("web-2"))
	wantConflicts(t, p.refused(409, "stale-input"),
		map[string]any{"head": p.network, "expected": 1, "actual": 2},
		map[string]any{"machine": p.machine, "expected": "web-1", "actual": "web-2"})
}

// The release machine records the hostname the unit carries: its source and, for machine-name, the
// name used (compilation §6.1, persistence-api.md §3.5).
func TestPublishCommitRecordsHostname(t *testing.T) {
	for _, c := range []struct {
		host     compile.Hostname
		wantName *string
	}{
		{compile.Hostname{Source: compile.HostnameMachineName, Name: "web-1"}, str("web-1")},
		{compile.Hostname{Source: compile.HostnameConfiguration}, nil},
		{compile.Hostname{Source: compile.HostnameNone}, nil},
		{compile.Hostname{Source: compile.HostnameLegacyStable}, nil},
	} {
		p := newPublishEnv(t)
		p.unit.machines[0].hostname = c.host
		if c.wantName != nil {
			p.named(c.wantName, c.wantName)
		}
		rel, ref := p.commit()
		if ref != nil {
			t.Fatalf("%v: refused: %v", c.host, ref)
		}
		var source string
		var name *string
		if err := p.db.QueryRow(`SELECT hostname_source, hostname_name FROM release_machine WHERE release = $1 AND machine = $2`,
			rel, p.machine).Scan(&source, &name); err != nil {
			t.Fatal(err)
		}
		if source != string(c.host.Source) || (name == nil) != (c.wantName == nil) || (name != nil && *name != *c.wantName) {
			t.Fatalf("%v: stored %q %v", c.host, source, name)
		}
	}
}

// withHostnameFill turns the fill on for the calling test and restores it at its end. The caller is
// a top-level test with no t.Parallel() and no parallel subtest (see fillHostname).
func withHostnameFill(t *testing.T) {
	t.Helper()
	fillHostname = true
	t.Cleanup(func() { fillHostname = false })
}

// Until the clash refusal exists (part D of ginsys/bronzeward#1040) a release fills no hostname: a
// named machine records `none`, the unit still carries the name the snapshot read (the name check
// of the commit compares it), and two machines whose names share a first label, which Talos would
// give one node name, both build without a fill.
func TestBuildReleaseFillsNoHostname(t *testing.T) {
	b := newBuildEnv(t)
	second := b.secondMachine(constants.DefaultKubernetesVersion)
	none := compile.Hostname{Source: compile.HostnameNone}
	for _, c := range []struct{ first, second string }{{"", ""}, {"web-1", ""}, {"a.x.org", "a.y.org"}} {
		mustExec(t, b.db, `UPDATE machine SET name = NULLIF($2, '') WHERE id = $1`, b.machine, c.first)
		mustExec(t, b.db, `UPDATE machine SET name = NULLIF($2, '') WHERE id = $1`, second, c.second)
		u, ref := b.build()
		if ref != nil {
			t.Fatalf("names %q %q: buildRelease refused: %v %v", c.first, c.second, ref, ref.extra)
		}
		want := map[string]string{b.machine: c.first, second: c.second}
		if len(u.machines) != 2 {
			t.Fatalf("machines %d", len(u.machines))
		}
		for _, m := range u.machines {
			if m.name != want[m.machine] || m.hostname != none {
				t.Fatalf("names %q %q: machine %s has name %q, hostname %+v; want the name carried and %+v",
					c.first, c.second, m.machine, m.name, m.hostname, none)
			}
		}
	}
}

// With the fill on (compilation §6.1): an unnamed machine takes no fill, a named one is filled, and
// the unit carries the name the snapshot read beside the decision, which the commit then stores.
func TestBuildReleaseFillsHostname(t *testing.T) {
	withHostnameFill(t)
	b := newBuildEnv(t)
	u, ref := b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	if m := u.machines[0]; m.name != "" || m.hostname != (compile.Hostname{Source: compile.HostnameNone}) {
		t.Fatalf("unnamed machine: name %q, hostname %+v", m.name, m.hostname)
	}
	mustExec(t, b.db, `UPDATE machine SET name = 'web-1' WHERE id = $1`, b.machine)
	u, ref = b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	want := compile.Hostname{Source: compile.HostnameMachineName, Name: "web-1"}
	if m := u.machines[0]; m.name != "web-1" || m.hostname != want {
		t.Fatalf("named machine: name %q, hostname %+v; want %+v", m.name, m.hostname, want)
	}
	b.unit = u
	rel, ref := b.commit()
	if ref != nil {
		t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	var source, name string
	if err := b.db.QueryRow(`SELECT hostname_source, hostname_name FROM release_machine WHERE release = $1 AND machine = $2`,
		rel, b.machine).Scan(&source, &name); err != nil {
		t.Fatal(err)
	}
	if source != "machine-name" || name != "web-1" {
		t.Fatalf("stored %q %q", source, name)
	}
}

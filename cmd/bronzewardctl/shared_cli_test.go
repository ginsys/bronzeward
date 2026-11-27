package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sharedFake is a draft of cluster cl_BWSYNTH (lab-a) that holds its own entry, an adoption and a shared
// edit, beside the shared fragments of the library: the shared verbs' routes, answered as the API's
// are (api/openapi.yaml), over draftFake for the rest. It records every write and refuses a route
// named in refuse with the problem given.
type sharedFake struct {
	*draftFake
	mu      sync.Mutex
	sent    []string
	reads   []string
	refuse  map[string]string
	entries string
	impact  string // the answer of the impact route, when a case needs another
}

const sharedEntries = `{"kind":"fragment","name":"own","head":"frg_own","base":1,"revision":"frv_own2"},` +
	`{"kind":"adoption","of":"fragment","name":"dns","head":"frg_dns","base":null,"revision":"frv_dns2"},` +
	`{"kind":"fragment","name":"lib","scope":"library","head":"frg_lib","base":1,"adoptionBase":null,"revision":"frv_lib2"}`

func (f *sharedFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.URL.RawQuery != "" {
		route += "?" + r.URL.RawQuery
	}
	f.mu.Lock()
	if r.Method != http.MethodGet {
		body, _ := io.ReadAll(r.Body)
		f.sent = append(f.sent, route+" "+string(body))
	} else {
		f.reads = append(f.reads, route)
	}
	problem := f.refuse[route]
	f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
	w.Header().Set("Content-Type", "application/json")
	if problem != "" {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, problem)
		return
	}
	write := func(body string) {
		w.Header().Set("ETag", `"9-BWSYNTH"`)
		fmt.Fprint(w, body)
	}
	switch strings.SplitN(route, "?", 2)[0] {
	case "GET /clusters":
		fmt.Fprint(w, `{"items":[{"id":"cl_BWSYNTH","name":"lab-a","endpoint":"https://BWSYNTH.example.invalid","contract":"v1.0","talosClusterId":"BWSYNTH"},`+
			`{"id":"cl_other","name":"prod","endpoint":"https://BWSYNTH.example.invalid","contract":"v1.0","talosClusterId":"BWSYNTH2"}]}`)
	case "GET /drafts/drf_BWSYNTH":
		entries := sharedEntries
		if f.entries != "" {
			entries = f.entries
		}
		w.Header().Set("ETag", `"3-BWSYNTH"`)
		fmt.Fprintf(w, `{"id":"drf_BWSYNTH","cluster":"cl_BWSYNTH","title":"BWSYNTH","state":"open","revision":3,"entries":[%s]}`, entries)
	case "GET /fragments":
		fmt.Fprint(w, `{"items":[{"id":"frg_own","name":"own","cluster":"cl_BWSYNTH","revision":"frv_own1"},`+
			`{"id":"frg_head","name":"head","cluster":"cl_BWSYNTH","revision":"frv_head1"},`+
			`{"id":"frg_ntp","name":"ntp","cluster":null,"revision":"frv_ntp3"},`+
			`{"id":"frg_dns","name":"dns","cluster":null,"revision":"frv_dns1"},`+
			`{"id":"frg_lib","name":"lib","cluster":null,"revision":"frv_lib1"}]}`)
	case "GET /profiles":
		fmt.Fprint(w, `{"items":[{"id":"prf_base","name":"base-set","cluster":null,"revision":"prv_base1"}]}`)
	case "GET /profiles/prf_base/adoptions":
		fmt.Fprint(w, `{"items":[{"cluster":"cl_other","revision":"prv_base1","adoptionRevision":1,"behind":false,"release":"rel_2","via":null,"machines":["mch_z"]}]}`)
	case "GET /fragments/frg_ntp/adoptions":
		rows := `{"cluster":"cl_BWSYNTH","revision":"frv_via","adoptionRevision":null,"behind":true,"release":null,"via":"prf_x","machines":["mch_c"]},` +
			`{"cluster":"cl_BWSYNTH","revision":"frv_ntp1","adoptionRevision":2,"behind":true,"release":"rel_1","via":null,"machines":["mch_a","mch_b"]},` +
			`{"cluster":"cl_other","revision":"frv_ntp3","adoptionRevision":1,"behind":false,"release":"rel_2","via":null,"machines":["mch_z"]}`
		if r.URL.Query().Get("behind") == "true" {
			rows = `{"cluster":"cl_BWSYNTH","revision":"frv_ntp1","adoptionRevision":2,"behind":true,"release":"rel_1","via":null,"machines":["mch_a","mch_b"]}`
		}
		fmt.Fprintf(w, `{"items":[%s]}`, rows)
	case "GET /fragments/frg_dns/adoptions":
		fmt.Fprint(w, `{"items":[{"cluster":"cl_BWSYNTH","revision":"frv_dns1","adoptionRevision":1,"behind":false,"release":"rel_1","via":null,"machines":[]}]}`)
	case "GET /drafts/drf_BWSYNTH/impact":
		if f.impact != "" {
			fmt.Fprint(w, f.impact)
			return
		}
		fmt.Fprint(w, `{"items":[{"kind":"fragment","name":"ntp","scope":"library","machines":[`+
			`{"machine":"mch_a","reason":"assignment","change":"gains"},{"machine":"mch_b","reason":"profile","change":"gains"},`+
			`{"machine":"mch_c","reason":"adoption","change":"keeps"}],`+
			`"otherClusters":[{"cluster":"cl_other","revision":"frv_ntp3","via":null,"unchanged":true}]},`+
			`{"kind":"profile","name":"idle","scope":"cluster","machines":[],"otherClusters":[]}]}`)
	case "PUT /drafts/drf_BWSYNTH/adoptions/fragments/ntp", "DELETE /drafts/drf_BWSYNTH/adoptions/fragments/ntp", "PUT /drafts/drf_BWSYNTH/adoptions/profiles/base-set":
		write(`{"draft":"drf_BWSYNTH","entry":{"kind":"adoption","of":"fragment","name":"ntp","head":"frg_ntp","base":2,"revision":"frv_ntp3"}}`)
	case "POST /drafts/drf_BWSYNTH/adoptions/fragments/dns/detachments":
		write(`{"draft":"drf_BWSYNTH","entry":{"kind":"adoption","of":"fragment","name":"dns","head":"frg_dns","base":1,"revision":null},` +
			`"copy":{"kind":"fragment","name":"dns","head":null,"base":null,"revision":"frv_copy"}}`)
	case "PUT /drafts/drf_BWSYNTH/fragments/ntp":
		write(`{"draft":"drf_BWSYNTH","entry":{"kind":"fragment","name":"ntp","scope":"library","head":"frg_ntp","base":3,"adoptionBase":2,"revision":"frv_new"},"ingestion":"ing_BWSYNTH"}`)
	case "PUT /drafts/drf_BWSYNTH/profiles/p", "PUT /drafts/drf_BWSYNTH/profiles/shared-set", "DELETE /drafts/drf_BWSYNTH/profiles/shared-set", "DELETE /drafts/drf_BWSYNTH/fragments/ntp":
		write(`{"draft":"drf_BWSYNTH","entry":{"kind":"profile","name":"shared-set","scope":"library","head":null,"base":null,"revision":"prv_new"}}`)
	default:
		f.draftFake.ServeHTTP(w, r)
	}
}

func (f *sharedFake) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sent
	f.sent, f.reads = nil, nil
	return s
}

// sharedRun runs one command against the fake and returns its exit code and output.
func sharedRun(t *testing.T, f *sharedFake, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := run(append([]string{"--no-input"}, args...), &o, &e)
	return code, o.String(), e.String()
}

func sharedServer(t *testing.T) *sharedFake {
	t.Helper()
	f := &sharedFake{draftFake: &draftFake{rev: 3, state: "open"}, refuse: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	return f
}

func wantAll(t *testing.T, name, got string, want ...string) {
	t.Helper()
	flat := strings.Join(strings.Fields(got), " ")
	for _, w := range want {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("%s: no %q in\n%s", name, w, got)
		}
	}
}

const ifMatch = `--if-match="3-BWSYNTH"`

// --library writes a shared entry: the body names the scope, a removal names it in the query, and
// the answer says whom publishing moves.
func TestLibraryWrites(t *testing.T) {
	f := sharedServer(t)
	doc := filepath.Join(t.TempDir(), "ntp.yaml")
	if err := os.WriteFile(doc, []byte("machine:\n  env:\n    BW_NTP: pool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		args  []string
		want  []string
		wrote string
	}{
		{"set a shared fragment", []string{"fragment", "set", "ntp", "--library", "--draft", "drf_BWSYNTH", "--layer", "global", "--file", doc, ifMatch},
			[]string{"Shared fragment ntp saved in draft drf_BWSYNTH (revision frv_new; draft revision 9).",
				"Publishing this draft moves cluster lab-a to it. No other cluster changes."},
			`PUT /drafts/drf_BWSYNTH/fragments/ntp {"document":"machine:\n  env:\n    BW_NTP: pool\n","layer":"global","scope":"library"}`},
		{"remove a shared fragment", []string{"fragment", "rm", "ntp", "--library", "--draft", "drf_BWSYNTH", ifMatch},
			[]string{"Shared fragment ntp removed from draft drf_BWSYNTH (draft revision 9)."},
			`DELETE /drafts/drf_BWSYNTH/fragments/ntp?scope=library `},
		{"set a shared profile", []string{"profile", "set", "shared-set", "--library", "--draft", "drf_BWSYNTH", "--fragments", "ntp,frv_given", ifMatch},
			[]string{"Shared profile shared-set saved: pins ntp frv_ntp3, frv_given (draft revision 9).", "moves cluster lab-a to it"},
			`PUT /drafts/drf_BWSYNTH/profiles/shared-set {"fragments":["frv_ntp3","frv_given"],"scope":"library"}`},
		{"remove a shared profile", []string{"profile", "rm", "shared-set", "--library", "--draft", "drf_BWSYNTH", ifMatch},
			[]string{"Shared profile shared-set removed from draft drf_BWSYNTH"},
			`DELETE /drafts/drf_BWSYNTH/profiles/shared-set?scope=library `},
		{"remove the cluster's own, no query", []string{"profile", "rm", "shared-set", "--draft", "drf_BWSYNTH", ifMatch},
			[]string{"Profile shared-set removed from draft drf_BWSYNTH"},
			`DELETE /drafts/drf_BWSYNTH/profiles/shared-set `},
	} {
		code, out, errText := sharedRun(t, f, c.args...)
		took := f.took()
		if code != 0 || len(took) != 1 || took[0] != c.wrote {
			t.Errorf("%s: exit %d, sent %q, want one write %q; stderr %s", c.name, code, took, c.wrote, errText)
		}
		wantAll(t, c.name, out, c.want...)
	}
}

// A profile's fragment names resolve through the cluster's view (contract 12): the draft's own entry,
// its adoption or shared edit, the cluster's own head, then the adoption the shared head lists, never a
// row that reaches the name through a profile.
func TestPinsThroughTheView(t *testing.T) {
	f := sharedServer(t)
	for _, c := range []struct{ names, pins string }{
		{"own", "frv_own2"}, // the draft's entry beats the head frv_own1
		{"dns", "frv_dns2"}, // the draft's adoption beats the adoption the list shows
		{"lib", "frv_lib2"}, // a shared edit of the draft is adopted by the cluster that publishes it
		{"head", "frv_head1"},
		{"ntp", "frv_ntp1"}, // adopted: the via row of the list is skipped
	} {
		code, _, errText := sharedRun(t, f, "profile", "set", "p", "--draft", "drf_BWSYNTH", "--fragments", c.names, ifMatch)
		took := f.took()
		want := `PUT /drafts/drf_BWSYNTH/profiles/p {"fragments":["` + c.pins + `"]}`
		if code != 0 || len(took) != 1 || took[0] != want {
			t.Errorf("%s: exit %d, sent %q, want %q; stderr %s", c.names, code, took, want, errText)
		}
	}
	// No view at all names what was read.
	code, _, errText := sharedRun(t, f, "profile", "set", "p", "--draft", "drf_BWSYNTH", "--fragments", "nothing", ifMatch)
	if code != 1 || !strings.Contains(errText, "cluster cl_BWSYNTH has no fragment 'nothing' with a revision, adopts none and draft drf_BWSYNTH proposes none; nothing was sent") {
		t.Errorf("no view: exit %d, stderr %s", code, errText)
	}
	// A shared profile pins shared revisions: the draft's shared edit, else the shared head, never the
	// cluster's own entry of the same name.
	for _, c := range []struct{ name, pin string }{{"lib", "frv_lib2"}, {"ntp", "frv_ntp3"}, {"dns", "frv_dns1"}} {
		code, _, errText := sharedRun(t, f, "profile", "set", "p", "--library", "--draft", "drf_BWSYNTH", "--fragments", c.name, ifMatch)
		took := f.took()
		want := `PUT /drafts/drf_BWSYNTH/profiles/p {"fragments":["` + c.pin + `"],"scope":"library"}`
		if code != 0 || len(took) != 1 || took[0] != want {
			t.Errorf("library %s: exit %d, sent %q, want %q; stderr %s", c.name, code, took, want, errText)
		}
	}
	code, _, errText = sharedRun(t, f, "profile", "set", "p", "--library", "--draft", "drf_BWSYNTH", "--fragments", "own", ifMatch)
	if code != 1 || !strings.Contains(errText, "there is no shared fragment 'own' with a revision, and draft drf_BWSYNTH proposes none; nothing was sent") {
		t.Errorf("library, a cluster's own name: exit %d, stderr %s", code, errText)
	}
	// A shared profile cannot pin a shared fragment the same draft removes.
	f.entries = `{"kind":"fragment","name":"ntp","scope":"library","head":"frg_ntp","base":3,"adoptionBase":null,"revision":null}`
	code, _, errText = sharedRun(t, f, "profile", "set", "p", "--library", "--draft", "drf_BWSYNTH", "--fragments", "ntp", ifMatch)
	if got := f.took(); code != 1 || len(got) != 0 || !strings.Contains(errText, "draft drf_BWSYNTH removes the fragment 'ntp', so a profile cannot pin it; nothing was sent") {
		t.Errorf("library, a shared head the draft removes: exit %d, sent %q, stderr %s", code, got, errText)
	}
}

// adopt and detach: the requests, and the words a person reads.
func TestAdoptAndDetach(t *testing.T) {
	f := sharedServer(t)
	for _, c := range []struct {
		name  string
		args  []string
		code  int
		want  []string
		wrote string
	}{
		{"adopt the head", []string{"fragment", "adopt", "ntp", "--draft", "drf_BWSYNTH", ifMatch}, 0,
			[]string{"Cluster lab-a adopts fragment ntp frv_ntp3, was frv_ntp1 (draft revision 9). Reaches 2 machines.",
				"A profile of this cluster that pins the name keeps its pin: set it again to use the new revision."},
			`PUT /drafts/drf_BWSYNTH/adoptions/fragments/ntp {"revision":"frv_ntp3"}`},
		{"adopt a revision", []string{"fragment", "adopt", "ntp", "--draft", "drf_BWSYNTH", "--revision", "frv_ntp1", ifMatch}, 0,
			[]string{"adopts fragment ntp frv_ntp1"}, `PUT /drafts/drf_BWSYNTH/adoptions/fragments/ntp {"revision":"frv_ntp1"}`},
		{"stop following", []string{"fragment", "adopt", "ntp", "--draft", "drf_BWSYNTH", "--remove", ifMatch}, 0,
			[]string{"Cluster lab-a stops following fragment ntp, was frv_ntp1 (draft revision 9)."}, `DELETE /drafts/drf_BWSYNTH/adoptions/fragments/ntp `},
		{"adopt a profile", []string{"profile", "adopt", "base-set", "--draft", "drf_BWSYNTH", ifMatch}, 0,
			[]string{"adopts profile base-set prv_base1, was none"}, `PUT /drafts/drf_BWSYNTH/adoptions/profiles/base-set {"revision":"prv_base1"}`},
		{"detach", []string{"fragment", "detach", "dns", "--draft", "drf_BWSYNTH", ifMatch}, 0,
			[]string{"Cluster lab-a stops following fragment dns; its own fragment dns now holds frv_copy (draft revision 9)."},
			`POST /drafts/drf_BWSYNTH/adoptions/fragments/dns/detachments `},
		{"remove with a revision", []string{"fragment", "adopt", "ntp", "--draft", "drf_BWSYNTH", "--remove", "--revision", "frv_ntp1"}, 2, nil, ""},
		{"a revision of the other kind", []string{"profile", "adopt", "base-set", "--draft", "drf_BWSYNTH", "--revision", "frv_x"}, 2, nil, ""},
		{"no draft", []string{"fragment", "adopt", "ntp"}, 2, nil, ""},
		{"a name the library lacks", []string{"fragment", "adopt", "absent", "--draft", "drf_BWSYNTH", ifMatch}, 1, nil, ""},
	} {
		code, out, errText := sharedRun(t, f, c.args...)
		took := f.took()
		if code != c.code {
			t.Errorf("%s: exit %d, stderr %s; want %d", c.name, code, errText, c.code)
		}
		if c.wrote == "" && len(took) != 0 || c.wrote != "" && (len(took) != 1 || took[0] != c.wrote) {
			t.Errorf("%s: sent %q, want %q", c.name, took, c.wrote)
		}
		wantAll(t, c.name, out, c.want...)
	}

	// A repeat gives the revision that was sent, not the head it would resolve now.
	code, _, errText := sharedRun(t, f, "fragment", "adopt", "ntp", "--draft", "drf_BWSYNTH", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa",
		"--expect-epoch", "ep_BWSYNTH-1", "--expect-principal", "svc_BWSYNTH", ifMatch)
	if code != 2 || !strings.Contains(errText, "a repeat of fragment adopt gives --revision as the outcome-unknown block printed it; nothing was sent") {
		t.Errorf("a repeat with no revision: exit %d, stderr %s", code, errText)
	}
	f.took()
}

// readRoutes are the reads the fake has answered since the last took, without clearing them.
func (f *sharedFake) readRoutes() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.reads, "\n")
}

// adoptions lists each cluster on a shared name with its revision, the head, the release and the machines; a
// row that reaches the name through a profile says which; --behind sends the query and nothing is written.
func TestSharedAdoptions(t *testing.T) {
	f := sharedServer(t)
	code, out, errText := sharedRun(t, f, "fragment", "adoptions", "ntp")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errText)
	}
	wantAll(t, "fragment adoptions", out, "CLUSTER ADOPTED HEAD RELEASE MACHINES VIA",
		"lab-a frv_via frv_ntp3 - 1 prf_x", "lab-a frv_ntp1 frv_ntp3 rel_1 2 -", "prod frv_ntp3 frv_ntp3 rel_2 1 -")
	if reads := f.readRoutes(); !strings.Contains(reads, "GET /fragments/frg_ntp/adoptions") || strings.Contains(reads, "behind") {
		t.Errorf("reads %q: want the unfiltered adoptions read", reads)
	}
	if got := f.took(); len(got) != 0 {
		t.Errorf("a read sent %q", got)
	}

	code, out, errText = sharedRun(t, f, "fragment", "adoptions", "ntp", "--behind")
	if code != 0 || !strings.Contains(f.readRoutes(), "GET /fragments/frg_ntp/adoptions?behind=true") {
		t.Errorf("--behind: exit %d, reads %q, stderr %s", code, f.readRoutes(), errText)
	}
	wantAll(t, "--behind", out, "lab-a frv_ntp1 frv_ntp3 rel_1 2 -")
	if strings.Contains(out, "prod") || strings.Contains(out, "frv_via") {
		t.Errorf("--behind lists a cluster that is not behind:\n%s", out)
	}
	f.took()

	code, out, errText = sharedRun(t, f, "profile", "adoptions", "base-set")
	if code != 0 {
		t.Fatalf("profile adoptions: exit %d, stderr %s", code, errText)
	}
	wantAll(t, "profile adoptions", out, "CLUSTER ADOPTED HEAD RELEASE MACHINES VIA", "prod prv_base1 prv_base1 rel_2 1 -")

	// A cluster's own name has no shared head: the sentence names it, and no adoptions are read.
	f.took()
	code, _, errText = sharedRun(t, f, "fragment", "adoptions", "own")
	if code != 1 || !strings.Contains(errText, "there is no shared fragment 'own'") || strings.Contains(f.readRoutes(), "/adoptions") {
		t.Errorf("a cluster's own name: exit %d, reads %q, stderr %s", code, f.readRoutes(), errText)
	}
	code, _, errText = sharedRun(t, f, "profile", "adoptions", "absent")
	if code != 1 || !strings.Contains(errText, "there is no shared profile 'absent'") {
		t.Errorf("an absent profile: exit %d, stderr %s", code, errText)
	}
	if got := f.took(); len(got) != 0 {
		t.Errorf("a read sent %q", got)
	}
}

// draft impact prints one block per entry: what this cluster's machines gain, lose or keep, and the other
// clusters that stay unchanged; it renders nothing and writes nothing.
func TestDraftImpact(t *testing.T) {
	f := sharedServer(t)
	code, out, errText := sharedRun(t, f, "draft", "impact", "drf_BWSYNTH")
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errText)
	}
	wantAll(t, "draft impact", out,
		"fragment ntp shared gains in lab-a: 2 machines (mch_a and 1 more; assignment, profile)",
		"keeps in lab-a: 1 machine (mch_c; adoption)",
		"stays unchanged: prod on frv_ntp3",
		"profile idle cluster reaches no machine of lab-a")
	if got := f.took(); len(got) != 0 {
		t.Errorf("a read sent %q", got)
	}

	// A change or a scope this client does not know is shown as the server sent it: a machine is never dropped
	// from the answer, and an entry that reaches machines never reads as one that reaches none (contract 11).
	f.impact = `{"items":[{"kind":"fragment","name":"ntp","scope":"tenant","machines":[` +
		`{"machine":"mch_a","reason":"assignment","change":"moves"}],"otherClusters":[` +
		`{"cluster":"cl_other","revision":"frv_ntp3","via":"prf_x","unchanged":false}]}]}`
	code, out, errText = sharedRun(t, f, "draft", "impact", "drf_BWSYNTH")
	if code != 0 {
		t.Fatalf("unknown members: exit %d, stderr %s", code, errText)
	}
	wantAll(t, "unknown members", out, "fragment ntp tenant moves in lab-a: 1 machine (mch_a; assignment)",
		"also changes: prod on frv_ntp3 via profile prf_x")
	if strings.Contains(out, "reaches no machine") {
		t.Errorf("a machine of an unknown change reads as none:\n%s", out)
	}

	f.impact = `{"items":[]}`
	if _, out, _ = sharedRun(t, f, "draft", "impact", "drf_BWSYNTH"); !strings.Contains(out, "The draft holds no fragment or profile entry.") {
		t.Errorf("an empty draft:\n%s", out)
	}
	f.took()
}

// The adopt answer of a profile says nothing of a pin: a profile pins fragments, and no profile pins a profile.
func TestProfileAdoptSaysNothingOfAPin(t *testing.T) {
	f := sharedServer(t)
	_, out, _ := sharedRun(t, f, "profile", "adopt", "base-set", "--draft", "drf_BWSYNTH", ifMatch)
	f.took()
	if strings.Contains(out, "keeps its pin") {
		t.Errorf("profile adopt answers with a sentence about a pin:\n%s", out)
	}
	if _, help, _ := sharedRun(t, f, "profile", "adopt", "--help"); strings.Contains(help, "keeps its pin") {
		t.Errorf("profile adopt --help speaks of a pin:\n%s", help)
	}
	_, out, _ = sharedRun(t, f, "fragment", "adopt", "ntp", "--draft", "drf_BWSYNTH", ifMatch)
	f.took()
	wantAll(t, "fragment adopt", out, "A profile of this cluster that pins the name keeps its pin")
}

// A failed publication carries the refusal in its operation's error: publish words the next step of a
// shared one as the routes' own answers do, and keeps the one it had.
func TestPublishNextForSharedRefusals(t *testing.T) {
	for _, c := range []struct {
		doc  map[string]any
		want string
	}{
		{map[string]any{"type": "urn:bronzeward:problem:validation-failed", "rule": "name-in-two-scopes", "kind": "fragment", "name": "ntp"},
			". Next: this cluster holds the fragment ntp as its own and as a shared one"},
		{map[string]any{"type": "urn:bronzeward:problem:validation-failed", "rule": "library-pin-conflict", "fragment": "ntp"},
			". Next: two sources of this release pin different revisions of the fragment ntp"},
		{map[string]any{"type": "urn:bronzeward:problem:stale-input", "conflicts": []any{map[string]any{"head": "frg_ntp", "scope": "adoption", "expected": 3, "actual": 4}}},
			". Next: the shared name moved after the draft was read (frg_ntp was at adoption 3 and is at 4)"},
		{map[string]any{"type": "urn:bronzeward:problem:recovery-mode-entered"}, ". Wait for the recovery to finish, then publish again."},
		{map[string]any{"type": "urn:bronzeward:problem:validation-failed", "rule": "other"}, ""},
	} {
		if got := publishNext(c.doc); !strings.HasPrefix(got, c.want) {
			t.Errorf("%v: next step %q, want it to start %q", c.doc, got, c.want)
		}
	}
}

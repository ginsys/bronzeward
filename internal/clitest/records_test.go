package clitest

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestRecordReads runs the record reads: the machine timeline and observations, source heads and
// revisions, drafts, releases, plans, operations and acts. A draft, a machine's empty timeline,
// acts, the empty lists and every not-found come from the server. What the test server cannot
// create (source heads and revisions, releases, plans, operations, a recorded observation, an
// adoption on the timeline, a draft with entries) is answered by the injector with bodies of the
// API's shapes: it has no provider to digest an entry or publish with, or machine to observe.
func TestRecordReads(t *testing.T) {
	f := start(t, "viewer")
	s := f.seed()
	in := newInjector(t, f.base)
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", in.URL, "--certificate-authority", in.ca,
		"--automation-token-file", f.tokenFile), 0)
	before := f.acts()
	get := func(path string) []byte {
		q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1"+path, nil)
		q.Header.Set("Authorization", "Bearer "+f.token)
		return f.do(q, http.StatusOK)
	}
	same := func(r result, want []byte) {
		t.Helper()
		var a, b any
		if r.code != 0 || json.Unmarshal([]byte(r.out), &a) != nil || json.Unmarshal(want, &b) != nil || !reflect.DeepEqual(a, b) {
			t.Errorf("exit %d, -o json printed %s; want %s\nstderr: %s", r.code, r.out, want, r.err)
		}
	}

	// From the server.
	in.set(nil, nil)
	f.want(f.ctl(nil, "", "draft", "list"), 0, "DRAFT", s.draft, "worker labels")
	f.want(f.ctl(nil, "", "draft", "list", "--cluster", "cl_other"), 0, "None found.")
	same(f.ctl(nil, "", "draft", "list", "-o", "json"), get("/drafts"))
	f.want(f.ctl(nil, "", "draft", "show", s.draft), 0, "Draft "+s.draft+` "worker labels" open, revision`, "cluster "+s.cluster, "Not rendered")
	same(f.ctl(nil, "", "-o", "json", "draft", "show", s.draft), get("/drafts/"+s.draft))
	f.want(f.ctl(nil, "", "machine", "observations", s.machine), 0, "None found.", "Health is not checked")
	f.want(f.ctl(nil, "", "machine", "timeline", s.machine), 0)
	same(f.ctl(nil, "", "machine", "timeline", s.machine, "-o", "json"), get("/machines/"+s.machine+"/timeline"))
	f.want(f.ctl(nil, "", "act", "list"), 0, "ACTION", s.cluster)
	same(f.ctl(nil, "", "act", "list", "-o", "yaml", "-o", "json"), get("/acts"))
	for _, args := range [][]string{{"release", "list"}, {"plan", "list"}, {"operation", "list", "--machine", s.machine}} {
		f.want(f.ctl(nil, "", args...), 0, "None found.")
	}
	for _, args := range [][]string{{"release", "show", "rel_missing"}, {"plan", "show", "pln_missing"}, {"operation", "show", "op_missing"},
		{"fragment", "show", "frg_missing"}, {"fragment", "show", "frv_missing"}, {"profile", "history", "prf_missing"},
		{"assignment", "show", "asr_missing"}, {"machine", "timeline", "mch_missing"}} {
		f.want(f.ctl(nil, "", args...), 1, "404", "lab ("+in.URL+")", "instance")
	}

	// A list reads one page, or with --all every page; a show takes no paging flag.
	acts := listed(t, string(get("/acts")))
	if len(acts) < 2 {
		t.Fatalf("want two acts or more: %v", acts)
	}
	r := f.ctl(nil, "", "act", "list", "--limit", "1")
	f.want(r, 0, acts[0], "Next page: --cursor ")
	if strings.Contains(r.out, acts[1]) {
		t.Errorf("one page of one act shows more:\n%s", r.out)
	}
	if got := listed(t, f.ctl(nil, "", "act", "list", "--limit", "1", "--all", "-o", "json").out); !slices.Equal(got, acts) {
		t.Errorf("act list --all listed %v, want %v", got, acts)
	}
	f.want(f.ctl(nil, "", "machine", "timeline", s.machine, "--all", "--cursor", "x"), 2, "not both")
	f.want(f.ctl(nil, "", "fragment", "show", "frg_test", "--all"), 2, "history's")

	// Server-side filters go in the query; nothing else does.
	var mu sync.Mutex
	var queries []string
	in.set(func(_ http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		return false
	}, nil)
	f.want(f.ctl(nil, "", "plan", "list", "--state", "proposed", "--machine", s.machine), 0, "None found.")
	f.want(f.ctl(nil, "", "operation", "list", "--kind", "apply-config", "--plan", "pln_aaaaaaaaaaaaaaaaaaaaaaaaaa"), 0, "None found.")
	f.want(f.ctl(nil, "", "release", "list", "--draft", s.draft), 0, "None found.")
	for _, q := range []string{"/api/v1/plans?machine=" + s.machine + "&state=proposed", "/api/v1/operations?kind=apply-config&plan=pln_aaaaaaaaaaaaaaaaaaaaaaaaaa", "/api/v1/releases?"} {
		if !slices.Contains(queries, q) {
			t.Errorf("no request %s among %v", q, queries)
		}
	}

	// Usage.
	f.want(f.ctl(nil, "", "draft"), 2, "draft needs list or show")
	f.want(f.ctl(nil, "", "plan", "show"), 2, "takes 1 argument")
	f.want(f.ctl(nil, "", "act", "list", "extra"), 2, "takes 0 argument")
	f.want(f.ctl(nil, "", "release", "list", "--state", "x"), 2, "not defined")

	// From the injector: bodies of the shapes only a publication produces.
	var id struct {
		Epoch string `json:"epoch"`
	}
	json.Unmarshal(get("/identity"), &id)
	m, c := s.machine, s.cluster
	canned := map[string]string{
		"/fragments/frg_test": `{"id":"frg_test","cluster":"` + c + `","scope":"cluster","name":"worker-labels","layer":"role","revision":"frv_test","headRevision":2,"createdAt":"2026-10-22T09:00:00Z"}`,
		"/fragment-revisions/frv_test": `{"id":"frv_test","cluster":"` + c + `","name":"worker-labels","layer":"role","document":"machine:\n  nodeLabels:\n    team: storage\n    motd: \"a\tb\"\n",` +
			`"declarations":{"references":{"registry-pass":{"kind":"string","version":1}},"embedded":[]},"author":"h-author","createdAt":"2026-10-22T09:00:00Z"}`,
		"/profile-revisions/prv_test": `{"id":"prv_test","cluster":"` + c + `","name":"workers","fragments":["frv_test"],"author":"h-author","createdAt":"2026-10-22T09:01:00Z"}`,
		"/assignments/asg_test":       `{"id":"asg_test","cluster":"` + c + `","machine":"` + m + `","revision":null,"headRevision":3,"createdAt":"2026-10-22T09:02:00Z"}`,
		"/assignment-revisions/asr_test": `{"id":"asr_test","cluster":"` + c + `","machine":"` + m + `","profiles":["workers"],"fragments":{"cluster":["cluster-base"]},` +
			`"author":"h-author","createdAt":"2026-10-22T09:02:00Z"}`,
		"/releases/rel_test": `{"id":"rel_test","cluster":"` + c + `","draft":"drf_x","draftRevision":6,"operation":"op_pub","publishedBy":{"principal":"ops-ben","role":"publisher"},` +
			`"publishedAt":"2026-10-22T10:02:00Z","renderer":{"contract":"v1.13","machineryVersion":"v1.13.6","machineryChecksum":"h1:x","kubernetesVersion":"v1.34.1"},` +
			`"sources":[{"kind":"fragment","head":"frg_test","headRevision":2,"revision":"frv_test"}],` +
			`"machines":[{"machine":"` + m + `","mode":"metal","publicationObservation":"obs_a","review":"/api/v1/releases/rel_test/machines/` + m + `/review"}]}`,
		"/plans/pln_test": `{"id":"pln_test","revision":1,"state":"proposed","committedOperation":null,"approval":null,"cluster":"` + c + `","machine":"` + m + `",` +
			`"release":"rel_test","operation":"apply-config","mode":"no-reboot","assignmentRevision":"asr_test","desiredRelease":"rel_test","baselineRevision":4,` +
			`"driftRecord":null,"route":"10.55.0.3:50000","maxObservationAgeSeconds":300,"checkValiditySeconds":60,"transportDeadlineSeconds":60,` +
			`"verificationDeadlineSeconds":600,"maxAttempts":3,"rolloutLimit":1,"approvalPolicy":"one-approver","createdBy":{"principal":"ops-ben","role":"publisher"},` +
			`"epoch":"` + id.Epoch + `","timelineRevision":3,"createdAt":"2026-10-22T10:04:00Z","expiresAt":"2026-10-23T10:04:00Z","evidence":{"validation":` +
			`{"result":"passed","at":"2026-10-22T10:02:00Z","contract":"v1.13","kubernetesVersion":"v1.34.1","machineryVersion":"v1.13.6","platformMode":"metal"},` +
			`"dryRun":{"available":false,"reason":"the PoC has none"},"diff":{"from":"rel_old","to":"rel_test","withheld":false,"unified":"@@ machine.nodeLabels @@\n+  team: storage\n"}}}`,
		"/operations/op_test": `{"id":"op_test","kind":"apply-config","state":"unresolved","epoch":"` + id.Epoch + `","lastEvent":4,"subject":{"plan":"pln_test","machine":"` + m + `"},` +
			`"createdBy":null,"createdAt":"2026-10-22T10:06:00Z","result":null,"error":null,"attempts":[{"id":"att_a","number":1,"revision":1,"controller":"ctl-b",` +
			`"generation":3,"route":"10.55.0.3:50000","at":"2026-10-22T10:06:02Z","transportDeadline":"2026-10-22T10:07:02Z",` +
			`"verificationDeadline":"2026-10-22T10:16:02Z","observation":"obs_a","classification":null,"response":null,"accounting":null}]}`,
	}
	two := strings.NewReplacer(`"frv_test"`, `"frv_two0"`, `"h-author"`, `"h-a"`).Replace(canned["/fragment-revisions/frv_test"])
	canned["/fragments/frg_test/revisions"] = `{"items":[` + canned["/fragment-revisions/frv_test"] + `,` + two + `]}`
	other := strings.ReplaceAll(strings.ReplaceAll(canned["/releases/rel_test"], `"rel_test"`, `"rel_other"`), `"drf_x"`, `"drf_y"`)
	canned["/releases"] = `{"items":[` + canned["/releases/rel_test"] + `,` + other + `]}`
	canned["/releases?limit=1"] = `{"items":[` + canned["/releases/rel_test"] + `],"next":"c2"}`
	canned["/releases?cursor=c2"] = `{"items":[` + other + `]}`
	canned["/plans"] = `{"items":[` + canned["/plans/pln_test"] + `]}`
	canned["/operations"] = `{"items":[` + canned["/operations/op_test"] + `]}`
	canned["/machines/"+m+"/timeline"] = `{"items":[{"revision":1,"at":"2026-10-22T10:00:00Z","epoch":"` + id.Epoch + `","kind":"adoption",` +
		`"entry":{"approval":"apr_a","comparisons":["4.1","4.2","4.3","4.4","4.5"],"controller":"ctl-b","observation":"obs_a","operation":"op_adopt","plan":"pln_adopt"}}]}`
	canned["/drafts/drf_test"] = `{"id":"drf_test","cluster":"` + c + `","state":"open","revision":5,"title":"labels","entries":[` +
		`{"kind":"import-base","machine":"` + m + `","revision":"ibr_base"},` +
		`{"kind":"fragment","name":"labels","head":null,"base":null,"revision":"frv_new"},` +
		`{"kind":"profile","name":"workers","head":"prf_w","base":2,"revision":null},` +
		`{"kind":"assignment","machine":"` + m + `","head":"asg_a","base":3,"revision":"asr_new"}]}`
	canned["/plans/pln_adopt"] = strings.NewReplacer(`"pln_test"`, `"pln_adopt"`, `"operation":"apply-config","mode":"no-reboot"`, `"operation":"adopt","mode":null`,
		`"transportDeadlineSeconds":60`, `"transportDeadlineSeconds":null`, `"verificationDeadlineSeconds":600`, `"verificationDeadlineSeconds":null`,
		`"maxAttempts":3`, `"maxAttempts":null`).Replace(canned["/plans/pln_test"])
	canned["/machines/"+m+"/observations"] = `{"items":[{"id":"obs_b","machine":"` + m + `","basis":7,"revision":2,"purpose":"monitor","publicationGeneration":null,` +
		`"plan":null,"operation":null,"endpoint":"10.55.0.3:50000","controller":"ctl-b","startedAt":"2026-10-22T10:00:00Z","at":"2026-10-22T10:00:01Z",` +
		`"access":null,"smbiosUuid":null,"talosNodeId":null,"talosClusterId":null,"assignmentEvidence":null,"runningVersion":null,"resourceVersion":null,` +
		`"health":"none-bound","unread":{"configuration":{"code":"timeout","cause":"the read timed out"}}}]}`
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		b, ok := canned[path+"?"+r.URL.RawQuery]
		if !ok {
			b, ok = canned[path]
		}
		if ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Bronzeward-Epoch", id.Epoch)
			w.Write([]byte(b))
		}
		return ok
	}, nil)
	for _, c := range []struct {
		args  []string
		parts []string
	}{
		{[]string{"fragment", "show", "frg_test"}, []string{"Fragment frg_test", "worker-labels, layer role, scope cluster", "frv_test, head revision 2",
			"bronzewardctl fragment show frv_test", "2026-10-22 09:00:00 UTC"}},
		{[]string{"fragment", "show", "frv_test"}, []string{"References  registry-pass (string, version 1)", "\n  machine:\n", "\n      team: storage\n",
			"\n      motd: \"a\tb\"\n"}},
		{[]string{"fragment", "history", "frg_test"}, []string{"REVISION", "frv_test", "h-author", "fragment worker-labels, layer role"}},
		{[]string{"profile", "show", "prv_test"}, []string{"profile workers pins frv_test"}},
		{[]string{"assignment", "show", "asg_test"}, []string{"Assignment asg_test", "none: removed at head revision 3"}},
		{[]string{"assignment", "show", "asr_test"}, []string{"machine " + m + ", profiles: workers; cluster: cluster-base"}},
		{[]string{"release", "show", "rel_test"}, []string{"published by ops-ben (publisher) 2026-10-22 10:02:00 UTC", "drf_x revision 6, operation op_pub",
			"contract v1.13, machinery v1.13.6, Kubernetes v1.34.1", "Built from (source revisions, not configuration):", "fragment  frg_test  frv_test",
			"Rendered configuration per machine (1):", m + "  validated: metal"}},
		{[]string{"release", "list", "--draft", "drf_y"}, []string{"rel_other", "drf_y r6"}},
		{[]string{"plan", "show", "pln_test"}, []string{"Plan pln_test  proposed", "apply-config, mode no-reboot", "(machine Desired: rel_test)",
			"baseline revision 4, drift record: none", "transport 60s, verification 600s, observation age 300s, attempts 3",
			"Dry run     not available: the PoC has none", "rel_old -> rel_test", "\n    +  team: storage\n"}},
		{[]string{"plan", "show", "pln_adopt"}, []string{"adopt, mode none", "transport -, verification -, observation age 300s, attempts -"}},
		{[]string{"release", "list", "--draft", "drf_y", "--limit", "1"}, []string{"None on this page.", "Next page: --cursor c2"}},
		{[]string{"release", "list", "--draft", "drf_x", "--cursor", "c2"}, []string{"None on this page."}},
		{[]string{"operation", "show", "op_test"}, []string{"Operation op_test  apply-config  unresolved", "plan pln_test, machine " + m, "by the controller",
			"Attempt 1", "controller ctl-b, generation 3", "Unresolved: completion and retry safety are unknown"}},
		{[]string{"machine", "observations", m}, []string{"obs_b", "monitor", "configuration: the read timed out", "Health is not checked"}},
	} {
		f.want(f.ctl(nil, "", c.args...), 0, c.parts...)
	}
	if r := f.ctl(nil, "", "release", "list", "--draft", "drf_y"); strings.Contains(r.out, "rel_test") {
		t.Errorf("the draft filter kept rel_test:\n%s", r.out)
	}
	for path, args := range map[string][]string{"/plans/pln_test": {"plan", "show", "pln_test"}, "/fragment-revisions/frv_test": {"fragment", "show", "frv_test"}} {
		if r := f.ctl(nil, "", append(args, "-o", "json")...); r.code != 0 || r.out != canned[path]+"\n" {
			t.Errorf("%v -o json: exit %d\n%s\nwant the body unchanged", args, r.code, r.out)
		}
	}
	same(f.ctl(nil, "", "release", "list", "--draft", "drf_y", "-o", "json"), []byte(`{"items":[`+other+`]}`))
	same(f.ctl(nil, "", "release", "list", "--draft", "drf_y", "--limit", "1", "-o", "json"), []byte(`{"items":[],"next":"c2"}`))

	// Each row prints its fields, in order.
	for _, c := range []struct {
		args []string
		key  string
		want string
	}{
		{[]string{"draft", "show", "drf_test"}, "import-base", "import-base " + m + " ibr_base import base"},
		{[]string{"draft", "show", "drf_test"}, "frv_new", "fragment labels frv_new new"},
		{[]string{"draft", "show", "drf_test"}, "workers", "profile workers - removed"},
		{[]string{"draft", "show", "drf_test"}, "asr_new", "assignment " + m + " asr_new changed (based on head revision 3)"},
		{[]string{"machine", "timeline", m}, "adoption", `1 2026-10-22 10:00:00 UTC adoption {"approval":"apr_a","comparisons":["4.1","4.2","4.3","4.4","4.5"],` +
			`"controller":"ctl-b","observation":"obs_a","operation":"op_adopt","plan":"pln_adopt"}`},
		{[]string{"plan", "list"}, "pln_test", "pln_test proposed " + m + " apply-config rel_test 2026-10-23 10:04:00 UTC"},
		{[]string{"operation", "list"}, "op_test", "op_test apply-config unresolved plan pln_test, machine " + m + " 2026-10-22 10:06:00 UTC"},
	} {
		r := f.ctl(nil, "", c.args...)
		i := slices.IndexFunc(strings.Split(r.out, "\n"), func(l string) bool { return strings.Contains(l, c.key) })
		if r.code != 0 || i < 0 || strings.Join(strings.Fields(strings.Split(r.out, "\n")[i]), " ") != c.want {
			t.Errorf("%v: exit %d, want a row %q:\n%s%s", c.args, r.code, c.want, r.out, r.err)
		}
	}

	// History is one table: each revision's columns line up under the header.
	r = f.ctl(nil, "", "fragment", "history", "frg_test")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 3 {
		t.Fatalf("fragment history printed %d lines, want a header and 2 revisions:\n%s", len(lines), r.out)
	}
	for _, l := range lines[1:] {
		if strings.Index(l, "2026-") != strings.Index(lines[0], "CREATED") || strings.Index(l, "fragment ") != strings.Index(lines[0], "CONTENT") {
			t.Errorf("fragment history does not line up:\n%s", r.out)
		}
	}

	if after := f.acts(); after != before {
		t.Errorf("acts changed around the read commands:\nbefore %s\nafter  %s", before, after)
	}
}

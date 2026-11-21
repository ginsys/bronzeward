package main

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Titles of plans, releases and operations (client contracts A8, ginsys/bronzeward#987): a table shows the
// title beside the identifier, which stays in its own column; a detail adds a Title line; each distinct
// draft, release, plan, ingestion and machine is read once for the command; a failed read leaves the
// identifier alone; -o json and -o yaml read nothing more and print the resource unchanged.

const (
	titleDraft   = "Add the office labels"
	titleRelease = titleDraft + ", published "
)

var titleAt = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func titleRel(draft string) string {
	return `{"id":"rel_BWSYNTH","cluster":"cl_BWSYNTH","draft":"` + draft + `","draftRevision":3,"operation":"op_pub",` +
		`"publishedBy":{"principal":"svc_BWSYNTH","role":"publisher"},"publishedAt":"2026-11-19T10:00:00Z",` +
		`"renderer":{"contract":"v1.14","machineryVersion":"v1.14.5","kubernetesVersion":"v1.34.1"},"sources":[],"machines":[]}`
}

func titleOp(id, kind, subject string) string {
	return `{"id":"` + id + `","kind":"` + kind + `","state":"succeeded","epoch":"ep_BWSYNTH-1","lastEvent":2,"subject":` + subject +
		`,"createdBy":null,"createdAt":"2026-11-19T10:00:00Z","result":null,"error":null}`
}

func titlePlan(op string) string {
	f := &planFake{state: "proposed", op: op, evidence: diffEvidence, desired: "rel_BWSYNTH", assignment: "asr_BWSYNTH-1"}
	if op == "adopt" {
		f.evidence = adoptEvidence
	}
	return f.plan()
}

func titleItems(items ...string) string { return `{"items":[` + strings.Join(items, ",") + `]}` }

// titleServer serves what every case reads; a case overrides or removes an answer.
func titleServer(t *testing.T, edit func(map[string]string)) func() []string {
	t.Helper()
	a := map[string]string{
		"GET /api/v1/plans":                 titleItems(titlePlan("apply-config")),
		"GET /api/v1/plans/pln_BWSYNTH":     titlePlan("apply-config"),
		"GET /api/v1/releases":              titleItems(titleRel("drf_a")),
		"GET /api/v1/releases/rel_BWSYNTH":  titleRel("drf_a"),
		"GET /api/v1/drafts/drf_a":          `{"id":"drf_a","title":"` + titleDraft + `"}`,
		"GET /api/v1/machines/mch_a":        machineBody("mch_a", `"name":"web-1",`),
		"GET /api/v1/operations":            titleItems(titleOp("op_pub", "publish", `{"draft":"drf_a","draftRevision":3}`)),
		"GET /api/v1/operations/op_pub":     titleOp("op_pub", "publish", `{"draft":"drf_a","draftRevision":3}`),
		"GET /api/v1/ingestions/ing_a":      `{"id":"ing_a","draft":"drf_a","machine":"mch_a"}`,
		"GET /api/v1/ingestions/ing_nomach": `{"id":"ing_nomach","draft":"drf_a","machine":null}`,
	}
	if edit != nil {
		edit(a)
	}
	return recoveryServer(t, a, "")
}

func reads(seen []string, path string) int {
	n := 0
	for _, s := range seen {
		if s == "GET /api/v1"+path {
			n++
		}
	}
	return n
}

func TestTitlesRelease(t *testing.T) {
	seen := titleServer(t, func(a map[string]string) {
		a["GET /api/v1/releases"] = titleItems(titleRel("drf_a"), strings.Replace(titleRel("drf_a"), "rel_BWSYNTH", "rel_two", 1))
	})
	code, out, errOut := runCtl(t, "release", "list")
	if code != 0 || strings.Count(out, titleRelease+when(titleAt)) != 2 || !strings.Contains(out, "rel_BWSYNTH") || !strings.Contains(out, "rel_two") {
		t.Fatalf("release list: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	if n := reads(seen(), "/drafts/drf_a"); n != 1 {
		t.Errorf("release list read the shared draft %d times; want once", n)
	}
	code, out, errOut = runCtl(t, "release", "show", "rel_BWSYNTH")
	if code != 0 || !strings.Contains(out, "Release rel_BWSYNTH") || !strings.Contains(out, "Title") || !strings.Contains(out, titleRelease+when(titleAt)) {
		t.Errorf("release show: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

func TestTitlesPlan(t *testing.T) {
	seen := titleServer(t, nil)
	want := "Apply of mch_a (web-1) to " + titleRelease + when(titleAt)
	code, out, errOut := runCtl(t, "plan", "list")
	if code != 0 || !strings.Contains(out, want) || !strings.Contains(out, "pln_BWSYNTH") {
		t.Fatalf("plan list: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	for _, p := range []string{"/releases/rel_BWSYNTH", "/drafts/drf_a", "/machines/mch_a"} {
		if n := reads(seen(), p); n != 1 {
			t.Errorf("plan list read %s %d times; want once", p, n)
		}
	}
	code, out, errOut = runCtl(t, "plan", "show", "pln_BWSYNTH")
	if code != 0 || !strings.Contains(out, "Plan pln_BWSYNTH  proposed") || !strings.Contains(out, "Title") || !strings.Contains(out, want) {
		t.Fatalf("plan show: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	// The plan page's own machine read serves the title: one read of the machine for the whole command.
	if n := reads(seen(), "/machines/mch_a"); n != 2 {
		t.Errorf("machine reads over plan list and plan show = %d; want 1 for each command", n)
	}
}

func TestTitlesAdoptPlan(t *testing.T) {
	titleServer(t, func(a map[string]string) { a["GET /api/v1/plans/pln_BWSYNTH"] = titlePlan("adopt") })
	code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
	if want := "Adopt of mch_a (web-1) to " + titleRelease; code != 0 || !strings.Contains(out, want) {
		t.Errorf("plan show: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

func TestTitlesOperation(t *testing.T) {
	op := func(id, kind, subject string) string { return titleOp(id, kind, subject) }
	planTitle := "Apply of mch_a (web-1) to " + titleRelease + when(titleAt)
	for _, c := range []struct {
		name, kind, subject, want string
		reads                     map[string]int
	}{
		{"publish", "publish", `{"draft":"drf_a","draftRevision":3}`, "Publish of " + titleDraft, map[string]int{"/drafts/drf_a": 1}},
		{"ingest", "ingest", `{"draft":"drf_a","draftRevision":1,"ingestion":"ing_a"}`, "Import of mch_a (web-1)", map[string]int{"/ingestions/ing_a": 1, "/machines/mch_a": 1}},
		{"ingest with no machine", "ingest", `{"draft":"drf_a","draftRevision":1,"ingestion":"ing_nomach"}`, "Import", map[string]int{"/ingestions/ing_nomach": 1}},
		{"ingest naming no ingestion", "ingest", `{"draft":"drf_a","draftRevision":1}`, "Import", map[string]int{"/ingestions/ing_a": 0, "/machines/mch_a": 0}},
		{"apply-config", "apply-config", `{"plan":"pln_BWSYNTH","machine":"mch_a"}`, planTitle, map[string]int{"/plans/pln_BWSYNTH": 1, "/releases/rel_BWSYNTH": 1, "/drafts/drf_a": 1, "/machines/mch_a": 1}},
	} {
		t.Run(c.name, func(t *testing.T) {
			seen := titleServer(t, func(a map[string]string) {
				a["GET /api/v1/operations"] = titleItems(op("op_x", c.kind, c.subject))
				a["GET /api/v1/operations/op_x"] = op("op_x", c.kind, c.subject)
			})
			for _, args := range [][]string{{"operation", "list"}, {"operation", "show", "op_x"}} {
				code, out, errOut := runCtl(t, args...)
				if code != 0 || !strings.Contains(out, c.want) || !strings.Contains(out, "op_x") {
					t.Fatalf("%v: exit %d, out\n%s\nerr\n%s", args, code, out, errOut)
				}
				all := seen()
				for p, n := range c.reads {
					if got := reads(all, p); got != n {
						t.Errorf("%v read %s %d times; want %d", args, p, got, n)
					}
				}
				seen = titleServerSeenReset(seen)
			}
		})
	}
}

// titleServerSeenReset keeps one server's reads across the two commands of a case: the counts are
// per command, so each command is read from the requests it added.
func titleServerSeenReset(seen func() []string) func() []string {
	before := len(seen())
	return func() []string { return seen()[before:] }
}

// A draft that cannot be read leaves no title and keeps the row and the identifier: a dash in the list,
// no Title line in the show, and the rest of the output as it is.
func TestTitlesFailedDraftKeepsTheIdentifier(t *testing.T) {
	titleServer(t, func(a map[string]string) { delete(a, "GET /api/v1/drafts/drf_a") })
	for _, c := range []struct {
		args []string
		keep []string
	}{
		{[]string{"release", "list"}, []string{`rel_BWSYNTH +- `}},
		{[]string{"release", "show", "rel_BWSYNTH"}, []string{"Release rel_BWSYNTH", "Draft"}},
		{[]string{"plan", "list"}, []string{`pln_BWSYNTH +- `}},
		{[]string{"plan", "show", "pln_BWSYNTH"}, []string{"Plan pln_BWSYNTH  proposed", "Machine"}},
		{[]string{"operation", "list"}, []string{`op_pub +- `}},
	} {
		code, out, errOut := runCtl(t, c.args...)
		if code != 0 || strings.Contains(out, titleDraft) || strings.Contains(out, "Title") && c.args[1] == "show" || errOut != "" {
			t.Errorf("%v: exit %d, out\n%s\nerr\n%s", c.args, code, out, errOut)
		}
		for _, k := range c.keep {
			if !regexp.MustCompile(k).MatchString(out) {
				t.Errorf("%v: out lacks %q\n%s", c.args, k, out)
			}
		}
	}
}

// A machine that cannot be read is named by its identifier in a title, once, in the plan list and in the
// plan show, whose own read of it is the one that failed; an ingestion that cannot be read is an import
// with no machine to name.
func TestTitlesFailedMachineAndIngestion(t *testing.T) {
	seen := titleServer(t, func(a map[string]string) {
		delete(a, "GET /api/v1/machines/mch_a")
		a["GET /api/v1/operations"] = titleItems(titleOp("op_x", "ingest", `{"draft":"drf_a","draftRevision":1,"ingestion":"ing_gone"}`))
	})
	want := "Apply of mch_a to " + titleRelease + when(titleAt)
	for _, args := range [][]string{{"plan", "list"}, {"plan", "show", "pln_BWSYNTH"}} {
		code, out, errOut := runCtl(t, args...)
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%v: exit %d, out\n%s\nerr\n%s", args, code, out, errOut)
		}
	}
	if n := reads(seen(), "/machines/mch_a"); n != 2 {
		t.Errorf("a failed machine was read %d times over plan list and plan show; want once for each", n)
	}
	code, out, errOut := runCtl(t, "operation", "list")
	if code != 0 || !regexp.MustCompile(`op_x +Import +ingest`).MatchString(out) {
		t.Errorf("operation list: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

func TestTitlesRemoteTextIsEscaped(t *testing.T) {
	titleServer(t, func(a map[string]string) { a["GET /api/v1/drafts/drf_a"] = `{"id":"drf_a","title":"x\u001b[31mred"}` })
	code, out, errOut := runCtl(t, "release", "list")
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, `x\x1b[31mred`) {
		t.Errorf("exit %d, out %q\nerr\n%s", code, out, errOut)
	}
}

func TestTitlesJSONAndYAMLReadNothingMore(t *testing.T) {
	var served map[string]string
	seen := titleServer(t, func(a map[string]string) { served = a })
	for _, c := range []struct {
		args []string
		key  string // the served answer the output must be
		id   string
	}{
		{[]string{"release", "list"}, "GET /api/v1/releases", "rel_BWSYNTH"},
		{[]string{"plan", "list"}, "GET /api/v1/plans", "pln_BWSYNTH"},
		{[]string{"operation", "list"}, "GET /api/v1/operations", "op_pub"},
		{[]string{"release", "show", "rel_BWSYNTH"}, "GET /api/v1/releases/rel_BWSYNTH", "rel_BWSYNTH"},
		{[]string{"operation", "show", "op_pub"}, "GET /api/v1/operations/op_pub", "op_pub"},
	} {
		code, out, errOut := runCtl(t, append(c.args, "-o", "json")...)
		var got, want any
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil || json.Unmarshal([]byte(served[c.key]), &want) != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%v -o json: exit %d, out\n%s\nwant the served resource\n%s\nerr\n%s", c.args, code, out, served[c.key], errOut)
		}
		code, out, errOut = runCtl(t, append(c.args, "-o", "yaml")...)
		if code != 0 || !strings.Contains(out, "id: "+c.id) || strings.Contains(out, titleDraft) || strings.Contains(out, "Title") || errOut != "" {
			t.Errorf("%v -o yaml: exit %d, out\n%s\nerr\n%s", c.args, code, out, errOut)
		}
	}
	for _, p := range []string{"/drafts/drf_a", "/machines/mch_a", "/ingestions/ing_a"} {
		if n := reads(seen(), p); n != 0 {
			t.Errorf("-o json and yaml read %s %d times; want none", p, n)
		}
	}
}

// Cross-references (ginsys/bronzeward#1017): where one command names another plan, release, operation, draft or
// import, the identifier is followed by that object's title through the same cache, so each distinct
// object is read once for the command; a failed read leaves the identifier alone.

// flat collapses the padding of a table's columns, so a case reads one space where the table pads.
func flat(out string) string { return regexp.MustCompile(" +").ReplaceAllString(out, " ") }

func refOf(id, title string) string { return id + " (" + title + ")" }

var (
	refRelease = refOf("rel_BWSYNTH", titleRelease+when(titleAt))
	refOldRel  = refOf("rel_BWSYNTH-old", "Earlier labels, published "+when(titleAt))
	refPlan    = "Apply of mch_a (web-1) to " + titleRelease + when(titleAt)
)

// refServer is titleServer with the objects the cross-references name: an earlier release, a committed
// operation, an import and its operation, an approval and a page of acts.
func refServer(t *testing.T, edit func(map[string]string)) func() []string {
	t.Helper()
	return titleServer(t, func(a map[string]string) {
		a["GET /api/v1/releases/rel_BWSYNTH-old"] = strings.Replace(titleRel("drf_b"), "rel_BWSYNTH", "rel_BWSYNTH-old", 1)
		a["GET /api/v1/drafts/drf_b"] = `{"id":"drf_b","title":"Earlier labels"}`
		a["GET /api/v1/operations/op_BWSYNTH-apply"] = titleOp("op_BWSYNTH-apply", "apply-config", `{"plan":"pln_BWSYNTH"}`)
		a["GET /api/v1/operations/op_imp"] = titleOp("op_imp", "ingest", `{"ingestion":"ing_a"}`)
		ing := `{"id":"ing_a","kind":"import","mode":"transient","state":"held","machine":"mch_a","draft":"drf_a","operation":"op_imp",` +
			`"ownerGeneration":1,"expiresAt":"2026-11-19T11:00:00Z"}`
		a["GET /api/v1/ingestions/ing_a"] = ing
		a["GET /api/v1/ingestions"] = titleItems(ing)
		a["GET /api/v1/approvals/apr_a"] = `{"id":"apr_a","plan":"pln_BWSYNTH","machine":"mch_a","approver":{"principal":"h-approver","role":"approver"},` +
			`"selfApproval":{"reasons":[]},"unseenOverwrite":false,"epoch":"ep_BWSYNTH-1","at":"2026-11-19T10:00:00Z"}`
		act := func(id string, subjects ...string) string {
			s, _ := json.Marshal(subjects)
			return `{"id":"` + id + `","at":"2026-11-19T10:00:00Z","epoch":"ep_BWSYNTH-1","action":"plan.create","principal":"h-a","principalKind":"human",` +
				`"role":"approver","via":"cli","subjects":` + string(s) + `}`
		}
		a["GET /api/v1/acts"] = titleItems(act("act_1", "pln_BWSYNTH", "rel_BWSYNTH", "drf_a"), act("act_2", "rel_BWSYNTH", "mch_a", "op_pub", "ing_a", "prf_x"))
		if edit != nil {
			edit(a)
		}
	})
}

func TestTitlesRefsPlan(t *testing.T) {
	seen := refServer(t, func(a map[string]string) {
		a["GET /api/v1/plans/pln_BWSYNTH"] = (&planFake{state: "committed", op: "apply-config", evidence: diffEvidence, desired: "rel_BWSYNTH", assignment: "asr_BWSYNTH-1"}).plan()
	})
	code, out, errOut := runCtl(t, "plan", "list")
	if code != 0 || !strings.Contains(out, refRelease) {
		t.Fatalf("plan list: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	for _, p := range []string{"/releases/rel_BWSYNTH", "/drafts/drf_a"} {
		if n := reads(seen(), p); n != 1 {
			t.Errorf("plan list read %s %d times; want once", p, n)
		}
	}
	code, out, errOut = runCtl(t, "plan", "show", "pln_BWSYNTH")
	out = flat(out)
	committed := refOf("op_BWSYNTH-apply", refPlan)
	for _, w := range []string{"Release " + refRelease, "committed operation " + committed, committed + ". Follow it with: bronzewardctl operation watch 'op_BWSYNTH-apply'",
		"Changes shown below: the whole redacted configuration, " + refOldRel + " -> " + refRelease} {
		if code != 0 || !strings.Contains(out, w) {
			t.Errorf("plan show lacks %q: exit %d, out\n%s\nerr\n%s", w, code, out, errOut)
		}
	}
	for _, p := range []string{"/releases/rel_BWSYNTH-old", "/drafts/drf_b", "/operations/op_BWSYNTH-apply"} {
		if n := reads(seen(), p); n != 1 {
			t.Errorf("plan show read %s %d times; want once", p, n)
		}
	}
}

func TestTitlesRefsRelease(t *testing.T) {
	seen := refServer(t, nil)
	code, out, errOut := runCtl(t, "release", "list")
	if code != 0 || !strings.Contains(out, refOf("drf_a", titleDraft)+" r3") {
		t.Fatalf("release list: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	if n := reads(seen(), "/drafts/drf_a"); n != 1 {
		t.Errorf("release list read the draft %d times; want once", n)
	}
	code, out, errOut = runCtl(t, "release", "show", "rel_BWSYNTH")
	out = flat(out)
	if want := "Draft " + refOf("drf_a", titleDraft) + " revision 3, operation " + refOf("op_pub", "Publish of "+titleDraft); code != 0 || !strings.Contains(out, want) {
		t.Errorf("release show lacks %q: exit %d, out\n%s\nerr\n%s", want, code, out, errOut)
	}
	if n := reads(seen(), "/operations/op_pub"); n != 1 {
		t.Errorf("release show read the publication %d times; want once", n)
	}
}

func TestTitlesRefsImport(t *testing.T) {
	seen := refServer(t, nil)
	draft, op := refOf("drf_a", titleDraft), refOf("op_imp", "Import of mch_a (web-1)")
	code, out, errOut := runCtl(t, "import", "status")
	if code != 0 || !strings.Contains(out, draft) || !strings.Contains(out, op) {
		t.Fatalf("import list: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	if n := reads(seen(), "/ingestions/ing_a"); n != 0 {
		t.Errorf("import list read an ingestion it holds %d times; want none", n)
	}
	code, out, errOut = runCtl(t, "import", "status", "ing_a")
	if want := "draft " + draft + ", operation " + op; code != 0 || !strings.Contains(out, want) {
		t.Fatalf("import status lacks %q: exit %d, out\n%s\nerr\n%s", want, code, out, errOut)
	}
	if n := reads(seen(), "/ingestions/ing_a"); n != 1 {
		t.Errorf("import status read its own ingestion %d times; want once", n)
	}
	if n := reads(seen(), "/drafts/drf_a"); n != 2 {
		t.Errorf("draft reads over import list and status = %d; want one for each command", n)
	}
}

func TestTitlesRefsApproval(t *testing.T) {
	seen := refServer(t, nil)
	code, out, errOut := runCtl(t, "approval", "show", "apr_a")
	if want := "Approval apr_a of plan " + refOf("pln_BWSYNTH", refPlan) + ", epoch"; code != 0 || !strings.Contains(out, want) {
		t.Errorf("approval show lacks %q: exit %d, out\n%s\nerr\n%s", want, code, out, errOut)
	}
	for _, p := range []string{"/plans/pln_BWSYNTH", "/releases/rel_BWSYNTH", "/drafts/drf_a", "/machines/mch_a"} {
		if n := reads(seen(), p); n != 1 {
			t.Errorf("approval show read %s %d times; want once", p, n)
		}
	}
}

func TestTitlesRefsActSubjects(t *testing.T) {
	seen := refServer(t, nil)
	code, out, errOut := runCtl(t, "act", "list")
	for _, w := range []string{refOf("pln_BWSYNTH", refPlan), refRelease, refOf("drf_a", titleDraft), refOf("mch_a", "web-1"),
		refOf("op_pub", "Publish of "+titleDraft), refOf("ing_a", "Import of mch_a (web-1)"), "ing_a (Import of mch_a (web-1))" + ", prf_x"} {
		if code != 0 || !strings.Contains(out, w) {
			t.Errorf("act list lacks %q: exit %d, out\n%s\nerr\n%s", w, code, out, errOut)
		}
	}
	for _, p := range []string{"/releases/rel_BWSYNTH", "/drafts/drf_a", "/machines/mch_a", "/operations/op_pub", "/ingestions/ing_a"} {
		if n := reads(seen(), p); n != 1 {
			t.Errorf("act list read %s %d times; want once for the page", p, n)
		}
	}
}

// The prefix of an identifier names its kind (persistence-api.md section 2); the CLI keeps its own map of
// the six kinds that have a title, and any other prefix stays the bare identifier.
func TestTitlesKindOfID(t *testing.T) {
	doc, err := os.ReadFile("../../docs/spec/persistence-api.md")
	if err != nil {
		t.Fatal(err)
	}
	for prefix, c := range map[string]struct{ kind, entity string }{
		"rel": {"release", "release"}, "pln": {"plan", "plan"}, "op": {"operation", "operation"},
		"drf": {"draft", "draft"}, "ing": {"ingestion", "ingestion claim"}, "mch": {"machine", "machine"},
	} {
		if !regexp.MustCompile("`" + prefix + "` \\| " + c.entity).Match(doc) {
			t.Errorf("persistence-api.md section 2 does not map `%s` to %s", prefix, c.entity)
		}
		if got := kindOfID(prefix + "_abc"); got != c.kind {
			t.Errorf("kindOfID(%s_abc) = %q; want %q", prefix, got, c.kind)
		}
	}
	for _, id := range []string{"prf_abc", "apr_abc", "relx_abc", "rel", "", "_abc", "op-pub"} {
		if got := kindOfID(id); got != "" {
			t.Errorf("kindOfID(%q) = %q; want none", id, got)
		}
	}
}

func TestTitlesRefsKeepTheIdentifierWhenAReadFails(t *testing.T) {
	refServer(t, func(a map[string]string) {
		delete(a, "GET /api/v1/drafts/drf_a")
		delete(a, "GET /api/v1/operations/op_pub")
	})
	code, out, errOut := runCtl(t, "release", "show", "rel_BWSYNTH")
	out = flat(out)
	if want := "Draft drf_a revision 3, operation op_pub\n"; code != 0 || !strings.Contains(out, want) {
		t.Errorf("release show lacks %q: exit %d, out\n%s\nerr\n%s", want, code, out, errOut)
	}
}

func TestTitlesRefsRemoteTextIsEscaped(t *testing.T) {
	refServer(t, func(a map[string]string) { a["GET /api/v1/drafts/drf_a"] = `{"id":"drf_a","title":"x\u001b[31mred"}` })
	code, out, errOut := runCtl(t, "import", "status", "ing_a")
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, `drf_a (x\x1b[31mred)`) {
		t.Errorf("import status: exit %d, out %q\nerr\n%s", code, out, errOut)
	}
}

func TestTitlesRefsJSONAndYAMLReadNothingMore(t *testing.T) {
	seen := refServer(t, nil)
	for _, args := range [][]string{{"act", "list"}, {"import", "status"}, {"import", "status", "ing_a"}, {"approval", "show", "apr_a"}, {"plan", "show", "pln_BWSYNTH"}} {
		for _, o := range []string{"json", "yaml"} {
			if code, out, errOut := runCtl(t, append(args, "-o", o)...); code != 0 || strings.Contains(out, "Earlier") || strings.Contains(out, titleDraft) {
				t.Errorf("%v -o %s: exit %d, out\n%s\nerr\n%s", args, o, code, out, errOut)
			}
		}
	}
	for _, p := range []string{"/releases/rel_BWSYNTH", "/releases/rel_BWSYNTH-old", "/drafts/drf_a", "/drafts/drf_b", "/operations/op_imp", "/operations/op_pub", "/machines/mch_a"} {
		if n := reads(seen(), p); n != 0 {
			t.Errorf("-o json and yaml read %s %d times; want none", p, n)
		}
	}
}

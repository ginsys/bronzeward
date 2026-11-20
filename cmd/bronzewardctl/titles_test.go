package main

import (
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

func TestTitlesFailedReadKeepsTheIdentifier(t *testing.T) {
	titleServer(t, func(a map[string]string) { delete(a, "GET /api/v1/drafts/drf_a") })
	for _, args := range [][]string{{"release", "list"}, {"release", "show", "rel_BWSYNTH"}, {"plan", "list"}, {"plan", "show", "pln_BWSYNTH"}, {"operation", "list"}} {
		code, out, errOut := runCtl(t, args...)
		if code != 0 || strings.Contains(out, titleRelease) || strings.Contains(out, "Title") && args[1] == "show" {
			t.Errorf("%v: exit %d, out\n%s\nerr\n%s", args, code, out, errOut)
		}
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
	seen := titleServer(t, nil)
	for _, o := range []string{"json", "yaml"} {
		for _, args := range [][]string{{"release", "list"}, {"plan", "list"}, {"operation", "list"}, {"release", "show", "rel_BWSYNTH"}, {"operation", "show", "op_pub"}} {
			code, out, errOut := runCtl(t, append(args, "-o", o)...)
			if code != 0 || strings.Contains(out, titleDraft) {
				t.Errorf("%v -o %s: exit %d, out\n%s\nerr\n%s", args, o, code, out, errOut)
			}
		}
	}
	for _, p := range []string{"/drafts/drf_a", "/machines/mch_a", "/ingestions/ing_a"} {
		if n := reads(seen(), p); n != 0 {
			t.Errorf("-o json and yaml read %s %d times; want none", p, n)
		}
	}
}

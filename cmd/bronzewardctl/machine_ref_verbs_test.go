package main

import (
	"strings"
	"testing"
)

// Every other verb that takes a machine takes its name too (ginsys/bronzeward#1055, part 9b, pull request 3): the
// resolver of pull request 2 (machineByName) reads the machine list once for a name, and an identifier never lists.
// A verb given a name says, in its result line, which machine the act landed on, as `<identifier> (<name>)`.

// namedVerbs is every verb of this pull request with a machine by name, the request it must send for the machine
// mch_b (named db-1) and the answer it needs. {m} stands for the machine as the person typed it.
var namedVerbs = []struct {
	name  string
	args  []string
	sent  string // a method and path recoveryServer saw, or "" when the query decides
	query string // a method, path and query it saw, or ""
	post  string
}{
	{"machine timeline", []string{"machine", "timeline", "{m}"}, "GET /api/v1/machines/mch_b/timeline", "", ""},
	{"machine observations", []string{"machine", "observations", "{m}"}, "GET /api/v1/machines/mch_b/observations", "", ""},
	{"drift show", []string{"drift", "show", "{m}"}, "GET /api/v1/machines/mch_b", "", ""},
	{"drift revert", []string{"drift", "revert", "{m}"}, "GET /api/v1/machines/mch_b", "", ""},
	{"drift adopt", []string{"drift", "adopt", "{m}", "--draft", "drf_a"}, "GET /api/v1/identity", "", ""},
	{"machine set-endpoint", []string{"machine", "set-endpoint", "{m}", "--talos-endpoint", "10.0.0.9:50000"}, "POST /api/v1/machines/mch_b/talos-endpoints", "", `{}`},
	{"assignment set", []string{"assignment", "set", "{m}", "--draft", "drf_a", "--profiles", "p1", "--if-match", `"r1"`}, "PUT /api/v1/drafts/drf_a/assignments/mch_b", "", ""},
	{"assignment rm", []string{"assignment", "rm", "{m}", "--draft", "drf_a", "--if-match", `"r1"`}, "DELETE /api/v1/drafts/drf_a/assignments/mch_b", "", ""},
	{"import start", []string{"import", "start", "--machine", "{m}", "--draft", "drf_a", "--no-wait", "--if-match", `"r1"`}, "POST /api/v1/ingestions", "", `{"ingestion":"ing_1","operation":"op_1"}`},
	{"import status --machine", []string{"import", "status", "--machine", "{m}"}, "", "GET /api/v1/ingestions?machine=mch_b", ""},
	{"plan list --machine", []string{"plan", "list", "--machine", "{m}"}, "", "GET /api/v1/plans?machine=mch_b", ""},
	{"operation list --machine", []string{"operation", "list", "--machine", "{m}"}, "", "GET /api/v1/operations?machine=mch_b", ""},
	{"release review", []string{"release", "review", "rel_a", "{m}"}, "GET /api/v1/releases/rel_a/machines/mch_b/review", "", ""},
	{"release provenance", []string{"release", "provenance", "rel_a", "{m}"}, "GET /api/v1/releases/rel_a/machines/mch_b/review", "", ""},
	{"recovery mark", []string{"recovery", "mark", "{m}"}, "POST /api/v1/recovery/scopes/mch_b/marks", "", `{}`},
	{"recovery release", []string{"recovery", "release", "{m}"}, "POST /api/v1/recovery/scopes/mch_b/releases", "", `{}`},
}

func namedAnswers() map[string]string {
	a := refAnswers()
	a["GET /api/v1/machines/mch_b/timeline"] = `{"items":[]}`
	return a
}

func withMachine(args []string, m string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.Replace(a, "{m}", m, 1)
	}
	return out
}

func TestEveryMachineVerbTakesAName(t *testing.T) {
	for _, c := range namedVerbs {
		t.Run(c.name+" by name", func(t *testing.T) {
			setServerFacts(t, 10, nil)
			resetPosted()
			seen := recoveryServer(t, namedAnswers(), c.post)
			code, out, errOut := runDrift(t, withMachine(c.args, "db-1")...)
			if sentCount(seen(), "GET /api/v1/machines") != 1 {
				t.Errorf("%v: the machine list was read %d times, want once; sent %v", c.args, sentCount(seen(), "GET /api/v1/machines"), seen())
			}
			for _, s := range seen() {
				if strings.Contains(s, "db-1") {
					t.Errorf("%v: sent %q, the name where the identifier belongs; exit %d, stdout %q, stderr %q", c.args, s, code, out, errOut)
				}
			}
			switch {
			case c.sent != "" && sentCount(seen(), c.sent) == 0:
				t.Errorf("%v: sent %v, want %s; exit %d, stderr %q", c.args, seen(), c.sent, code, errOut)
			case c.query != "" && sentCount(uris(), c.query) == 0:
				t.Errorf("%v: sent %v, want %s", c.args, uris(), c.query)
			}
		})
		t.Run(c.name+" by identifier never lists", func(t *testing.T) {
			setServerFacts(t, 10, nil)
			resetPosted()
			seen := recoveryServer(t, namedAnswers(), c.post)
			runDrift(t, withMachine(c.args, "mch_b")...)
			if sentCount(seen(), "GET /api/v1/machines") != 0 {
				t.Errorf("%v: with an identifier the machine list was read; sent %v", c.args, seen())
			}
		})
	}
}

// A name two clusters hold sends nothing, for a verb of this pull request as for pull request 2's.
func TestMachineVerbRefusesAnAmbiguousName(t *testing.T) {
	for _, args := range [][]string{
		{"machine", "timeline", "web-1"}, {"machine", "set-endpoint", "web-1", "--talos-endpoint", "e"},
		{"recovery", "mark", "web-1"}, {"release", "review", "rel_a", "web-1"}, {"plan", "list", "--machine", "web-1"},
	} {
		setServerFacts(t, 10, nil)
		seen := recoveryServer(t, namedAnswers(), `{}`)
		code, _, errOut := runDrift(t, args...)
		if code == 0 || !strings.Contains(errOut, "web-1 names 2 machines: mch_a in cluster alpha, mch_c in cluster beta") {
			t.Errorf("%v: exit %d, stderr %q; want the refusal naming both machines", args, code, errOut)
		}
		for _, s := range seen() {
			if strings.Contains(s, "mch_a") || strings.Contains(s, "mch_c") || strings.Contains(s, "web-1") || strings.HasPrefix(s, "POST") {
				t.Errorf("%v: sent %q after the refusal", args, s)
			}
		}
	}
}

// --cluster narrows a name. With an identifier it would do nothing, so it is a usage error (exit 2) that sends no read of the
// machine and no write, in every verb that takes the flag (an advisory finding on ginsys/bronzeward#1125). The
// command's own reads of /server and /identity may come first.
func TestClusterWithAnIdentifierIsRefused(t *testing.T) {
	for _, args := range [][]string{
		{"machine", "freeze", "mch_b", "--reason", "r", "--cluster", "alpha"}, {"machine", "rename", "mch_b", "--name", "n", "--cluster", "alpha"},
		{"machine", "timeline", "mch_b", "--cluster", "alpha"}, {"drift", "show", "mch_b", "--cluster", "alpha"},
		{"plan", "list", "--machine", "mch_b", "--cluster", "alpha"}, {"recovery", "mark", "mch_b", "--cluster", "alpha"},
	} {
		setServerFacts(t, 10, nil)
		seen := recoveryServer(t, namedAnswers(), `{}`)
		code, _, errOut := runDrift(t, args...)
		if code != 2 || !strings.Contains(errOut, "--cluster narrows a name; an identifier needs none") {
			t.Errorf("%v: exit %d, stderr %q; want the usage error", args, code, errOut)
		}
		for _, s := range seen() {
			if strings.Contains(s, "mch_b") {
				t.Errorf("%v: sent %q after the refusal", args, s)
			}
		}
	}
}

// A verb given a name says which machine the act landed on, `<identifier> (<name>)`, in the line a person reads
// afterwards; given an identifier it prints the identifier alone.
func TestResultLineNamesTheMachine(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		post string
		want string // with {m} the machine as typed; the identifier form drops the name and its parentheses
	}{
		{"freeze", []string{"machine", "freeze", "{m}", "--reason", "r"}, strings.Replace(strings.Replace(freezeAnswer, "%s", "true", 1), "mch_a", "mch_b", 1), "machine {l}, timeline revision"},
		{"unfreeze", []string{"machine", "unfreeze", "{m}", "--reason", "r"}, strings.Replace(strings.Replace(freezeAnswer, "%s", "false", 1), "mch_a", "mch_b", 1), "machine {l}, timeline revision"},
		{"rename", []string{"machine", "rename", "{m}", "--name", "db-2"}, `{"machine":"mch_b","name":"db-2","previousName":"db-1","revision":5}`, "Machine {l}: name db-2"},
		{"set-endpoint", []string{"machine", "set-endpoint", "{m}", "--talos-endpoint", "10.0.0.9:50000"}, `{"machine":"mch_b","talosEndpoint":"10.0.0.9:50000","previousEndpoint":"10.0.0.8:50000","revision":6}`, "Machine {l}: Talos endpoint"},
		{"recovery mark", []string{"recovery", "mark", "{m}"}, `{"machine":"mch_b","state":"ready","from":"blocked","act":"act_1","observation":"obs_1"}`, "Scope {l} marked ready"},
		{"recovery release", []string{"recovery", "release", "{m}"}, `{"machine":"mch_b","from":"ready","act":"act_1"}`, "Scope {l} released"},
	} {
		for _, form := range []struct{ typed, label string }{{"db-1", "mch_b (db-1)"}, {"mch_b", "mch_b"}} {
			t.Run(c.name+" given "+form.typed, func(t *testing.T) {
				setServerFacts(t, 10, nil)
				recoveryServer(t, namedAnswers(), c.post)
				code, out, errOut := runDrift(t, withMachine(c.args, form.typed)...)
				want := strings.Replace(c.want, "{l}", form.label, 1)
				if code != 0 || !strings.Contains(out, want) {
					t.Errorf("%v: exit %d, stdout %q, stderr %q; want %q", c.args, code, out, errOut, want)
				}
				if form.typed == "mch_b" && strings.Contains(out, "mch_b (") {
					t.Errorf("%v: given the identifier, the line shows a name beside it: %q", c.args, out)
				}
			})
		}
	}
}

// Drift revert's refusals name the machine as the person typed it and its identifier.
func TestDriftRevertRefusalNamesTheMachine(t *testing.T) {
	setServerFacts(t, 10, nil)
	recoveryServer(t, namedAnswers(), `{}`)
	code, _, errOut := runDrift(t, "drift", "revert", "db-1")
	if code == 0 || !strings.Contains(errOut, "machine mch_b (db-1) has no Desired release to revert to") {
		t.Errorf("drift revert db-1: exit %d, stderr %q; want the refusal to name mch_b (db-1)", code, errOut)
	}
}

// The epoch moving between the name's lookup and the first read of the accounting refuses it: the reads the window is
// built from are not the reads the name was resolved at.
func TestScopeAccountByNameRefusesAMoveAfterTheLookup(t *testing.T) {
	answers := accAnswers(accScopeAnswer)
	answers["GET /api/v1/machines"] = `{"items":[` + refMachine("mch_a", "worker-1", "cls_a") + `]}`
	setServerFacts(t, 10, map[string]string{"/api/v1/server": "ep_BWSYNTH-2"})
	resetPosted()
	seen := recoveryServer(t, answers, accScopeAnswer)
	code, _, errb := account(t, "y\n", "recovery", "scope-account", "worker-1", "--start", "obs_s", "--end", "obs_e")
	if code == 0 || !strings.Contains(errb, "the epoch changed") || sentCount(seen(), accScope) != 0 {
		t.Errorf("scope-account by name, epoch moved after the lookup: exit %d, sent %v; want refused before the POST\n%s", code, seen(), errb)
	}
}

// Scope accounting heads its window with the machine as `<identifier> (<name>)` when given a name, and posts the
// identifier.
func TestScopeAccountByName(t *testing.T) {
	answers := accAnswers(accScopeAnswer)
	answers["GET /api/v1/machines"] = `{"items":[` + refMachine("mch_a", "worker-1", "cls_a") + `]}`
	answers["GET /api/v1/clusters"] = `{"items":[{"id":"cls_a","name":"prod"}]}`
	for _, c := range []struct{ typed, head string }{
		{"worker-1", "Scope accounting of machine mch_a (worker-1) in "}, {"mch_a", "Scope accounting of machine mch_a in "},
	} {
		resetPosted()
		seen := recoveryServer(t, answers, accScopeAnswer)
		code, _, errb := account(t, "y\n", "recovery", "scope-account", c.typed, "--start", "obs_s", "--end", "obs_e")
		if code != 0 || !strings.Contains(errb, c.head) || sentCount(seen(), accScope) != 1 {
			t.Errorf("scope-account %s: exit %d, sent %v; want the heading %q\n%s", c.typed, code, seen(), c.head, errb)
		}
		if p := posted(); len(p) == 0 || !strings.Contains(p[len(p)-1], `"machine":"mch_a"`) {
			t.Errorf("scope-account %s: posted %v; want the machine mch_a", c.typed, p)
		}
	}
}

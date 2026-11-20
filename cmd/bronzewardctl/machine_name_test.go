package main

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A machine's name (client contracts A9, ginsys/bronzeward#985): shown beside the identifier as a
// cluster's is, a machine with none or from a server older than API revision 10 printing as before.
const machineBase = `"cluster":"c1","platform":"metal","talosEndpoint":"10.0.0.1","hardware":{"talosNodeId":"n1"},` +
	`"scopeState":"normal","frozen":false,"pendingConvergence":false,"applied":null,"desired":null,"observed":null,"openDrift":null`

func machineBody(id string, name string) string {
	return `{"id":"` + id + `",` + name + machineBase + `}`
}

func TestMachineListAndShowName(t *testing.T) {
	list := `{"items":[` + strings.Join([]string{
		machineBody("mch_a", `"name":"web-1",`),
		machineBody("mch_b", `"name":null,`),
		machineBody("mch_c", ``), // a server older than API revision 10 serves no member
		machineBody("mch_d", `"name":"x\u001b[31mred",`),
	}, ",") + `]}`
	recoveryServer(t, map[string]string{
		"GET /api/v1/machines":                    list,
		"GET /api/v1/machines/mch_a":              machineBody("mch_a", `"name":"web-1",`),
		"GET /api/v1/machines/mch_b":              machineBody("mch_b", `"name":null,`),
		"GET /api/v1/machines/mch_c":              machineBody("mch_c", ``),
		"GET /api/v1/machines/mch_a/observations": `{"items":[]}`,
		"GET /api/v1/machines/mch_b/observations": `{"items":[]}`,
		"GET /api/v1/machines/mch_c/observations": `{"items":[]}`,
	}, "")
	var out, errb bytes.Buffer
	if rc := run([]string{"machine", "list"}, &out, &errb); rc != 0 {
		t.Fatalf("machine list: exit %d, stderr %q", rc, errb.String())
	}
	rows := map[string]string{}
	for _, l := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(l); len(f) > 0 && strings.HasPrefix(f[0], "mch_") {
			rows[f[0]] = l
		}
	}
	if !strings.HasPrefix(rows["mch_a"], "mch_a (web-1) ") {
		t.Errorf("a named machine: row %q; want the name beside the identifier", rows["mch_a"])
	}
	for _, id := range []string{"mch_b", "mch_c"} {
		if !regexp.MustCompile(`^` + id + ` +10\.0\.0\.1 `).MatchString(rows[id]) {
			t.Errorf("a machine with no name (%s): row %q; want the identifier alone, as before", id, rows[id])
		}
	}
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), `mch_d (x\x1b[31mred)`) {
		t.Errorf("a name with a control character is not escaped:\n%q", out.String())
	}
	for id, want := range map[string]*regexp.Regexp{
		"mch_a": regexp.MustCompile(`(?m)^Machine +mch_a \(web-1\) +cluster c1$`),
		"mch_b": regexp.MustCompile(`(?m)^Machine +mch_b +cluster c1$`),
		"mch_c": regexp.MustCompile(`(?m)^Machine +mch_c +cluster c1$`),
	} {
		out.Reset()
		errb.Reset()
		if rc := run([]string{"machine", "show", id}, &out, &errb); rc != 0 || !want.MatchString(out.String()) {
			t.Errorf("machine show %s: exit %d, stdout %q, stderr %q; want the heading line %v", id, rc, out.String(), errb.String(), want)
		}
	}
}

func TestMachineAddName(t *testing.T) {
	base := []string{"machine", "add", "--cluster", "cl_c", "--talos-node-id", "n", "--platform", "metal", "--talos-endpoint", "10.0.0.5:50000"}
	for _, c := range []struct {
		name, answer, want string
		args               []string
		body               string // a member the body must hold or lack
		sent               bool
	}{
		{"with a name", `{"id":"mch_new","name":"web-1","cluster":"cl_c","talosEndpoint":"10.0.0.5:50000"}`,
			"Machine mch_new (web-1) recorded in cluster cl_c (lab-a). Endpoint 10.0.0.5:50000.", append(slices.Clone(base), "--name", "web-1"), `"name":"web-1"`, true},
		{"without one", `{"id":"mch_new","name":null,"cluster":"cl_c","talosEndpoint":"10.0.0.5:50000"}`,
			"Machine mch_new recorded in cluster cl_c (lab-a). Endpoint 10.0.0.5:50000.", base, `"name"`, false},
	} {
		setServerFacts(t, 10, nil)
		resetPosted()
		recoveryServer(t, map[string]string{
			"GET /api/v1/recovery":      `{"epoch":"ep_BWSYNTH-1","recoveryMode":false}`,
			"GET /api/v1/clusters/cl_c": `{"id":"cl_c","name":"lab-a"}`,
		}, c.answer)
		var out, errb bytes.Buffer
		code := run(c.args, &out, &errb)
		body := strings.Join(posted(), "\n")
		if code != 0 || !strings.Contains(out.String(), c.want+"\n") || strings.Contains(body, c.body) != c.sent {
			t.Errorf("%s: exit %d, stdout %q, stderr %q, body %q; want 0, %q and %s sent %t", c.name, code, out.String(), errb.String(), body, c.want, c.body, c.sent)
		}
	}
}

// sprintfProblem is a problem document of the code, with extra members after the instance.
func sprintfProblem(code string, status int, extra string) string {
	return fmt.Sprintf(`{"type":"urn:bronzeward:problem:%s","title":"BWSYNTH","status":%d,"instance":"req_BWSYNTH"%s}`, code, status, extra)
}

func TestMachineRename(t *testing.T) {
	const route = "POST /api/v1/machines/mch_a/names"
	for _, c := range []struct {
		name, answer string
		code         int
		out, err     string
	}{
		{"renamed", `{"machine":"mch_a","revision":7,"previousName":"web-1","name":"web-2","changedBy":"idn_BWSYNTH","role":"author","act":"act_BWSYNTH","epoch":"ep_BWSYNTH-1","at":"2026-11-20T09:00:00Z"}`,
			0, "Machine mch_a: name web-2, was web-1 (timeline revision 7).\n", ""},
		{"named for the first time", `{"machine":"mch_a","revision":2,"previousName":null,"name":"web-2","changedBy":"idn_BWSYNTH","role":"author","act":"act_BWSYNTH","epoch":"ep_BWSYNTH-1","at":"2026-11-20T09:00:00Z"}`,
			0, "Machine mch_a: name web-2, was none (timeline revision 2).\n", ""},
		{"a name another machine holds names it", `409 ` + sprintfProblem("name-in-use", 409, `,"detail":"another machine of the cluster holds this name","machine":"mch_b"`),
			1, "", "The name is held by machine mch_b."},
		{"its own name", `409 ` + sprintfProblem("conflict", 409, `,"detail":"the machine already has this name","machine":"mch_a"`),
			1, "", "the machine already has this name"},
		{"a name that breaks the grammar", `400 ` + sprintfProblem("invalid-request", 400, `,"detail":"name must be 1 to 63 characters of lowercase a-z, digits and the hyphen"`),
			1, "", "name must be 1 to 63 characters of lowercase a-z, digits and the hyphen"},
	} {
		t.Run(c.name, func(t *testing.T) {
			setServerFacts(t, 10, nil)
			resetPosted()
			// An answer that starts with a status is a refusal the fake sends as such; any other is the 201 body.
			answers := map[string]string{}
			if c.code != 0 {
				answers[route] = c.answer
			}
			seen := recoveryServer(t, answers, c.answer)
			var out, errb bytes.Buffer
			code := run([]string{"machine", "rename", "mch_a", "--name", "web-2"}, &out, &errb)
			if code != c.code || out.String() != c.out || !strings.Contains(errb.String(), c.err) {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d, %q and %q", code, out.String(), errb.String(), c.code, c.out, c.err)
			}
			if !slices.Contains(seen(), route) || strings.Join(posted(), "") != `{"name":"web-2"}` {
				t.Errorf("sent %v with body %q; want %s with {\"name\":\"web-2\"}", seen(), posted(), route)
			}
		})
	}
}

// TestTimelineTextNameChange: the stored entry is {previous, new, principal, role} (previous null on a first
// naming). Only the kind name-change is told this way: endpoint-change stores the same two keys, and a kind
// this client does not know prints its record as it did.
func TestTimelineTextNameChange(t *testing.T) {
	e := &env{}
	for _, c := range []struct {
		kind  string
		entry map[string]any
		want  string
	}{
		{"name-change", map[string]any{"previous": nil, "new": "web-1", "principal": "h-all", "role": "author"}, "none -> web-1, by h-all (author)"},
		{"name-change", map[string]any{"previous": "web-1", "new": "web-2", "principal": "h-all", "role": "author"}, "web-1 -> web-2, by h-all (author)"},
		{"endpoint-change", map[string]any{"previous": "10.0.0.1:50000", "new": "10.0.0.2:50000", "principal": "h-all", "role": "author"},
			`{"new":"10.0.0.2:50000","previous":"10.0.0.1:50000","principal":"h-all","role":"author"}`},
		{"future-kind", map[string]any{"previous": "a", "new": "b"}, `{"new":"b","previous":"a"}`},
	} {
		if got := e.timelineText(c.kind, c.entry); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.kind, c.entry, got, c.want)
		}
	}
}

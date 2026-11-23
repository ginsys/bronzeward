package main

import (
	"os"
	"strings"
	"testing"
)

// A machine given by name (ginsys/bronzeward#1055, part 9b): `machine show`, `rename`, `freeze`, `unfreeze` and
// `plan create --machine` take a name or an identifier. An identifier is sent as it is and never lists; a bare name is
// matched in the machine list across every cluster the credential reads, and --cluster (a cluster's name or identifier)
// narrows it.
func refMachine(id, name, cluster string) string {
	return strings.Replace(machineBody(id, `"name":"`+name+`",`), `"cluster":"c1"`, `"cluster":"`+cluster+`"`, 1)
}

func refAnswers() map[string]string {
	return map[string]string{
		"GET /api/v1/machines": `{"items":[` + strings.Join([]string{
			refMachine("mch_a", "web-1", "cl_1"), refMachine("mch_b", "db-1", "cl_1"), refMachine("mch_c", "web-1", "cl_2"),
		}, ",") + `]}`,
		"GET /api/v1/clusters":                    `{"items":[{"id":"cl_1","name":"alpha"},{"id":"cl_2","name":"beta"}]}`,
		"GET /api/v1/machines/mch_a":              refMachine("mch_a", "web-1", "cl_1"),
		"GET /api/v1/machines/mch_b":              refMachine("mch_b", "db-1", "cl_1"),
		"GET /api/v1/machines/mch_c":              refMachine("mch_c", "web-1", "cl_2"),
		"GET /api/v1/machines/mch_a/observations": `{"items":[]}`,
		"GET /api/v1/machines/mch_b/observations": `{"items":[]}`,
		"GET /api/v1/machines/mch_c/observations": `{"items":[]}`,
	}
}

func sentCount(seen []string, want string) (n int) {
	for _, s := range seen {
		if s == want {
			n++
		}
	}
	return n
}

func TestMachineByName(t *testing.T) {
	t.Run("a name held by one machine reads that machine, after one read of the list", func(t *testing.T) {
		seen := recoveryServer(t, refAnswers(), "")
		code, out, errOut := runDrift(t, "machine", "show", "db-1")
		if code != 0 || !strings.Contains(out, "db-1") || sentCount(seen(), "GET /api/v1/machines/mch_b") != 1 || sentCount(seen(), "GET /api/v1/machines") != 1 {
			t.Errorf("machine show db-1: exit %d, stdout %q, stderr %q, sent %v", code, out, errOut, seen())
		}
	})
	t.Run("an identifier is sent as it is and never lists", func(t *testing.T) {
		seen := recoveryServer(t, refAnswers(), "")
		if code, _, errOut := runDrift(t, "machine", "show", "mch_b"); code != 0 || sentCount(seen(), "GET /api/v1/machines") != 0 {
			t.Errorf("machine show mch_b: exit %d, stderr %q, sent %v; want no read of the list", code, errOut, seen())
		}
	})
	t.Run("a name two clusters hold sends nothing and names both machines", func(t *testing.T) {
		seen := recoveryServer(t, refAnswers(), "")
		code, out, errOut := runDrift(t, "machine", "show", "web-1")
		want := "web-1 names 2 machines: mch_a in cluster alpha, mch_c in cluster beta; give the identifier or --cluster"
		if code == 0 || out != "" || !strings.Contains(errOut, want) || sentCount(seen(), "GET /api/v1/machines/mch_a") != 0 || sentCount(seen(), "GET /api/v1/machines/mch_c") != 0 {
			t.Errorf("machine show web-1: exit %d, stdout %q, stderr %q, sent %v; want %q", code, out, errOut, seen(), want)
		}
	})
	t.Run("--cluster narrows a name, by the cluster's name or its identifier", func(t *testing.T) {
		for _, c := range []struct{ cluster, id string }{{"alpha", "mch_a"}, {"beta", "mch_c"}, {"cl_2", "mch_c"}} {
			seen := recoveryServer(t, refAnswers(), "")
			if code, _, errOut := runDrift(t, "machine", "show", "web-1", "--cluster", c.cluster); code != 0 || sentCount(seen(), "GET /api/v1/machines/"+c.id) != 1 {
				t.Errorf("--cluster %s: exit %d, stderr %q, sent %v; want machine %s", c.cluster, code, errOut, seen(), c.id)
			}
		}
	})
	t.Run("a name no machine holds says so and sends nothing", func(t *testing.T) {
		seen := recoveryServer(t, refAnswers(), "")
		code, _, errOut := runDrift(t, "machine", "show", "nope")
		if code == 0 || !strings.Contains(errOut, "no machine is named nope") || len(seen()) > 3 {
			t.Errorf("machine show nope: exit %d, stderr %q, sent %v", code, errOut, seen())
		}
	})
	t.Run("freeze, unfreeze and rename send to the identifier the name resolves to", func(t *testing.T) {
		for _, c := range []struct {
			args  []string
			route string
		}{
			{[]string{"machine", "freeze", "db-1", "--reason", "r"}, "POST /api/v1/machines/mch_b/freezes"},
			{[]string{"machine", "unfreeze", "db-1", "--reason", "r"}, "POST /api/v1/machines/mch_b/unfreezes"},
			{[]string{"machine", "rename", "db-1", "--name", "db-2"}, "POST /api/v1/machines/mch_b/names"},
		} {
			setServerFacts(t, 10, nil) // a rename needs the name's API revision
			seen := recoveryServer(t, refAnswers(), strings.Replace(freezeAnswer, "%s", "true", 1))
			runDrift(t, c.args...)
			if sentCount(seen(), c.route) != 1 {
				t.Errorf("%v: sent %v; want %s", c.args, seen(), c.route)
			}
		}
	})
	t.Run("plan create sends the identifier the name resolves to", func(t *testing.T) {
		resetPosted()
		recoveryServer(t, refAnswers(), `{}`)
		runDrift(t, "plan", "create", "--release", "rel_a", "--machine", "db-1", "--operation", "apply-config")
		if p := posted(); len(p) == 0 || !strings.Contains(p[0], `"machine":"mch_b"`) {
			t.Errorf("plan create --machine db-1: posted %v; want the machine mch_b", p)
		}
	})
}

// A write built from a name is one client's work (a token on standard input is read once), is bound to the epoch of
// the lookup, and tells a repeat the identifier (review of ginsys/bronzeward#1055, part 9b, pull request 2).
func TestWriteByNameKeepsItsLookup(t *testing.T) {
	t.Run("a token on standard input is read once for the lookup and the write", func(t *testing.T) {
		seen := recoveryServer(t, refAnswers(), strings.Replace(freezeAnswer, "%s", "true", 1))
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		w.WriteString("bwt_BWSYNTH0123456789abcdef\n")
		w.Close()
		old := os.Stdin // the client reads the token from os.Stdin itself
		os.Stdin = r
		code, _, errOut := runDrift(t, "--token-stdin", "machine", "freeze", "db-1", "--reason", "r")
		os.Stdin = old
		r.Close()
		if code != 0 || sentCount(seen(), "POST /api/v1/machines/mch_b/freezes") != 1 {
			t.Errorf("freeze by name with --token-stdin: exit %d, stderr %q, sent %v", code, errOut, seen())
		}
	})
	t.Run("the epoch moving after the lookup refuses the write", func(t *testing.T) {
		setServerFacts(t, 10, map[string]string{"/api/v1/identity": "ep_BWSYNTH-2"})
		seen := recoveryServer(t, refAnswers(), strings.Replace(freezeAnswer, "%s", "true", 1))
		code, _, errOut := runDrift(t, "machine", "freeze", "db-1", "--reason", "r")
		if code == 0 || !strings.Contains(errOut, "the epoch changed") || sentCount(seen(), "POST /api/v1/machines/mch_b/freezes") != 0 {
			t.Errorf("freeze by name, epoch moved: exit %d, stderr %q, sent %v; want refused before the send", code, errOut, seen())
		}
	})
	t.Run("the epoch moving at the cluster read, after the machine list, refuses the write", func(t *testing.T) {
		setServerFacts(t, 10, map[string]string{"/api/v1/clusters": "ep_BWSYNTH-2"})
		seen := recoveryServer(t, refAnswers(), strings.Replace(freezeAnswer, "%s", "true", 1))
		code, _, errOut := runDrift(t, "machine", "freeze", "db-1", "--cluster", "alpha", "--reason", "r")
		if code == 0 || !strings.Contains(errOut, "the epoch changed") || sentCount(seen(), "POST /api/v1/machines/mch_a/freezes")+sentCount(seen(), "POST /api/v1/machines/mch_b/freezes") != 0 {
			t.Errorf("freeze by name and cluster, epoch moved at the cluster read: exit %d, stderr %q, sent %v; want refused before the send", code, errOut, seen())
		}
	})
	t.Run("a refresh after the machine list, showing yet another epoch, refuses the write", func(t *testing.T) {
		// The context has seen epoch 1; the list answers at epoch 2 and the client's own refresh read of the server then
		// shows epoch 3, as every later read does: the bind is the list's epoch 2, never the refresh's.
		seen := recoveryServer(t, refAnswers(), strings.Replace(freezeAnswer, "%s", "true", 1))
		if code, _, errOut := runDrift(t, "machine", "show", "mch_b"); code != 0 {
			t.Fatalf("the read that saves epoch 1: exit %d, stderr %q", code, errOut)
		}
		setServerFacts(t, 10, map[string]string{"/api/v1/machines": "ep_BWSYNTH-2", "/api/v1/server": "ep_BWSYNTH-3",
			"/api/v1/identity": "ep_BWSYNTH-3", "/api/v1/recovery": "ep_BWSYNTH-3"})
		code, _, errOut := runDrift(t, "machine", "freeze", "db-1", "--reason", "r")
		if code == 0 || !strings.Contains(errOut, "the epoch changed") || sentCount(seen(), "POST /api/v1/machines/mch_b/freezes") != 0 {
			t.Errorf("freeze by name, refresh after the list: exit %d, stderr %q, sent %v; want refused before the send", code, errOut, seen())
		}
	})
	t.Run("an unknown outcome tells a repeat the identifier, not the name", func(t *testing.T) {
		answers := refAnswers()
		answers["POST /api/v1/machines/mch_b/freezes"] = "500 " + problemHead + `internal","title":"BWSYNTH","status":500` + problemTail
		recoveryServer(t, answers, "")
		code, _, errOut := runDrift(t, "machine", "freeze", "db-1", "--reason", "r")
		if code != 3 || !strings.Contains(errOut, "the same input, with machine 'mch_b' in place of 'db-1',") {
			t.Errorf("freeze by name, outcome unknown: exit %d, stderr %q; want the repeat to name machine 'mch_b'", code, errOut)
		}
	})
}

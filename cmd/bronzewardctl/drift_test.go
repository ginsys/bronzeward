package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

const (
	driftMachine = `{"id":"mch_a","cluster":"cls_a","desired":"rel_b","applied":{"release":"rel_a","source":"apply"},` +
		`"openDrift":{"observation":"obs_open","lastObservation":"obs_last","lateLanding":%s,"revision":7},"frozen":%s,"scopeState":"normal"}`
	driftObservations = `{"items":[{"id":"obs_open","at":"2026-11-01T09:41:07Z"},{"id":"obs_last","at":"2026-11-01T09:46:12Z"}]}`
	noDriftMachine    = `{"id":"mch_a","cluster":"cls_a","desired":"rel_b","applied":{"release":"rel_a","source":"apply"},"openDrift":null,"frozen":false,"scopeState":"normal"}`
	freezeAnswer      = `{"machine":"mch_a","revision":41,"frozen":%s,"principal":"svc_BWSYNTH","role":"approver","reason":"incident 4711",` +
		`"act":"act_BWSYNTH","epoch":"ep_BWSYNTH-1","at":"2026-11-01T09:50:00Z"}`
	problemHead = `{"type":"urn:bronzeward:problem:`
	problemTail = `,"instance":"urn:bronzeward:act:BWSYNTH"}`
)

func driftJSON(late, frozen string) string {
	return strings.Replace(strings.Replace(driftMachine, "%s", late, 1), "%s", frozen, 1)
}

func runDrift(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// TestDriftShow: T27's sentences for an open drift, the times of the observations the record names,
// a late landing, an observation that was not read, and the machine with no drift. The sentences are
// pinned whole.
func TestDriftShow(t *testing.T) {
	const tail = "A configuration read differed from what Bronzeward last verified: Applied rel_a.\n" +
		"Bronzeward cannot show what changed until it is imported for adoption.\n" +
		"Choose: freeze while you decide; adopt it into a release; or revert to Desired (rel_b).\n"
	for _, c := range []struct{ name, machine, observations, want string }{
		{"open drift", driftJSON("null", "false"), driftObservations,
			"Drift opened by observation obs_open at 2026-11-01 09:41:07 UTC, last seen in observation obs_last at 2026-11-01 09:46:12 UTC. Late landing: no.\n" + tail},
		{"late landing", driftJSON(`"op_late"`, "false"), driftObservations,
			"Drift opened by observation obs_open at 2026-11-01 09:41:07 UTC, last seen in observation obs_last at 2026-11-01 09:46:12 UTC. Late landing: yes, operation op_late.\n" + tail},
		{"opening observation not read", driftJSON("null", "false"), `{"items":[{"id":"obs_last","at":"2026-11-01T09:46:12Z"}]}`,
			"Drift opened by observation obs_open (its time was not read), last seen in observation obs_last at 2026-11-01 09:46:12 UTC. Late landing: no.\n" + tail},
		{"frozen", driftJSON("null", "true"), driftObservations,
			"Drift opened by observation obs_open at 2026-11-01 09:41:07 UTC, last seen in observation obs_last at 2026-11-01 09:46:12 UTC. Late landing: no.\n" + tail +
				"Frozen: no new commitment or attempt until an approver unfreezes; an attempt already recorded is not stopped, and an adoption can still be recorded.\n"},
		{"no drift", noDriftMachine, driftObservations,
			"No open drift on machine mch_a.\n" +
				"Why: only a configuration read that differs from Applied opens a drift record; a read that matches it, or one that could not be read, opens none. See: machine observations 'mch_a'\n"},
	} {
		seen := recoveryServer(t, map[string]string{
			"GET /api/v1/machines/mch_a":              c.machine,
			"GET /api/v1/machines/mch_a/observations": c.observations,
		}, "")
		if code, out, errOut := runDrift(t, "drift", "show", "mch_a"); code != 0 || out != c.want {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %q", c.name, code, out, errOut, c.want)
		}
		if c.name == "no drift" && slices.Contains(seen(), "GET /api/v1/machines/mch_a/observations") {
			t.Errorf("a machine with no drift needs no observation read: %v", seen())
		}
	}
}

// TestDriftHasNoIgnore: drift offers show, and no verb that ends a drift without an adoption or a
// revert (#260 row 1: no ignore policy).
func TestDriftHasNoIgnore(t *testing.T) {
	recoveryServer(t, nil, "")
	for _, verb := range []string{"ignore", "retry", "resolve", "dismiss"} {
		if code, out, errOut := runDrift(t, "drift", verb, "mch_a"); code != 2 || out != "" || !strings.Contains(errOut, "drift needs show") {
			t.Errorf("drift %s: exit %d, stdout %q, stderr %q; want a usage error", verb, code, out, errOut)
		}
	}
	if _, out, _ := runDrift(t, "-h"); !strings.Contains(out, "there is no ignore command") {
		t.Errorf("the help text does not say that drift has no ignore command:\n%s", out)
	}
}

// TestMachineFreeze: the reason is required, a freeze and an unfreeze say what they do, and the
// stops and limits read as machine show says them.
func TestMachineFreeze(t *testing.T) {
	seen := recoveryServer(t, nil, strings.Replace(freezeAnswer, "%s", "true", 1))
	for _, reason := range [][]string{nil, {"--reason", ""}, {"--reason", strings.Repeat("x", 1025)}} {
		if code, _, errOut := runDrift(t, append([]string{"machine", "freeze", "mch_a"}, reason...)...); code != 2 || !strings.Contains(errOut, "1 to 1024 bytes; nothing was sent") {
			t.Errorf("reason of %d bytes: exit %d, %s", len(reason), code, errOut)
		}
	}
	if len(seen()) != 0 {
		t.Errorf("sent %v", seen())
	}
	code, out, errOut := runDrift(t, "machine", "freeze", "mch_a", "--reason", "incident 4711")
	want := "Frozen. No new commitment or attempt for this machine until an approver unfreezes it.\n" +
		"An attempt already recorded is not stopped, and an adoption can still be recorded.\n" +
		"Recorded by svc_BWSYNTH (approver): machine mch_a, timeline revision 41, act act_BWSYNTH.\n"
	if code != 0 || out != want || !slices.Contains(seen(), "POST /api/v1/machines/mch_a/freezes") {
		t.Errorf("freeze: exit %d, stdout %q, stderr %q, sent %v; want %q", code, out, errOut, seen(), want)
	}

	seen = recoveryServer(t, nil, strings.Replace(freezeAnswer, "%s", "false", 1))
	code, out, errOut = runDrift(t, "machine", "unfreeze", "mch_a", "--reason", "decided")
	want = "Unfrozen. The freeze no longer stops a commitment or an attempt for this machine.\n" +
		"Unfreezing approves and commits nothing.\n" +
		"Recorded by svc_BWSYNTH (approver): machine mch_a, timeline revision 41, act act_BWSYNTH.\n"
	if code != 0 || out != want || !slices.Contains(seen(), "POST /api/v1/machines/mch_a/unfreezes") {
		t.Errorf("unfreeze: exit %d, stdout %q, stderr %q, sent %v; want %q", code, out, errOut, seen(), want)
	}
}

// TestMachineFreezeSentenceMatchesShow: machine show and drift show say a freeze's stops and limits
// in the words that machine freeze answers with (one constant).
func TestMachineFreezeSentenceMatchesShow(t *testing.T) {
	recoveryServer(t, map[string]string{
		"GET /api/v1/machines/mch_a":              strings.Replace(driftJSON("null", "true"), `"scopeState":"normal"`, `"scopeState":"normal","observed":null`, 1),
		"GET /api/v1/machines/mch_a/observations": `{"items":[]}`,
	}, "")
	code, out, errOut := runDrift(t, "machine", "show", "mch_a")
	if code != 0 || !strings.Contains(out, freezeSentence+"\n") {
		t.Errorf("machine show: exit %d, stdout %q, stderr %q; want %q", code, out, errOut, freezeSentence)
	}
}

// TestUnfreezeWhyBlocked: an unfreeze that recovery mode refuses says why, and the scope's refusal is
// told from the process's by the scope member: the scope lines never follow the process's refusal
// and the process line never follows the scope's (#260 row 1).
func TestUnfreezeWhyBlocked(t *testing.T) {
	const (
		scopeWhy = "Why: a restore can erase the record of a request sent to this machine, and a request sent before the restore may still land. " +
			"Until a recovery administrator accounts for the scope and releases it, unfreezing is refused.\n" +
			"Next: recovery status names the next step of each scope."
		processWhy = "Why: a server started for recovery refuses unfreezing, plan creation and approval, even after its recovery entry.\n" +
			"Next: restart the server without the recovery flag. Recovery mode continues. Then repeat this command."
	)
	unfreeze := []string{"machine", "unfreeze", "mch_a", "--reason", "decided"}
	route := "POST /api/v1/machines/mch_a/unfreezes"

	recoveryServer(t, map[string]string{route: "409 " + problemHead + `recovery-mode-active","title":"Recovery mode active","status":409,` +
		`"detail":"the machine's scope is still pre-restore unaccounted","machine":"mch_a","scope":"pre-restore-unaccounted"` + problemTail}, "")
	code, out, errOut := runDrift(t, unfreeze...)
	if code != 1 || out != "" || !strings.Contains(errOut, "Recovery mode: the scope of machine mch_a is pre-restore-unaccounted.\n"+scopeWhy+"\n") ||
		strings.Contains(errOut, "started for recovery") {
		t.Errorf("a held scope: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	recoveryServer(t, map[string]string{route: "409 " + problemHead + `recovery-mode-active","title":"Recovery mode active","status":409,` +
		`"detail":"this process started with the recovery-start flag; restart it without the flag for this act"` + problemTail}, "")
	code, out, errOut = runDrift(t, unfreeze...)
	if code != 1 || out != "" || !strings.Contains(errOut, "Recovery mode: the server was started for recovery: it refuses plan creation and approval") ||
		!strings.Contains(errOut, processWhy+"\n") || strings.Contains(errOut, "the scope of machine") || strings.Contains(errOut, "Why: a restore can erase") ||
		strings.Contains(errOut, "recovery status names") {
		t.Errorf("the process's refusal: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	// Another scope state names itself.
	recoveryServer(t, map[string]string{route: "409 " + problemHead + `recovery-mode-active","title":"Recovery mode active","status":409,` +
		`"machine":"mch_a","scope":"blocked"` + problemTail}, "")
	if _, _, errOut = runDrift(t, unfreeze...); !strings.Contains(errOut, "Why: the scope is blocked, not released in this epoch, so unfreezing is refused until a recovery administrator releases it.\n") {
		t.Errorf("a blocked scope: stderr %q", errOut)
	}
}

// TestUnfreezeRoleRefused: the 403 names the roles the server named; nothing else is added.
func TestUnfreezeRoleRefused(t *testing.T) {
	recoveryServer(t, map[string]string{"POST /api/v1/machines/mch_a/unfreezes": "403 " + problemHead + `forbidden","title":"Forbidden","status":403,"roles":["approver"]` + problemTail}, "")
	code, out, errOut := runDrift(t, "machine", "unfreeze", "mch_a", "--reason", "decided")
	if code != 1 || out != "" || !strings.Contains(errOut, "Needs one of the roles [approver].") || strings.Contains(errOut, "Why:") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

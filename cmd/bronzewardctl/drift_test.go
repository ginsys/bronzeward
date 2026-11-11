package main

import (
	"bytes"
	"fmt"
	"net/http"
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

// TestDriftShowEpoch: a drift show prints nothing when the epoch moved after the machine answer,
// even when it moved back before the check (client-contracts.md §8.5). The count of moves tells the
// two apart; the epoch of the last answer cannot. A move that comes with the machine answer is
// not a move after it.
func TestDriftShowEpoch(t *testing.T) {
	answers := map[string]string{
		"GET /api/v1/machines/mch_a":              driftJSON("null", "false"),
		"GET /api/v1/machines/mch_a/observations": driftObservations,
	}
	again := "Read it again with: drift show 'mch_a'"
	for _, c := range []struct {
		name    string
		moves   map[string]string
		refresh string
		want    string // "" means the table is shown
	}{
		{"the observations move it and the refresh moves it back", map[string]string{"/api/v1/machines/mch_a/observations": "ep_BWSYNTH-2"}, "ep_BWSYNTH-1",
			"the epoch moved and came back to 'ep_BWSYNTH-1' while the drift was read; nothing is shown. " + again},
		{"the observations move it", map[string]string{"/api/v1/machines/mch_a/observations": "ep_BWSYNTH-2"}, "",
			"the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2' while the drift was read; nothing is shown. " + again},
		{"the machine answer moves it again before the refresh", map[string]string{"/api/v1/machines/mch_a": "ep_BWSYNTH-2"}, "ep_BWSYNTH-3",
			"the epoch changed from 'ep_BWSYNTH-2' to 'ep_BWSYNTH-3' while the drift was read; nothing is shown. " + again},
		{"the move comes with the machine answer", map[string]string{"/api/v1/machines/mch_a": "ep_BWSYNTH-2"}, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			setServerFacts(t, 3, c.moves)
			recoveryRefreshEpoch = c.refresh
			recoveryServer(t, answers, "")
			code, out, errOut := runDrift(t, "drift", "show", "mch_a")
			if c.want == "" {
				if code != 0 || !strings.Contains(out, "Drift opened by observation obs_open") {
					t.Errorf("exit %d, stdout %q, stderr %q; want the table", code, out, errOut)
				}
				return
			}
			if code == 0 || out != "" || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want no table and %q", code, out, errOut, c.want)
			}
		})
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
		"Unfreezing approves and commits nothing. An attempt already recorded was not stopped by the freeze, and an adoption could still be recorded.\n" +
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
			"Until a recovery administrator accounts for the scope, unfreezing is refused.\n" +
			"Next: recovery status names the next step of each scope."
		// The scope-less refusal is the same before and after the recovery entry, so the text covers both.
		processWhy = "Why: a server started for recovery refuses every request but the recovery entry until the entry is recorded. After the entry it still refuses unfreezing, plan creation and approval.\n" +
			"Next: if the recovery entry is not recorded yet, record it with recovery enter. Then restart the server without the recovery flag and repeat this command."
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
	if code != 1 || out != "" || !strings.Contains(errOut, "Recovery mode: the server was started for recovery: it refuses plan creation, approval and unfreeze") ||
		!strings.Contains(errOut, processWhy+"\n") || strings.Contains(errOut, "the scope of machine") || strings.Contains(errOut, "Why: a restore can erase") ||
		strings.Contains(errOut, "recovery status names") {
		t.Errorf("the process's refusal: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	// The server refuses an unfreeze only on pre-restore-unaccounted (internal/api/freeze.go:72), and
	// never refuses a freeze for a scope. A scope member the server names otherwise gets no reason of
	// the client's own: only the next step.
	for _, c := range []struct{ verb, scope string }{{"unfreeze", "blocked"}, {"freeze", "pre-restore-unaccounted"}} {
		recoveryServer(t, map[string]string{"POST /api/v1/machines/mch_a/" + c.verb + "s": "409 " + problemHead + `recovery-mode-active","title":"Recovery mode active","status":409,` +
			`"machine":"mch_a","scope":"` + c.scope + `"` + problemTail}, "")
		_, _, errOut = runDrift(t, "machine", c.verb, "mch_a", "--reason", "decided")
		if strings.Contains(errOut, "Why:") || !strings.Contains(errOut, "Next: recovery status names the next step of each scope.\n") {
			t.Errorf("%s on a %s scope: stderr %q; want the next step and no reason of the client's own", c.verb, c.scope, errOut)
		}
	}

	// A freeze that a server started for recovery refuses before its entry gets the process's text.
	recoveryServer(t, map[string]string{"POST /api/v1/machines/mch_a/freezes": "409 " + problemHead + `recovery-mode-active","title":"Recovery mode active","status":409,` +
		`"detail":"this process started with the recovery-start flag; only the entry is served"` + problemTail}, "")
	if _, _, errOut = runDrift(t, "machine", "freeze", "mch_a", "--reason", "decided"); !strings.Contains(errOut, processWhy+"\n") {
		t.Errorf("a freeze before the entry: stderr %q; want %q", errOut, processWhy)
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

// The usage text keeps the continuation line of machine set-endpoint under it: the entries added
// after it (machine freeze, drift show) go below that line, not between the entry and its line.
func TestUsageKeepsSetEndpointContinuation(t *testing.T) {
	_, after, ok := strings.Cut(usage, "  machine set-endpoint <id> --talos-endpoint <ip>[:<port>]\n")
	if !ok || !strings.HasPrefix(after, "              writes, by a person signed in as author") {
		t.Errorf("the line after machine set-endpoint is %q", strings.SplitN(after, "\n", 2)[0])
	}
}

// TestDriftRevertRefusesBeforeSending: drift revert reads the machine first and sends nothing for a
// machine with no Desired release or no open drift record; it exits 2 and names plan create (T30).
func TestDriftRevertRefusesBeforeSending(t *testing.T) {
	const noDesired = `{"id":"mch_a","cluster":"cls_a","applied":{"release":"rel_a","source":"apply"},` +
		`"openDrift":{"observation":"obs_open","lastObservation":"obs_open","revision":7},"frozen":false,"scopeState":"normal"}`
	for _, c := range []struct{ name, machine, want string }{
		{"no Desired", noDesired,
			"machine mch_a has no Desired release to revert to; nothing was sent. Publish a release that covers it, then: bronzewardctl plan create --release <release> --machine 'mch_a' --operation 'apply-config'"},
		{"no open drift", noDriftMachine,
			"machine mch_a has no open drift record; nothing was sent. To apply its Desired release without a revert: bronzewardctl plan create --release 'rel_b' --machine 'mch_a' --operation 'apply-config'"},
		// The server refuses an apply-config plan for a machine with no Applied configuration
		// (internal/api/plans.go), so the guidance is an adopt plan.
		{"no Applied", `{"id":"mch_a","cluster":"cls_a","desired":"rel_b","applied":null,"openDrift":null,"frozen":false,"scopeState":"normal"}`,
			"machine mch_a has no Applied configuration, so it has no drift to revert; nothing was sent. Hand it over with an adopt plan: bronzewardctl plan create --release 'rel_b' --machine 'mch_a' --operation 'adopt'"},
		{"no Applied and no Desired", `{"id":"mch_a","cluster":"cls_a","desired":null,"applied":null,"openDrift":null,"frozen":false,"scopeState":"normal"}`,
			"machine mch_a has no Desired release to revert to; nothing was sent. Publish a release that covers it, then: bronzewardctl plan create --release <release> --machine 'mch_a' --operation 'adopt'"},
	} {
		seen := recoveryServer(t, map[string]string{"GET /api/v1/machines/mch_a": c.machine}, "")
		if code, out, errOut := runDrift(t, "drift", "revert", "mch_a"); code != 2 || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 2 and %q", c.name, code, out, errOut, c.want)
		}
		for _, r := range seen() {
			if strings.HasPrefix(r, "POST ") {
				t.Errorf("%s: sent %s", c.name, r)
			}
		}
	}
}

// revertServer is a plan server whose machine mch_a has Desired rel_BWSYNTH, an Applied
// configuration and an open drift record 7; POST /plans creates the plan that binds that record.
func revertServer(t *testing.T) *planFake {
	f := planServer(t)
	f.set(func(f *planFake) {
		f.open, f.drift = "7", "7"
		f.post = func(w http.ResponseWriter, _ string) {
			w.WriteHeader(http.StatusCreated)
			f.mu.Unlock()
			fmt.Fprint(w, f.plan())
			f.mu.Lock()
		}
	})
	return f
}

// TestDriftRevertCreatesTheMachinesDesiredPlan: with an open drift record, drift revert creates the
// apply-config plan of the machine's Desired release, says what it binds and exits 0.
func TestDriftRevertCreatesTheMachinesDesiredPlan(t *testing.T) {
	f := revertServer(t)
	code, out, errOut := runDrift(t, "drift", "revert", "mch_a")
	const want = `/plans {"machine":"mch_a","mode":"no-reboot","operation":"apply-config","releaseId":"rel_BWSYNTH"}`
	if got := f.took(); code != 0 || len(got) != 1 || got[0] != want {
		t.Errorf("exit %d, sent %q, stderr %q; want exit 0 and %q", code, got, errOut, want)
	}
	if !strings.Contains(out, "Revert of drift record 7 to Desired rel_BWSYNTH.") {
		t.Errorf("stdout %q does not say what the plan binds", out)
	}
}

// TestDriftRevertBindsTheMachineRead: the machine is read and the plan created on one client, so
// the write is bound to the epoch of that read (client-contracts.md §8.5, §8.6 item 3). When the
// preflight reads of the send show another epoch, even one that is set back, nothing is sent.
func TestDriftRevertBindsTheMachineRead(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"the preflight moves it", "the epoch changed from 'ep_BWSYNTH-2' to 'ep_BWSYNTH-3' after the request was built"},
		{"the preflight moves it and back", "the epoch changed after the request was built from reads of 'ep_BWSYNTH-2', though the last read shows 'ep_BWSYNTH-2' as at those reads"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := revertServer(t)
			back := strings.HasSuffix(c.name, "and back")
			read, identity := false, 0
			f.set(func(f *planFake) {
				f.epochFor = func(path string) string {
					// The machine is read at ep_BWSYNTH-2. Send then reads the principal, GET /server and
					// the principal again: the first read shows ep_BWSYNTH-3 and the others stay there, or
					// with back they show ep_BWSYNTH-2 again.
					switch {
					case path == "/machines/mch_a":
						read = true
					case !read:
					case path == "/identity" && identity == 0:
						identity++
						return "ep_BWSYNTH-3"
					case !back:
						return "ep_BWSYNTH-3"
					}
					return "ep_BWSYNTH-2"
				}
			})
			code, out, errOut := runDrift(t, "drift", "revert", "mch_a")
			if got := f.took(); code == 0 || out != "" || len(got) != 0 || !strings.Contains(errOut, c.want) || !strings.Contains(errOut, "Nothing was sent") {
				t.Errorf("exit %d, stdout %q, sent %q, stderr %q; want a refusal that sent nothing and says %q", code, out, got, errOut, c.want)
			}
		})
	}
}

// TestDriftRevertCannotRepeat: a repeat would read Desired again, which may have changed, so drift
// revert takes no repeat flags; it reads and sends nothing and names plan create. The outcome-unknown
// block of a revert names plan create with the release it read, so the repeat is the identical request.
func TestDriftRevertCannotRepeat(t *testing.T) {
	f := revertServer(t)
	code, out, errOut := runDrift(t, "drift", "revert", "mch_a", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa", "--expect-epoch", "ep_BWSYNTH-2", "--expect-principal", "svc_BWSYNTH")
	if got := f.took(); code != 2 || out != "" || len(got) != 0 || !strings.Contains(errOut, "so it cannot repeat a write; nothing was sent. Repeat through plan create") {
		t.Errorf("exit %d, stdout %q, sent %q, stderr %q; want a usage error that sent nothing", code, out, got, errOut)
	}
	f.set(func(f *planFake) {
		f.post = func(w http.ResponseWriter, _ string) {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusTemporaryRedirect) // an answer the client cannot read: the outcome is unknown
		}
	})
	code, _, errOut = runDrift(t, "drift", "revert", "mch_a")
	const want = "give the same input, with the command bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_a' --operation 'apply-config' in place of drift revert, and: --idempotency-key "
	if code != 3 || !strings.Contains(errOut, want) {
		t.Errorf("exit %d, stderr %q; want exit 3 and %q", code, errOut, want)
	}
}

// TestDriftRevertViewIsOneSnapshot: the lines that say whether the machine is frozen come from a
// further read of the machine. When that read shows another epoch the plan stands, and the view is
// not shown, as for any plan view.
func TestDriftRevertViewIsOneSnapshot(t *testing.T) {
	f := revertServer(t)
	machineReads := 0
	f.set(func(f *planFake) {
		f.epochFor = func(path string) string {
			if path == "/machines/mch_a" {
				machineReads++
				// The reads are drift revert's own, planView's and the one revertLines makes for the frozen state.
				if machineReads > 2 {
					return "ep_BWSYNTH-3"
				}
			}
			return "ep_BWSYNTH-2"
		}
	})
	code, out, errOut := runDrift(t, "drift", "revert", "mch_a")
	const want = "Plan pln_BWSYNTH was created for machine mch_a, operation apply-config."
	if code != 0 || !strings.Contains(out, want) || strings.Contains(out, "Revert of drift record") || !strings.Contains(out, "while the plan was read, so it is not shown") {
		t.Errorf("exit %d, stdout %q, stderr %q; want the plan named and no view of it", code, out, errOut)
	}
}

// TestDriftAdoptNeedsAnAuthorAndADraft: drift adopt without --draft is a usage error. Signed in as a
// service (the fake server's identity), it runs no step and prints the three, each with its role.
func TestDriftAdoptNeedsAnAuthorAndADraft(t *testing.T) {
	seen := recoveryServer(t, nil, "")
	if code, out, errOut := runDrift(t, "drift", "adopt", "mch_a"); code != 2 || out != "" || !strings.Contains(errOut, "drift adopt needs --draft") {
		t.Errorf("no draft: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	const want = "Not run: step 1 needs an author signed in as a person. The steps:\n" +
		"Step 1/3  import (drift-adoption): an author, signed in as a person.\n" +
		"          bronzewardctl drift adopt 'mch_a' --draft 'drf_a' --review\n" +
		"Step 2/3  publish, which publishes the whole draft: a publisher.\n" +
		"          bronzewardctl publish 'drf_a'\n" +
		"Step 3/3  plan an adoption: a publisher, then an approver.\n" +
		"          bronzewardctl plan create --release <release> --machine 'mch_a' --operation 'adopt'\n" +
		"          bronzewardctl approve <plan>\n"
	if code, out, errOut := runDrift(t, "drift", "adopt", "mch_a", "--draft", "drf_a"); code != 0 || out != want {
		t.Errorf("a service: exit %d, stdout %q, stderr %q; want %q", code, out, errOut, want)
	}
	// An invalid repeat flag is refused before the role guidance, not skipped by it.
	if code, out, errOut := runDrift(t, "drift", "adopt", "mch_a", "--draft", "drf_a", "--idempotency-key="); code != 2 || out != "" {
		t.Errorf("an empty key: exit %d, stdout %q, stderr %q; want exit 2 and no guidance", code, out, errOut)
	}
	// The staging and review rules of the import step are refused the same way for a caller without the author role.
	for _, c := range []struct {
		name, want string
		flags      []string
	}{
		{"a staging that is neither", `--staging "bogus": give encrypted or transient`, []string{"--staging=bogus"}},
		{"a review of transient staging", "--review needs encrypted staging", []string{"--review", "--staging=transient"}},
	} {
		code, out, errOut := runDrift(t, append([]string{"drift", "adopt", "mch_a", "--draft", "drf_a"}, c.flags...)...)
		if code != 2 || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 2, no guidance and %q", c.name, code, out, errOut, c.want)
		}
	}
	for _, r := range seen() {
		if strings.HasPrefix(r, "POST ") {
			t.Errorf("sent %s", r)
		}
	}
}

// TestDriftAdoptNoOpenDriftRefused: drift adopt on a machine with no open drift record (#360 row 2: "a
// machine with no open drift is refused"). The server answers the import start with 409 conflict
// naming the machine (internal/api/ingestions.go), and the CLI has no check of its own, so the
// refusal must reach the caller whole: a failed step 1, no publication and no plan, exit 1.
func TestDriftAdoptNoOpenDriftRefused(t *testing.T) {
	f := adoptChainServer(t)
	f.imp.refuseStart = problemHead + `conflict","title":"Conflict","status":409,"detail":"the machine has no open drift record","machine":"mch_BWSYNTH"` + problemTail
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	const refusal = "POST /api/v1/ingestions answered 409 Conflict (conflict): the machine has no open drift record"
	if code != 1 || !strings.Contains(errOut, refusal) {
		t.Errorf("exit %d, stderr %q; want exit 1 and %q", code, errOut, refusal)
	}
	if !strings.HasPrefix(out, "Step 1/3  import (drift-adoption) of machine mch_BWSYNTH into draft drf_BWSYNTH\nThe later steps:\n") ||
		!strings.Contains(out, "bronzewardctl publish 'drf_BWSYNTH'") || !strings.Contains(out, "--operation 'adopt'") {
		t.Errorf("stdout %q; want step 1 as the step that was tried, and steps 2 and 3 as not run", out)
	}
	posts := 0
	for _, l := range f.log {
		if strings.HasPrefix(l, "POST /ingestions") {
			posts++
		}
		if strings.Contains(l, "/publications") || strings.HasPrefix(l, "POST /plans") {
			t.Errorf("sent %s after the refusal", l)
		}
	}
	if got := f.plan.took(); posts != 1 || len(got) != 0 {
		t.Errorf("%d import starts and plans %q; want one start and no plan", posts, got)
	}
	// The server looks for an open drift record only for the kind drift-adoption, so the start that
	// was refused must be one.
	if got := f.imp.took(); len(got) != 1 || !strings.HasPrefix(got[0], "POST /ingestions ") || !strings.Contains(got[0], `"kind":"drift-adoption"`) {
		t.Errorf("writes %q; want one POST /ingestions of the kind drift-adoption", got)
	}
}

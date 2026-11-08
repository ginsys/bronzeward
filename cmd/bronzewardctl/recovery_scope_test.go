package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The four acts of T34 and T35 (#362 PR B; operator-experience.md §5.9): recovery takeover, resolve,
// mark and release, against recoveryServer. Operation op_1 of machine mch_a is unresolved with one
// attempt at generation 2; op_2 is unresolved with none; op_3 is succeeded.

const (
	scTake    = "POST /api/v1/operations/op_1/takeovers"
	scResolve = "POST /api/v1/operations/op_2/resolutions"
	scMark    = "POST /api/v1/recovery/scopes/mch_a/marks"
	scRelease = "POST /api/v1/recovery/scopes/mch_a/releases"
)

func scAnswers() map[string]string {
	return map[string]string{
		"GET /api/v1/operations/op_1": `{"id":"op_1","kind":"apply-config","state":"unresolved","subject":{"machine":"mch_a"},` +
			`"attempts":[{"id":"att_1","number":1,"generation":2,"transportDeadline":"2026-11-01T08:50:00Z"}]}`,
		"GET /api/v1/operations/op_2": `{"id":"op_2","kind":"apply-config","state":"unresolved","subject":{"machine":"mch_a"}}`,
		"GET /api/v1/operations/op_3": `{"id":"op_3","kind":"apply-config","state":"succeeded","subject":{"machine":"mch_a"}}`,
		"GET /api/v1/operations/op_1/events": `{"items":[` +
			`{"id":"ep_BWSYNTH-1:4","type":"ownership-transition","data":{"generation":3,"fromGeneration":2}},` +
			`{"id":"ep_BWSYNTH-1:5","type":"operation-state","data":{"to":"unresolved","generation":3}},` +
			`{"id":"ep_BWSYNTH-1:6","type":"attempt","data":{"generation":2}}]}`,
		"GET /api/v1/operations/op_2/events": `{"items":[{"id":"ep_BWSYNTH-1:2","type":"commitment","data":{"operation":"op_2"}}]}`,
	}
}

const (
	scTakeAnswer = `{"act":"act_BWSYNTH","at":"2026-11-01T09:05:00Z","epoch":"ep_BWSYNTH-1","generation":4,"operation":"op_1","reason":"BWSYNTH",` +
		`"requestedBy":"prn_BWSYNTH","revision":9,"role":"recovery-admin","state":"unresolved"}`
	scResolveAnswer = `{"act":"act_BWSYNTH","at":"2026-11-01T09:05:00Z","epoch":"ep_BWSYNTH-1","operation":"op_2","reason":"BWSYNTH",` +
		`"resolvedBy":"prn_BWSYNTH","revision":9,"role":"recovery-admin","state":"cancelled"}`
	scReleaseAnswer = `{"act":"act_BWSYNTH","at":"2026-11-01T09:05:00Z","epoch":"ep_BWSYNTH-1","from":"ready","machine":"mch_a","mark":7,` +
		`"releasedBy":"prn_BWSYNTH","revision":9,"role":"recovery-admin","state":"released"}`
)

func markAnswer(state string, missing ...string) string {
	m, _ := json.Marshal(missing)
	return `{"act":"act_BWSYNTH","at":"2026-11-01T09:05:00Z","dependencies":{},"epoch":"ep_BWSYNTH-1","from":"blocked","machine":"mch_a",` +
		`"markedBy":"prn_BWSYNTH","missing":` + string(m) + `,"observation":"obs_r","revision":9,"role":"recovery-admin","state":"` + state + `"}`
}

func problem(status, code, detail, members string) string {
	return status + ` {"type":"urn:bronzeward:problem:` + code + `","title":"` + code + `","status":` + status + `,"detail":"` + detail + `"` + members + `,"instance":"req_BWSYNTH"}`
}

// scRun runs args against a server with the answers and the post, and returns the exit code and streams.
func scRun(t *testing.T, answers map[string]string, post string, args ...string) (int, string, string) {
	t.Helper()
	recoveryPosted = nil
	recoveryServer(t, answers, post)
	return account(t, "", args...)
}

// TestTakeoverSendsTheHighestGeneration: gap G2. The generation is the highest the operation's events
// and attempts show, 1 when it shows none; the answer names the new generation; the reason is required.
func TestTakeoverSendsTheHighestGeneration(t *testing.T) {
	for _, c := range []struct {
		op   string
		gen  int
		post string
	}{{"op_1", 3, scTakeAnswer}, {"op_2", 1, strings.ReplaceAll(scTakeAnswer, `"op_1"`, `"op_2"`)}} {
		code, out, errb := scRun(t, scAnswers(), c.post, "recovery", "takeover", c.op, "--reason", "BWSYNTH drill")
		var got, want map[string]any
		wantBody, _ := json.Marshal(map[string]any{"generation": c.gen, "reason": "BWSYNTH drill"})
		if code != 0 || len(recoveryPosted) != 1 || json.Unmarshal([]byte(recoveryPosted[0]), &got) != nil ||
			json.Unmarshal(wantBody, &want) != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: exit %d, body %v; want 0 and %s\n%s", c.op, code, recoveryPosted, wantBody, errb)
		}
		flat := strings.Join(strings.Fields(out), " ")
		for _, s := range []string{"taken over (act act_BWSYNTH): unresolved at generation 4", "Next: recovery account"} {
			if !strings.Contains(flat, s) {
				t.Errorf("%s: output lacks %q:\n%s", c.op, s, out)
			}
		}
		if !strings.Contains(errb, "read at owner generation") || !strings.Contains(errb, "sends nothing to the node") {
			t.Errorf("%s: the effects are not shown first:\n%s", c.op, errb)
		}
	}
	if code, _, errb := scRun(t, scAnswers(), scTakeAnswer, "recovery", "takeover", "op_1"); code != 2 || len(recoveryPosted) != 0 || !strings.Contains(errb, "give --reason") {
		t.Errorf("no reason: exit %d, %d POSTs\n%s", code, len(recoveryPosted), errb)
	}
}

// TestOperationActsRefuseBeforeSending: a finished operation is not taken over, and only an
// unresolved one with no attempt is resolved. Nothing is sent and the exit code is 1.
func TestOperationActsRefuseBeforeSending(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"recovery", "takeover", "op_3", "--reason", "BWSYNTH"}, "a finished operation is not taken over"},
		{[]string{"recovery", "resolve", "op_3", "--reason", "BWSYNTH"}, "only an unresolved operation is resolved"},
		{[]string{"recovery", "resolve", "op_1", "--reason", "BWSYNTH"}, "has an attempt"},
	} {
		code, out, errb := scRun(t, scAnswers(), scResolveAnswer, c.args...)
		if code != 1 || out != "" || len(recoveryPosted) != 0 || !strings.Contains(errb, c.want) || !strings.Contains(errb, "nothing was sent") {
			t.Errorf("%v: exit %d, %d POSTs; want 1, no POST and %q\n%s", c.args, code, len(recoveryPosted), c.want, errb)
		}
	}
}

// TestTakeoverGenerationMoved: gap G10. The refusal carries no new generation, so the next step is to
// read the operation again.
func TestTakeoverGenerationMoved(t *testing.T) {
	a := scAnswers()
	a[scTake] = problem("409", "conflict", "the operation's owner generation is not 3", `,"transaction":"takeover"`)
	code, out, errb := scRun(t, a, "", "recovery", "takeover", "op_1", "--reason", "BWSYNTH")
	if code != 1 || out != "" || !strings.Contains(errb, "owner generation is not 3") || !strings.Contains(errb, "Next: operation show 'op_1', then repeat this command.") {
		t.Errorf("exit %d, stdout %q\n%s", code, out, errb)
	}
}

// TestResolveRecordsCancelledOnly: the body is to=cancelled and the reason; the output says it records
// no remote outcome; a refused one gets a next step.
func TestResolveRecordsCancelledOnly(t *testing.T) {
	code, out, errb := scRun(t, scAnswers(), scResolveAnswer, "recovery", "resolve", "op_2", "--reason", "BWSYNTH drill")
	var got, want map[string]any
	if code != 0 || len(recoveryPosted) != 1 || json.Unmarshal([]byte(recoveryPosted[0]), &got) != nil ||
		json.Unmarshal([]byte(`{"to":"cancelled","reason":"BWSYNTH drill"}`), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("exit %d, body %v\n%s", code, recoveryPosted, errb)
	}
	if !strings.Contains(out, "resolved (act act_BWSYNTH): cancelled") || !strings.Contains(out, "It records no remote outcome.") || !strings.Contains(errb, "records no remote outcome") {
		t.Errorf("the record is not shown:\nstdout %s\nstderr %s", out, errb)
	}
	a := scAnswers()
	a[scResolve] = problem("409", "conflict", "an attempt is recorded for the operation", `,"transaction":"resolution"`)
	if code, _, errb := scRun(t, a, "", "recovery", "resolve", "op_2", "--reason", "BWSYNTH"); code != 1 || !strings.Contains(errb, "Next: an operation with an attempt is classified") {
		t.Errorf("exit %d\n%s", code, errb)
	}
}

// TestMarkShowsWhatIsMissing: gap G8. Every missing item gets its meaning, and the next step follows
// the state the server answered.
func TestMarkShowsWhatIsMissing(t *testing.T) {
	for _, c := range []struct {
		post  string
		shows []string
	}{
		{markAnswer("blocked", "observation", "access"), []string{"marked blocked, was blocked", "missing observation: the restoration observation", "missing access: the cluster's Talos access", "then recovery mark 'mch_a' again"}},
		{markAnswer("blocked", "identity", "artifact"), []string{"missing identity:", "missing artifact:"}},
		{markAnswer("ready"), []string{"marked ready", "Next: recovery release 'mch_a'."}},
		{markAnswer("unresolved"), []string{"marked unresolved", "A restored operation still holds the scope."}},
	} {
		code, out, errb := scRun(t, scAnswers(), c.post, "recovery", "mark", "mch_a", "--fact", "note=BWSYNTH")
		flat := strings.Join(strings.Fields(out), " ")
		var body map[string]any
		if code != 0 || len(recoveryPosted) != 1 || json.Unmarshal([]byte(recoveryPosted[0]), &body) != nil || !reflect.DeepEqual(body, map[string]any{"facts": map[string]any{"note": "BWSYNTH"}}) {
			t.Errorf("exit %d, body %v\n%s", code, recoveryPosted, errb)
		}
		for _, s := range c.shows {
			if !strings.Contains(flat, s) {
				t.Errorf("output lacks %q:\n%s", s, out)
			}
		}
	}
}

// TestReleaseIsNotAnApproval: a release says it is not an approval and sends no state of its own.
func TestReleaseIsNotAnApproval(t *testing.T) {
	code, out, errb := scRun(t, scAnswers(), scReleaseAnswer, "recovery", "release", "mch_a")
	flat := strings.Join(strings.Fields(out), " ")
	if code != 0 || len(recoveryPosted) != 1 || recoveryPosted[0] != "{}" {
		t.Fatalf("exit %d, body %v\n%s", code, recoveryPosted, errb)
	}
	for _, s := range []string{"Scope mch_a released, was ready (act act_BWSYNTH).", "A release is not an approval: it approves no plan and sends nothing."} {
		if !strings.Contains(flat, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	// Every use of the word is the negation, or says what still needs an approval.
	for _, line := range strings.Split(strings.ToLower(out), "\n") {
		if strings.Contains(line, "approv") && !strings.Contains(line, "not an approval") {
			t.Errorf("a line of the output calls a release an approval: %q", line)
		}
	}
}

// TestScopeRefusalsGetNextSteps: gap G9 and REC-01. A refusal with code conflict gets per-command
// text; the scope refusal of a pre-restore unaccounted scope names the accounting.
func TestScopeRefusalsGetNextSteps(t *testing.T) {
	scope := problem("409", "recovery-mode-active", "the scope is still pre-restore unaccounted: it leaves only by the post-restore accounting decision",
		`,"machine":"mch_a","scope":"pre-restore-unaccounted"`)
	for _, c := range []struct {
		verb, refusal, want string
	}{
		{"release", problem("409", "conflict", "the scope is blocked; only a ready scope is released", `,"machine":"mch_a","scope":"blocked"`), "Next: mark the scope until it is ready: recovery mark 'mch_a'. A release is not an approval."},
		{"release", problem("409", "conflict", "the Desired release changed since the scope was marked; mark it again", `,"machine":"mch_a"`), "Next: mark the scope again: recovery mark 'mch_a'."},
		{"release", scope, "Next: account the scope first: recovery scope-account 'mch_a'."},
		{"mark", scope, "Next: account the scope first: recovery scope-account 'mch_a'."},
		{"mark", problem("409", "conflict", "the scope is released and is not marked", `,"machine":"mch_a","scope":"released"`), "recovery status names the state"},
		{"mark", problem("409", "conflict", "the Desired release changed while the scope was checked; nothing was marked", `,"machine":"mch_a"`), "Next: mark the scope again"},
	} {
		a := scAnswers()
		a["POST /api/v1/recovery/scopes/mch_a/"+c.verb+"s"] = c.refusal
		code, out, errb := scRun(t, a, "", "recovery", c.verb, "mch_a")
		if code != 1 || out != "" || !strings.Contains(errb, c.want) {
			t.Errorf("%s: exit %d, stdout %q; want 1 and %q\n%s", c.verb, code, out, c.want, errb)
		}
	}
}

// TestNoRetryAndNoBatch: T37. A retry, a "mark successful" and an ignore are unknown verbs, and a
// second machine is a usage error. Nothing is sent.
func TestNoRetryAndNoBatch(t *testing.T) {
	for _, args := range [][]string{
		{"recovery", "retry", "op_1"}, {"recovery", "mark-successful", "mch_a"}, {"recovery", "ignore", "mch_a"},
		{"recovery", "mark", "mch_a", "mch_b"}, {"recovery", "release", "mch_a", "mch_b"},
		{"recovery", "takeover", "op_1", "op_2", "--reason", "BWSYNTH"}, {"recovery", "resolve", "op_1", "op_2", "--reason", "BWSYNTH"},
	} {
		recoveryPosted = nil
		seen := recoveryServer(t, scAnswers(), "{}")
		if code, out, _ := account(t, "", args...); code != 2 || out != "" || len(recoveryPosted) != 0 || slices.ContainsFunc(seen(), func(s string) bool { return strings.HasPrefix(s, "POST") }) {
			t.Errorf("%v: exit %d, %d POSTs; want 2 and none", args, code, len(recoveryPosted))
		}
	}
}

// TestOperationActEpochChange: a new epoch after the reads is shown at the send. At most one POST goes
// out, the write is set aside and nothing is repeated (AMB-05, AMB-07, AMB-12).
func TestOperationActEpochChange(t *testing.T) {
	recoveryEpochByPath = map[string]string{"/api/v1/operations/op_1/events": "ep_BWSYNTH-2"}
	t.Cleanup(func() { recoveryEpochByPath = nil })
	code, out, errb := scRun(t, scAnswers(), scTakeAnswer, "recovery", "takeover", "op_1", "--reason", "BWSYNTH")
	if code == 0 || out != "" || len(recoveryPosted) > 1 || !strings.Contains(errb, "epoch") {
		t.Errorf("exit %d, stdout %q, %d POSTs; want a refusal naming the epoch and at most one POST\n%s", code, out, len(recoveryPosted), errb)
	}
}

// repeatFlagsOf are the flags an outcome-unknown block prints for a repeat of a write.
func repeatAnswers() map[string]string {
	a := scAnswers()
	a["GET /api/v1/recovery"] = `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[]}`
	return a
}

func repeatFlagsOf() []string {
	return []string{"--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa", "--expect-epoch", "ep_BWSYNTH-1", "--expect-principal", "svc_BWSYNTH"}
}

// TestTakeoverRepeatSendsTheOriginalGeneration: a takeover that committed with every response lost shows
// its own transition at generation 4. The repeat sends the generation the block printed, 3, and not
// the one the events now show, which the server refuses under the same key (§8.4 item 4).
func TestTakeoverRepeatSendsTheOriginalGeneration(t *testing.T) {
	a := repeatAnswers()
	a["GET /api/v1/operations/op_1/events"] = `{"items":[{"id":"ep_BWSYNTH-1:7","type":"ownership-transition","data":{"generation":4,"fromGeneration":3}}]}`
	args := append([]string{"recovery", "takeover", "op_1", "--reason", "BWSYNTH", "--generation", "3"}, repeatFlagsOf()...)
	code, out, errb := scRun(t, a, scTakeAnswer, args...)
	var got map[string]any
	if code != 0 || len(recoveryPosted) != 1 || json.Unmarshal([]byte(recoveryPosted[0]), &got) != nil || got["generation"] != float64(3) {
		t.Fatalf("exit %d, body %v; want 0 and generation 3\nstdout %s\n%s", code, recoveryPosted, out, errb)
	}
	// A repeat needs the generation, and a first send takes none; nothing is sent.
	for name, args := range map[string][]string{
		"repeat without it":  append([]string{"recovery", "takeover", "op_1", "--reason", "BWSYNTH"}, repeatFlagsOf()...),
		"repeat with a zero": append([]string{"recovery", "takeover", "op_1", "--reason", "BWSYNTH", "--generation", "0"}, repeatFlagsOf()...),
		"first send":         {"recovery", "takeover", "op_1", "--reason", "BWSYNTH", "--generation", "3"},
	} {
		if code, _, errb := scRun(t, repeatAnswers(), scTakeAnswer, args...); code != 2 || len(recoveryPosted) != 0 || !strings.Contains(errb, "--generation") {
			t.Errorf("%s: exit %d, %d POSTs; want 2, no POST and a mention of --generation\n%s", name, code, len(recoveryPosted), errb)
		}
	}
}

// TestTakeoverBlockNamesTheGeneration: the block of an unknown outcome gives the generation that was
// sent, so that the repeat can send it again.
func TestTakeoverBlockNamesTheGeneration(t *testing.T) {
	a := repeatAnswers()
	a[scTake] = problem("503", "unavailable", "BWSYNTH", "")
	code, _, errb := scRun(t, a, "", "recovery", "takeover", "op_1", "--reason", "BWSYNTH")
	if code != 3 || !strings.Contains(errb, "the same input, with --generation 3, and: --idempotency-key ") {
		t.Errorf("exit %d; want 3 and the generation in the repeat line\n%s", code, errb)
	}
}

// TestResolveRepeatAfterTheCommit: a resolution that committed with every response lost leaves the
// operation cancelled. The repeat sends the unchanged body and does not stop at the state check, so
// the server replays its answer (§8.4 item 4).
func TestResolveRepeatAfterTheCommit(t *testing.T) {
	a := repeatAnswers()
	a["GET /api/v1/operations/op_2"] = `{"id":"op_2","kind":"apply-config","state":"cancelled","subject":{"machine":"mch_a"}}`
	args := append([]string{"recovery", "resolve", "op_2", "--reason", "BWSYNTH"}, repeatFlagsOf()...)
	code, out, errb := scRun(t, a, scResolveAnswer, args...)
	var got map[string]any
	if code != 0 || len(recoveryPosted) != 1 || json.Unmarshal([]byte(recoveryPosted[0]), &got) != nil || got["to"] != "cancelled" {
		t.Errorf("exit %d, body %v; want 0 and one POST to cancelled\nstdout %s\n%s", code, recoveryPosted, out, errb)
	}
}

// TestResolveBindsToTheOperationAnswer: the epoch moves with the operation answer and moves again before
// the client refreshes its server facts, so the client's last epoch is past the answer's own. The send
// stays bound to the answer that the checks read, and nothing is sent (§8.5, §8.6 item 3).
func TestResolveBindsToTheOperationAnswer(t *testing.T) {
	setServerFacts(t, 3, map[string]string{"/api/v1/operations/op_2": "ep_BWSYNTH-2"})
	recoveryRefreshEpoch = "ep_BWSYNTH-3"
	code, out, errb := scRun(t, scAnswers(), scResolveAnswer, "recovery", "resolve", "op_2", "--reason", "BWSYNTH")
	if code == 0 || out != "" || len(recoveryPosted) != 0 || !strings.Contains(errb, "epoch") {
		t.Errorf("exit %d, stdout %q, %d POSTs; want a refusal naming the epoch and no POST\n%s", code, out, len(recoveryPosted), errb)
	}
}

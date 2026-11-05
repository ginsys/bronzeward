package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The two accountings (#362 PR A; operator-experience.md §5.9): recovery scope-account and
// recovery account, against recoveryServer. Machine mch_a has the observations obs_s (routine, the
// start), obs_e (recovery, the end), obs_m (recovery, a moved resource version) and obs_n (routine).

const (
	accStopped = "2026-11-01T08:31:00Z"
	accScope   = "POST /api/v1/recovery/accountings"
	accAttempt = "POST /api/v1/operations/op_1/attempts/att_1/accountings"
)

func accAnswers(post string) map[string]string {
	a := map[string]string{
		"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"entry":{"enteredBy":"prn_BWSYNTH","enteredAt":"2026-11-01T08:40:00Z",` +
			`"stoppedAt":"` + accStopped + `","reason":"BWSYNTH drill","restored":[]},"exit":null,"scopes":[{"machine":"mch_a","state":"pre-restore-unaccounted"}]}`,
		"GET /api/v1/machines/mch_a/observations": `{"items":[` +
			`{"id":"obs_s","purpose":"routine","resourceVersion":"7","startedAt":"2026-11-01T09:00:00Z","at":"2026-11-01T09:00:02Z"},` +
			`{"id":"obs_e","purpose":"recovery","resourceVersion":"7","startedAt":"2026-11-01T09:01:00Z","at":"2026-11-01T09:01:02Z"},` +
			`{"id":"obs_m","purpose":"recovery","resourceVersion":"8","startedAt":"2026-11-01T09:01:00Z","at":"2026-11-01T09:01:02Z"},` +
			`{"id":"obs_n","purpose":"routine","resourceVersion":"7","startedAt":"2026-11-01T09:01:00Z","at":"2026-11-01T09:01:02Z"}]}`,
		"GET /api/v1/operations/op_1": `{"id":"op_1","kind":"apply-config","state":"unresolved","subject":{"machine":"mch_a"},` +
			`"attempts":[{"id":"att_1","number":1,"generation":2,"transportDeadline":"2026-11-01T08:50:00Z"}]}`,
		"GET /api/v1/machines/mch_a/timeline?order=newest": `{"items":[{"kind":"scope-state","revision":9,"entry":{"to":"blocked",` +
			`"facts":{"settleFloor":"30s","anchor":"2026-11-01T08:51:00Z"}}},{"kind":"accounting","revision":4,"entry":{"facts":{"settleFloor":"30s","anchor":"2026-11-01T08:50:00Z"}}}]}`,
	}
	if post != "" {
		a[accScope] = post
		a[accAttempt] = post
	}
	return a
}

const accScopeAnswer = `{"act":"act_BWSYNTH","at":"2026-11-01T09:05:00Z","decidedBy":"prn_BWSYNTH","epoch":"ep_BWSYNTH-1","role":"recovery-admin",` +
	`"scopes":[{"machine":"mch_a","state":"blocked","revision":9,"start":"obs_s","end":"obs_e","attempts":["att_1"]}]}`

const accAttemptAnswer = `{"act":"act_BWSYNTH","at":"2026-11-01T09:05:00Z","attempt":"att_1","decidedBy":"prn_BWSYNTH","end":"obs_e","epoch":"ep_BWSYNTH-1",` +
	`"operation":"op_1","revision":4,"role":"recovery-admin","start":"obs_s"}`

func conflict(detail string) string {
	return `409 {"type":"urn:bronzeward:problem:conflict","title":"conflict","status":409,"detail":"` + detail + `","instance":"req_BWSYNTH"}`
}

// account runs args with typed on standard input and returns the exit code and both streams.
func account(t *testing.T, typed string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	var code int
	withStdin(t, typed, func() { code = run(args, &out, &errb) })
	return code, out.String(), errb.String()
}

var (
	scopeArgs   = []string{"recovery", "scope-account", "mch_a", "--start", "obs_s", "--end", "obs_e"}
	attemptArgs = []string{"recovery", "account", "op_1", "att_1", "--start", "obs_s", "--end", "obs_e"}
)

// TestAccountRefusals: what the client reads for itself is refused with exit 2 before any POST.
func TestAccountRefusals(t *testing.T) {
	for _, c := range []struct {
		name, want string
		args       []string
	}{
		{"no start", "give --start and --end", []string{"recovery", "scope-account", "mch_a", "--end", "obs_e"}},
		{"no end", "give --start and --end", []string{"recovery", "account", "op_1", "att_1", "--start", "obs_s"}},
		{"end is not a recovery observation", "its purpose is routine, not recovery", []string{"recovery", "scope-account", "mch_a", "--start", "obs_s", "--end", "obs_n"}},
		{"same observation", "are the same observation", []string{"recovery", "scope-account", "mch_a", "--start", "obs_e", "--end", "obs_e"}},
		{"unknown start", "no such observation of machine mch_a", []string{"recovery", "account", "op_1", "att_1", "--start", "obs_x", "--end", "obs_e"}},
		{"moved without explanation", "moved from 7 to 8", []string{"recovery", "scope-account", "mch_a", "--start", "obs_s", "--end", "obs_m"}},
		{"explanation too long", "at most 1024 bytes", append(slices.Clone(scopeArgs), "--explanation", strings.Repeat("x", 1025))},
	} {
		t.Run(c.name, func(t *testing.T) {
			recoveryPosted = nil
			seen := recoveryServer(t, accAnswers(accScopeAnswer), accScopeAnswer)
			code, out, errb := account(t, "mch_a\n", c.args...)
			if code != 2 || out != "" || !strings.Contains(errb, c.want) || !strings.Contains(errb, "nothing was sent") {
				t.Errorf("exit %d, stdout %q, stderr:\n%s\nwant exit 2, nothing on stdout, %q and \"nothing was sent\"", code, out, errb, c.want)
			}
			if slices.Contains(seen(), accScope) || slices.Contains(seen(), accAttempt) || len(recoveryPosted) != 0 {
				t.Errorf("a POST was sent: %v", seen())
			}
		})
	}
}

// TestAccountTyped: the machine ID typed back is the only way to send; --no-input, an empty answer
// and a wrong one exit 2 and send nothing. The window, the settle floor and the risk come first.
func TestAccountTyped(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		route string
		post  string
	}{{"scope-account", scopeArgs, accScope, accScopeAnswer}, {"account", attemptArgs, accAttempt, accAttemptAnswer}} {
		t.Run(c.name, func(t *testing.T) {
			recoveryPosted = nil
			seen := recoveryServer(t, accAnswers(c.post), c.post)
			code, _, errb := account(t, "", append(slices.Clone(c.args), "--no-input")...)
			if code != 2 || slices.Contains(seen(), c.route) {
				t.Errorf("--no-input: exit %d, sent %v; want 2 and no POST\n%s", code, seen(), errb)
			}
			for _, typed := range []string{"", "\n", "mch_wrong\n", "mch_ax\n"} {
				if code, out, errb := account(t, typed, c.args...); code != 2 || out != "" || slices.Contains(seen(), c.route) {
					t.Errorf("typed %q: exit %d, sent %v; want 2 and no POST\n%s", typed, code, seen(), errb)
				}
			}
			for _, want := range []string{"obs_s, purpose routine", "obs_e, purpose recovery", "resource version 7", "Settle floor 30s", "can still land on this machine", "not a proven bound"} {
				if !strings.Contains(errb, want) {
					t.Errorf("the window lacks %q:\n%s", want, errb)
				}
			}
			code, _, errb = account(t, "mch_a\n", c.args...)
			if code != 0 || !slices.Contains(seen(), c.route) {
				t.Errorf("typed back: exit %d, sent %v\n%s", code, seen(), errb)
			}
		})
	}
}

// TestAccountRequestAndAnswer: the body names one scope or the one attempt's window and carries
// the explanation as facts; the answer shows the state and, from the timeline, the settle floor
// and the anchor the server recorded.
func TestAccountRequestAndAnswer(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		post  string
		body  string
		shows []string
	}{
		{"scope-account", append(slices.Clone(scopeArgs), "--explanation", "the node was cut off"), accScopeAnswer,
			`{"scopes":[{"end":"obs_e","machine":"mch_a","start":"obs_s"}],"facts":{"explanation":"the node was cut off"}}`,
			[]string{"Recorded. Scope mch_a is now blocked until it is marked.", "Attempts accounted: att_1.", "Settle floor 30s, counted from 2026-11-01T08:51:00Z.", "Next: recovery mark 'mch_a'."}},
		{"account", attemptArgs, accAttemptAnswer,
			`{"end":"obs_e","start":"obs_s"}`,
			[]string{"Recorded. Attempt att_1 of operation op_1 is accounted for by decision.", "changes no state", "Settle floor 30s, counted from 2026-11-01T08:50:00Z.", "Next: operation show 'op_1'."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			recoveryPosted = nil
			recoveryServer(t, accAnswers(c.post), c.post)
			code, out, errb := account(t, "mch_a\n", c.args...)
			if code != 0 || len(recoveryPosted) != 1 {
				t.Fatalf("exit %d, %d POSTs\n%s", code, len(recoveryPosted), errb)
			}
			var got, want map[string]any
			if err := json.Unmarshal([]byte(recoveryPosted[0]), &got); err != nil || json.Unmarshal([]byte(c.body), &want) != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("body %s; want %s", recoveryPosted[0], c.body)
			}
			flat := strings.Join(strings.Fields(out), " ")
			for _, s := range c.shows {
				if !strings.Contains(flat, s) {
					t.Errorf("output lacks %q:\n%s", s, out)
				}
			}
		})
	}
}

// TestAccountSettleCauses: each named refusal of the server gets exactly one next step, and the
// server's own words stay in the error.
func TestAccountSettleCauses(t *testing.T) {
	for _, c := range []struct {
		detail, next string
	}{
		{"the start observation obs_s is not the first successful read of the machine after the transport deadline", "machine observations 'mch_a'"},
		{"the end observation obs_e is not a recovery observation", "give a recovery observation as --end"},
		{"the end observation began before the settle floor of 30s had elapsed since 2026-11-01T08:51:00Z", "wait until the floor has passed"},
		{"the machine-configuration resource version changed during the settle and no explanation is recorded", "--explanation <text>"},
		{"the attempt is already accounted for", "machine timeline 'mch_a'"},
		{"the attempt's executor is not stopped or taken over", "recovery takeover 'op_1'"},
	} {
		// The executor cause comes from the attempt's settle only: it names an operation.
		for _, args := range [][]string{scopeArgs, attemptArgs} {
			if strings.Contains(c.next, "takeover") && args[1] == "scope-account" {
				continue
			}
			recoveryPosted = nil
			recoveryServer(t, accAnswers(conflict(c.detail)), "")
			code, out, errb := account(t, "mch_a\n", args...)
			if code != 1 || out != "" || !strings.Contains(errb, c.detail) || !strings.Contains(errb, c.next) {
				t.Errorf("%s: exit %d, stdout %q; want 1 with the cause and %q\n%s", args[1], code, out, c.next, errb)
			}
		}
	}
	// A scope that is not pre-restore unaccounted gets the status step, on the scope command only.
	recoveryServer(t, accAnswers(conflict("the scope of machine mch_a is not pre-restore unaccounted")), "")
	if code, _, errb := account(t, "mch_a\n", scopeArgs...); code != 1 || !strings.Contains(errb, "recovery status names the state") {
		t.Errorf("exit %d\n%s", code, errb)
	}
}

// TestAccountEpochChange: a new epoch after the reads is shown at the send. At most one POST goes
// out, the write is set aside and nothing is repeated (AMB-05, AMB-07, AMB-12).
func TestAccountEpochChange(t *testing.T) {
	recoveryPosted = nil
	recoveryEpochByPath = map[string]string{"/api/v1/machines/mch_a/observations": "ep_BWSYNTH-2"}
	t.Cleanup(func() { recoveryEpochByPath = nil })
	seen := recoveryServer(t, accAnswers(accScopeAnswer), accScopeAnswer)
	code, out, errb := account(t, "mch_a\n", scopeArgs...)
	if code == 0 || out != "" || len(recoveryPosted) > 1 || strings.Count(strings.Join(seen(), "\n"), accScope) > 1 {
		t.Errorf("exit %d, stdout %q, %d POSTs; want a refusal and at most one POST\n%s", code, out, len(recoveryPosted), errb)
	}
	if !strings.Contains(errb, "epoch") {
		t.Errorf("the epoch change is not named:\n%s", errb)
	}
}

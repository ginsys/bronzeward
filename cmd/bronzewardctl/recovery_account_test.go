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
			`{"id":"obs_n","purpose":"routine","resourceVersion":"7","startedAt":"2026-11-01T09:01:00Z","at":"2026-11-01T09:01:02Z"},` +
			`{"id":"obs_v","purpose":"recovery","startedAt":"2026-11-01T09:01:00Z","at":"2026-11-01T09:01:02Z"}]}`,
		"GET /api/v1/operations/op_1": `{"id":"op_1","kind":"apply-config","state":"unresolved","subject":{"machine":"mch_a"},` +
			`"attempts":[{"id":"att_1","number":1,"generation":2,"transportDeadline":"2026-11-01T08:50:00Z"}]}`,
		"GET /api/v1/machines/mch_a/timeline?order=newest": `{"items":[{"kind":"scope-state","revision":9,"epoch":"ep_BWSYNTH-1","entry":{"to":"blocked",` +
			`"facts":{"settleFloor":"30s","anchor":"2026-11-01T08:51:00Z"}}},{"kind":"accounting","revision":4,"epoch":"ep_BWSYNTH-1","entry":{"facts":{"settleFloor":"30s","anchor":"2026-11-01T08:50:00Z"}}}]}`,
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
		{"no version at the start", "--start obs_v: it read no machine-configuration resource version", []string{"recovery", "scope-account", "mch_a", "--start", "obs_v", "--end", "obs_e"}},
		{"no version at the end", "--end obs_v: it read no machine-configuration resource version", []string{"recovery", "account", "op_1", "att_1", "--start", "obs_s", "--end", "obs_v"}},
		{"moved without explanation", "moved from 7 to 8", []string{"recovery", "scope-account", "mch_a", "--start", "obs_s", "--end", "obs_m"}},
		{"explanation too long", "at most 1024 bytes", append(slices.Clone(scopeArgs), "--explanation", strings.Repeat("x", 1025))},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetPosted()
			seen := recoveryServer(t, accAnswers(accScopeAnswer), accScopeAnswer)
			code, out, errb := account(t, "mch_a\n", c.args...)
			if code != 2 || out != "" || !strings.Contains(errb, c.want) || !strings.Contains(errb, "nothing was sent") {
				t.Errorf("exit %d, stdout %q, stderr:\n%s\nwant exit 2, nothing on stdout, %q and \"nothing was sent\"", code, out, errb, c.want)
			}
			if slices.Contains(seen(), accScope) || slices.Contains(seen(), accAttempt) || len(posted()) != 0 {
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
			resetPosted()
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
			code, _, errb = account(t, "mch_a\n", c.args...)
			if code != 0 || !slices.Contains(seen(), c.route) {
				t.Errorf("typed back: exit %d, sent %v\n%s", code, seen(), errb)
			}
			// The window is shown to the run that asks for the typed ID; --no-input refuses before it.
			for _, want := range []string{"obs_s, purpose routine", "obs_e, purpose recovery", "resource version 7", "Settle floor 30s", "judgment that no ", "not a proven bound"} {
				if !strings.Contains(errb, want) {
					t.Errorf("the window lacks %q:\n%s", want, errb)
				}
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
			[]string{"Recorded. Scope mch_a is now blocked until it is marked.", "Attempts accounted: 1: att_1.", "Settle floor 30s, counted from 2026-11-01T08:51:00Z.", "Next: recovery mark 'mch_a'."}},
		{"account", attemptArgs, accAttemptAnswer,
			`{"end":"obs_e","start":"obs_s"}`,
			[]string{"Recorded. Attempt att_1 of operation op_1 is accounted for by decision.", "changes no state", "Settle floor 30s, counted from 2026-11-01T08:50:00Z.", "Next: operation show 'op_1'."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetPosted()
			recoveryServer(t, accAnswers(c.post), c.post)
			code, out, errb := account(t, "mch_a\n", c.args...)
			if code != 0 || len(posted()) != 1 {
				t.Fatalf("exit %d, %d POSTs\n%s", code, len(posted()), errb)
			}
			var got, want map[string]any
			if err := json.Unmarshal([]byte(posted()[0]), &got); err != nil || json.Unmarshal([]byte(c.body), &want) != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("body %s; want %s", posted()[0], c.body)
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
		{"the observation obs_e was not read at both ends of the settle: the machine-configuration resource version is missing", "give observations that both read the machine-configuration resource version"},
		{"the end observation obs_e did not begin after the start observation was recorded", "give an --end observation that began after --start was recorded"},
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
			resetPosted()
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

// TestAccountEpochChange: a new epoch after the reads is shown at the send. No POST goes out and
// nothing is repeated: a decision typed under the earlier epoch is set aside (client contracts §8.5
// item 2; AMB-05, AMB-07, AMB-12 with this write as the dropped one).
//
// The window is built from reads under ep_BWSYNTH-1. The send's own principal read, the first
// read after them, shows ep_BWSYNTH-2. A second case moves the epoch inside the window's own reads,
// at the observations page, so the earlier results shown beside it belong to another snapshot. Both
// commands are covered in both cases, since each takes the epoch of its reads.
func TestAccountEpochChange(t *testing.T) {
	for _, c := range []struct {
		name, route, post, moves, refresh, said string
		args                                    []string
	}{
		{"scope-account after the reads", accScope, accScopeAnswer, "/api/v1/identity", "", "after the request was built", scopeArgs},
		{"account after the reads", accAttempt, accAttemptAnswer, "/api/v1/identity", "", "after the request was built", attemptArgs},
		{"scope-account during the reads", accScope, accScopeAnswer, "/api/v1/machines/mch_a/observations", "", "while the window was read", scopeArgs},
		{"account during the reads", accAttempt, accAttemptAnswer, "/api/v1/machines/mch_a/observations", "", "while the window was read", attemptArgs},
		// The observations answer shows ep_BWSYNTH-2 and the GET /server it calls for shows ep_BWSYNTH-1
		// again: the last epoch equals the first, and the count of moves is what shows the change.
		{"scope-account and back", accScope, accScopeAnswer, "/api/v1/machines/mch_a/observations", "ep_BWSYNTH-1", "though the last read shows 'ep_BWSYNTH-1' as at the first", scopeArgs},
		{"account and back", accAttempt, accAttemptAnswer, "/api/v1/machines/mch_a/observations", "ep_BWSYNTH-1", "though the last read shows 'ep_BWSYNTH-1' as at the first", attemptArgs},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetPosted()
			setServerFacts(t, 3, map[string]string{c.moves: "ep_BWSYNTH-2"})
			recoveryRefreshEpoch = c.refresh
			seen := recoveryServer(t, accAnswers(c.post), c.post)
			code, out, errb := account(t, "mch_a\n", c.args...)
			if code == 0 || out != "" || len(posted()) != 0 || slices.Contains(seen(), c.route) {
				t.Errorf("exit %d, stdout %q, %d POSTs; want a refusal and no POST\n%s", code, out, len(posted()), errb)
			}
			want := "the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2'"
			if c.refresh != "" {
				want = "the epoch changed while the window was read"
			}
			if !strings.Contains(errb, want) || !strings.Contains(errb, c.said) || !strings.Contains(strings.ToLower(errb), "nothing was sent") {
				t.Errorf("the epoch change is not named, or the refusal does not say nothing was sent:\n%s", errb)
			}
		})
	}
}

// TestAccountPreflightEpochBack: the preflight reads of Send (the identity, then GET /server) show
// ep_BWSYNTH-2 and then ep_BWSYNTH-1 again. The last epoch equals the epoch of the reads, so only the
// count of moves shows that the epoch changed in between (§8.5, §8.6 item 3). Nothing is sent.
func TestAccountPreflightEpochBack(t *testing.T) {
	for _, c := range []struct {
		name, route, post string
		args              []string
	}{
		{"scope-account", accScope, accScopeAnswer, scopeArgs},
		{"account", accAttempt, accAttemptAnswer, attemptArgs},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetPosted()
			setServerFacts(t, 3, map[string]string{"/api/v1/identity": "ep_BWSYNTH-2"})
			recoveryRefreshEpoch = "ep_BWSYNTH-1"
			seen := recoveryServer(t, accAnswers(c.post), c.post)
			code, out, errb := account(t, "mch_a\n", c.args...)
			if code == 0 || out != "" || len(posted()) != 0 || slices.Contains(seen(), c.route) {
				t.Errorf("exit %d, stdout %q, %d POSTs; want a refusal and no POST\n%s", code, out, len(posted()), errb)
			}
			if !strings.Contains(errb, "the epoch changed after the request was built from reads of 'ep_BWSYNTH-1', though the last read shows 'ep_BWSYNTH-1' as at those reads") || !strings.Contains(strings.ToLower(errb), "nothing was sent") {
				t.Errorf("the epoch change is not named, or the refusal does not say nothing was sent:\n%s", errb)
			}
		})
	}
}

// TestAccountSettleFactsEpoch: the timeline entry at the answered revision is shown only when it
// carries the epoch of the answer. An entry of another epoch has the same kind and revision but
// belongs to another snapshot, so the line names the timeline instead of printing its facts.
func TestAccountSettleFactsEpoch(t *testing.T) {
	for _, c := range []struct {
		name, post string
		args       []string
	}{{"scope-account", accScopeAnswer, scopeArgs}, {"account", accAttemptAnswer, attemptArgs}} {
		t.Run(c.name, func(t *testing.T) {
			resetPosted()
			a := accAnswers(c.post)
			a["GET /api/v1/machines/mch_a/timeline?order=newest"] = strings.ReplaceAll(a["GET /api/v1/machines/mch_a/timeline?order=newest"], `"epoch":"ep_BWSYNTH-1"`, `"epoch":"ep_BWSYNTH-0"`)
			recoveryServer(t, a, c.post)
			code, out, errb := account(t, "mch_a\n", c.args...)
			flat := strings.Join(strings.Fields(out), " ")
			if code != 0 || strings.Contains(flat, "Settle floor 30s, counted from") || !strings.Contains(flat, "The settle floor and its anchor are on the machine's timeline: machine timeline 'mch_a'.") {
				t.Errorf("exit %d; want the timeline named and no facts of another epoch\n%s\n%s", code, out, errb)
			}
		})
	}
}

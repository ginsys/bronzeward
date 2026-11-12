package clitest

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newRecoveryJourneyEnv is newRecoveryEnv on a server that can mark: it has an executor, a provider
// and the Talos stand-in, so the mark reads the machine's identity and the cluster's access itself.
// The two observations the accounting runs between are still seeded, as newRecoveryEnv seeds them.
func newRecoveryJourneyEnv(t *testing.T) (*recoveryEnv, *standIn) {
	t.Helper()
	f, bao := startTimersExec(t, "viewer", "{instance: a, heartbeat: 5s, lease: 15s, absoluteExpiry: 10m, sweep: 15s}", "")
	e := &recoveryEnv{f: f, s: f.seed(), in: newInjector(t, f.base)}
	e.in.writing = true
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", e.in.URL, "--certificate-authority", e.in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	e.ctl = func(typed string, args ...string) result {
		return f.scan(f.exec(nil, typed, append([]string{"--context", "human"}, args...)...), args)
	}
	// The stand-in's endpoint is set before the recovery start, which serves only the recovery entry.
	f.store(f.humanFile(), f.mint("h-all", ""), "")
	e.in.set(nil, nil)
	st := f.newStandIn(t, bao.b, e.s, func(args ...string) result { return e.ctl("", args...) })
	f.restart("-recovery-start")
	f.store(f.humanFile(), f.mint("h-recovery", ""), "")
	f.want(e.ctl("enter\n", "recovery", "enter", "--restored", "family=database,backup=BWSYNTH-pg,taken-at=2026-11-01T08:30:00Z",
		"--stopped-at", "2026-11-01T08:31:00Z", "--reason", "BWSYNTH drill"), 0, "Recovery mode entered")
	e.start = f.seedObservation(e.s.machine, "drift", "7", 20*time.Minute)
	e.end = f.seedObservation(e.s.machine, "recovery", "7", 10*time.Minute)
	return e, st
}

// TestRecoveryJourney runs journey task T11 (operator-experience.md §13) as a script: a recovery
// administrator accounts a pre-restore-unaccounted scope, marks it and releases it, with the
// evidence stated as facts. The output is kept in testdata/journeys/t11.txt. The probe is "Is
// release an approval?", and no line of the output may say it is.
func TestRecoveryJourney(t *testing.T) {
	e, st := newRecoveryJourneyEnv(t)
	f, m := e.f, e.s.machine
	var out strings.Builder
	step := func(typed string, args ...string) result {
		r := e.ctl(typed, args...)
		for _, l := range st.leaks(r.out + r.err) {
			t.Errorf("bronzewardctl %v: output holds the stand-in's %v", args, l)
		}
		fmt.Fprintf(&out, "$ bronzewardctl %s\n%s%s[exit %d]\n\n", strings.Join(args, " "), r.err, r.out, r.code)
		return r
	}
	before := f.actCount()
	f.want(step("", "recovery", "status"), 0, m, "pre-restore-unaccounted", "recovery scope-account '"+m+"'")
	f.want(step(m+"\n", e.scopeAccount()...), 0, "Recorded. Scope "+m+" is now blocked until it is marked.", "Next: recovery mark '"+m+"'.")
	f.want(step("", "recovery", "mark", m, "--fact", "evidence=BWSYNTH restore drill read"), 0,
		"Scope "+m+" marked ready, was blocked", "Next: recovery release '"+m+"'.")
	f.want(step("", "recovery", "release", m, "--fact", "evidence=BWSYNTH restore drill read"), 0,
		"Scope "+m+" released, was ready", "A release is not an approval: it approves no plan and sends nothing.")
	f.want(step("", "recovery", "status"), 0, m)
	if n := f.actCount(); n != before+3 {
		t.Errorf("%d acts recorded by the accounting, the mark and the release, want 3", n-before)
	}
	// Only the release's own disclaimer may name an approval, and the match ignores case.
	const disclaimer = "a release is not an approval: it approves no plan and sends nothing."
	for _, line := range strings.Split(strings.ToLower(out.String()), "\n") {
		if strings.Contains(line, "approv") && !strings.HasPrefix(line, disclaimer) {
			t.Errorf("a line of the output calls a release an approval: %q", line)
		}
	}
	// The prompt ends in a space, which the repository's whitespace check refuses in a kept file.
	kept := regexp.MustCompile(`(?m)[ \t]+$`).ReplaceAllString(localTimes.ReplaceAllString(out.String(), "<local time>"), "")
	keepJourney(t, "t11", kept, f.home)
	f.scanFiles()
}

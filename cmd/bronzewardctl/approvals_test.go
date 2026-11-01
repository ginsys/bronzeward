package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const approvalJSON = `{"id":"apr_BWSYNTH","plan":"pln_BWSYNTH","epoch":"ep_BWSYNTH-2","approver":{"principal":"svc_BWSYNTH","role":"approver"},` +
	`"selfApproval":{"marked":true,"reasons":["created-plan","published","authored-change"]},"unseenOverwrite":%t}`

// TestApprove checks T21: the display, the labelled prediction and the overwrite line come before
// the prompt, on standard error; only the typed plan ID sends; standard output holds the approval.
func TestApprove(t *testing.T) {
	f := planServer(t)
	f.set(func(f *planFake) {
		f.post = func(w http.ResponseWriter, _ string) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, approvalJSON, f.drift != "")
		}
	})
	approve := []string{"approve", "pln_BWSYNTH"}
	// typed runs approve with the plan ID as standard input's one line.
	typed := func(extra ...string) (int, string, string) {
		setStdin(t, "pln_BWSYNTH\n")
		return runCtl(t, append(approve, extra...)...)
	}
	for _, c := range []struct{ name, stdin, want string }{
		{"a wrong ID", "pln_OTHER\n", "the typed text is not the plan ID pln_BWSYNTH; nothing was sent"},
		{"an empty line", "\n", "stopped: no plan ID was typed; nothing was sent"},
		{"no input at all", "", "stopped: no plan ID was typed; nothing was sent"},
	} {
		setStdin(t, c.stdin)
		code, out, errOut := runCtl(t, approve...)
		if code != 2 || out != "" || !strings.Contains(errOut, c.want) || strings.Contains(errOut, "usage:") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 2 and %q", c.name, code, out, errOut, c.want)
		}
	}
	if code, _, errOut := typed("--no-input"); code != 2 || !strings.Contains(errOut, "which --no-input forbids; nothing was sent") {
		t.Errorf("--no-input: exit %d, %s", code, errOut)
	}
	if code, _, errOut := typed("--yes"); code != 2 || !strings.Contains(errOut, "flag provided but not defined: -yes") {
		t.Errorf("--yes: exit %d, %s", code, errOut)
	}
	if b := f.took(); len(b) != 0 {
		t.Errorf("unconfirmed, yet sent %v", b)
	}

	code, out, errOut := typed()
	prompt := strings.Index(errOut, "Type the plan ID to approve, or press Enter to stop: ")
	for _, w := range []string{
		"Plan pln_BWSYNTH  proposed",
		"Changes shown below",
		"Self-approval  predicted: created-plan (you created this plan), published (you published its release), " +
			"authored-change (you authored a revision its release's draft introduced), authored-reused (you authored a revision its release reuses).",
		"Import-base authorship and automation you own are not predicted. The approval records the actual reasons; self-approval is marked and allowed.",
		"POST /api/v1/plans/pln_BWSYNTH/approvals is for a person signed in, not an automation token: the server refuses it.",
	} {
		if i := strings.Index(errOut, w); i < 0 || i > prompt && !strings.HasPrefix(w, "POST") || i < prompt && strings.HasPrefix(w, "POST") {
			t.Errorf("no %q in its place around the prompt (at %d) in\n%s", w, prompt, errOut)
		}
	}
	if code != 0 || out != "Approved (apr_BWSYNTH). Recorded self-approval reasons: created-plan, published, authored-change.\n"+
		"The controller re-checks the plan at use time; it applies it only if every check passes.\n" || strings.Contains(errOut, "Overwrites") {
		t.Errorf("approved: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if b := f.took(); len(b) != 1 || b[0] != "/plans/pln_BWSYNTH/approvals {}" {
		t.Errorf("sent %v; want one approval with an empty body", b)
	}

	f.set(func(f *planFake) { f.drift = "12" })
	code, out, errOut = typed()
	if i := strings.Index(errOut, "Overwrites     the drifted configuration of drift record 12, which no reviewer has seen.\n"+
		"               Approving records that you approve overwriting it.\n"); code != 0 || i < 0 || i > strings.Index(errOut, "Type the plan ID") ||
		!strings.Contains(out, "Overwrites  the drifted configuration, which no reviewer has seen: this approval records that you approve overwriting it.") {
		t.Errorf("a revert: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	f.set(func(f *planFake) { f.drift, f.op, f.evidence = "", "adopt", adoptEvidence })
	if code, out, _ = typed(); code != 0 || !strings.HasSuffix(out,
		"The controller records the adoption without sending anything; Applied becomes rel_BWSYNTH (adoption) once it does.\n") {
		t.Errorf("an adopt plan: exit %d, stdout %q", code, out)
	}
	if code, out, _ = typed("-o", "json"); code != 0 || out != fmt.Sprintf(approvalJSON, false)+"\n" {
		t.Errorf("-o json: exit %d, stdout %q; want the approval unchanged", code, out)
	}
}

// TestApprovalShowRevoke checks approval show and T22's revocation limits: a revoked plan, a plan
// that stays committed, and a reason refused before sending.
func TestApprovalShowRevoke(t *testing.T) {
	f := planServer(t)
	f.set(func(f *planFake) {
		f.approvalEpoch = "ep_BWSYNTH-1"
		f.post = func(w http.ResponseWriter, _ string) {
			if f.state != "committed" {
				f.state = "revoked"
			}
			fmt.Fprint(w, `{"approval":"apr_BWSYNTH","plan":"pln_BWSYNTH","machine":"mch_a","revokedBy":"h-approver","role":"approver",`+
				`"reason":"BWSYNTH","act":"act_BWSYNTH","epoch":"ep_BWSYNTH-2","at":"2026-11-01T06:10:00Z"}`)
		}
	})
	code, out, errOut := runCtl(t, "approval", "show", "apr_BWSYNTH")
	for _, w := range []string{"Approval apr_BWSYNTH of plan pln_BWSYNTH, epoch ep_BWSYNTH-1", "Approver       h-approver (approver)",
		"Self-approval  recorded reasons: none", "Overwrites     nothing unseen", "From before the last recovery entry: it authorizes nothing.",
		"Whether it was revoked, and the plan's state: bronzewardctl plan show 'pln_BWSYNTH'"} {
		if code != 0 || !strings.Contains(out, w) {
			t.Errorf("approval show: exit %d, no %q in\n%s%s", code, w, out, errOut)
		}
	}

	revoke := []string{"approval", "revoke", "apr_BWSYNTH"}
	for _, reason := range [][]string{nil, {"--reason", ""}, {"--reason", strings.Repeat("x", 1025)}} {
		if code, _, errOut := runCtl(t, append(revoke, reason...)...); code != 2 || !strings.Contains(errOut, "1 to 1024 bytes; nothing was sent") {
			t.Errorf("reason %d bytes: exit %d, %s", len(reason), code, errOut)
		}
	}
	if b := f.took(); len(b) != 0 {
		t.Errorf("sent %v", b)
	}
	f.set(func(f *planFake) { f.state, f.approval = "approved", "apr_BWSYNTH" })
	code, out, _ = runCtl(t, append(revoke, "--reason", "BWSYNTH withdrawn")...)
	if code != 0 || out != "Revocation of approval apr_BWSYNTH recorded by h-approver (approver), act act_BWSYNTH.\n"+
		"The plan pln_BWSYNTH is revoked: it cannot be committed. "+revokeLimits+"\n" {
		t.Errorf("an uncommitted approval: exit %d, stdout %q", code, out)
	}
	if b := f.took(); len(b) != 1 || b[0] != `/approvals/apr_BWSYNTH/revocations {"reason":"BWSYNTH withdrawn"}` {
		t.Errorf("sent %v", b)
	}
	f.set(func(f *planFake) { f.state = "committed" })
	code, out, _ = runCtl(t, append(revoke, "--reason", "BWSYNTH")...)
	if code != 0 || !strings.HasSuffix(out, "The plan pln_BWSYNTH stays committed to operation op_BWSYNTH-apply. A revocation ends an uncommitted "+
		"approval and undoes no commitment; it does not stop an attempt already recorded.\nFollow it with: bronzewardctl operation watch 'op_BWSYNTH-apply'\n") {
		t.Errorf("a committed plan: exit %d, stdout %q", code, out)
	}
}

package main

import (
	"encoding/json"
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
		{"a wrong ID", "pln_OTHER\n", "the typed text is not the plan ID; nothing was sent"},
		{"an empty line", "\n", "stopped: nothing was typed; nothing was sent"},
		{"no input at all", "", "stopped: nothing was typed; nothing was sent"},
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
	prompt := strings.Index(errOut, "Type the plan ID to send it, or press Enter to stop: ")
	for _, w := range []string{
		"Plan pln_BWSYNTH  proposed",
		"Changes shown below",
		"Self-approval  predicted: you created this plan and published this release, " +
			"and authored a fragment, profile or assignment revision its draft introduced and one it reuses.",
		"Import-base authorship and automation you own are not predicted. The approval records the actual reasons; self-approval is marked and allowed.",
		"POST /api/v1/plans/pln_BWSYNTH/approvals is for a person signed in, not an automation token: the server refuses it.",
	} {
		if i := strings.Index(errOut, w); i < 0 || i > prompt && !strings.HasPrefix(w, "POST") || i < prompt && strings.HasPrefix(w, "POST") {
			t.Errorf("no %q in its place around the prompt (at %d) in\n%s", w, prompt, errOut)
		}
	}
	if code != 0 || out != "Approved (apr_BWSYNTH). Recorded self-approval reasons: created-plan, published, authored-change.\n"+
		"The controller re-checks the plan at use time; it applies it only if every check passes.\n" || strings.Contains(errOut, "Overwrites") ||
		strings.Contains(errOut, "until an approver approves") {
		t.Errorf("approved: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if b := f.took(); len(b) != 1 || b[0] != "/plans/pln_BWSYNTH/approvals {}" {
		t.Errorf("sent %v; want one approval with an empty body", b)
	}

	f.set(func(f *planFake) { f.drift = "12" })
	code, out, errOut = typed()
	if i := strings.Index(errOut, "Overwrites     the drifted configuration of drift record 12, which no reviewer has seen.\n"+
		"               Approving records that you approve overwriting it.\n"); code != 0 || i < 0 || i > strings.Index(errOut, "Type the plan ID") ||
		!strings.Contains(out, "\nIt overwrites the drifted configuration, which no reviewer has seen: this approval records that you approve overwriting it.\n") {
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

// TestSelfWork checks the prediction's prose: one clause per predicted reason, the journey's own
// example, and nothing said as "did not" that a failed read left unknown.
func TestSelfWork(t *testing.T) {
	for _, c := range []struct {
		w    selfWork
		want string
	}{
		{selfWork{created: true, published: true, knowPublished: true, knowAuthored: true},
			"you created this plan and published this release, and authored no fragment, profile or assignment revision in it."},
		{selfWork{created: true, knowPublished: true, change: true},
			"you created this plan but did not publish this release, and authored a fragment, profile or assignment revision its draft introduced."},
		{selfWork{published: true, knowPublished: true, reused: true, knowAuthored: true},
			"you published this release but did not create this plan, and authored a fragment, profile or assignment revision it reuses."},
		{selfWork{knowPublished: true, knowAuthored: true},
			"you did not create this plan or publish this release, and authored no fragment, profile or assignment revision in it."},
		{selfWork{knowPublished: true}, "you did not create this plan or publish this release."},
		{selfWork{created: true}, "you created this plan."},
		{selfWork{}, "you did not create this plan."},
	} {
		if got := c.w.String(); got != c.want {
			t.Errorf("%+v: %q; want %q", c.w, got, c.want)
		}
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

// TestApprovalEpochs checks that each approval command judges the epoch from its own answer
// (client-contracts.md section 8.5). approve shows and sends nothing when the epoch moves while the
// plan is read. approval show compares the approval with its answer's epoch, not a later one. A
// revocation says nothing about what it ended when the epoch moves after its answer.
func TestApprovalEpochs(t *testing.T) {
	f := planServer(t)
	f.set(func(f *planFake) {
		f.post = func(w http.ResponseWriter, path string) {
			if strings.HasSuffix(path, "/approvals") {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, approvalJSON, false)
				return
			}
			fmt.Fprint(w, `{"approval":"apr_BWSYNTH","plan":"pln_BWSYNTH","machine":"mch_a","revokedBy":"h-approver","role":"approver",`+
				`"reason":"BWSYNTH","act":"act_BWSYNTH","epoch":"ep_BWSYNTH-2","at":"2026-11-01T06:10:00Z"}`)
		}
	})
	cases := []struct {
		name string
		back bool
		want string
	}{
		{"moved", false, "the epoch changed from ep_BWSYNTH-2 to ep_BWSYNTH-3"},
		{"moved and back", true, "the epoch changed from ep_BWSYNTH-2 and changed back"},
	}
	// The prediction's release read moves the epoch: from there on, or only for that read, so that
	// the GET /server it calls for brings ep_BWSYNTH-2 back.
	for _, c := range cases {
		f.set(func(f *planFake) {
			seen := false
			f.epochFor = func(path string) string {
				if path == "/releases/rel_BWSYNTH" || seen && !c.back {
					seen = true
					return "ep_BWSYNTH-3"
				}
				return "ep_BWSYNTH-2"
			}
		})
		setStdin(t, "pln_BWSYNTH\n")
		code, out, errOut := runCtl(t, "approve", "pln_BWSYNTH")
		if code == 0 || out != "" || !strings.Contains(errOut, c.want+" while the plan was read; nothing is shown and nothing was sent. "+
			"Approve it again with: approve 'pln_BWSYNTH'") || strings.Contains(errOut, "Self-approval") || strings.Contains(errOut, "Type the plan ID") {
			t.Errorf("approve, %s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
		if b := f.took(); len(b) != 0 {
			t.Errorf("approve, %s: sent %v", c.name, b)
		}
	}

	// The approval answer is in ep_BWSYNTH-3, as the approval is; the GET /server it calls for
	// answers ep_BWSYNTH-4, which the approval is not from.
	f.set(func(f *planFake) {
		f.approvalEpoch = "ep_BWSYNTH-3"
		f.epochFor = func(path string) string {
			switch path {
			case "/approvals/apr_BWSYNTH":
				return "ep_BWSYNTH-3"
			case "/server":
				return "ep_BWSYNTH-4"
			}
			return "ep_BWSYNTH-2"
		}
	})
	code, out, errOut := runCtl(t, "approval", "show", "apr_BWSYNTH")
	if code != 0 || !strings.Contains(out, "Approval apr_BWSYNTH of plan pln_BWSYNTH, epoch ep_BWSYNTH-3") || strings.Contains(out, "From before") {
		t.Errorf("approval show: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	// A restore after the revocation's answer can undo it.
	for _, c := range cases {
		posted := false
		f.set(func(f *planFake) {
			f.state, f.approval = "approved", "apr_BWSYNTH"
			f.epochFor = func(path string) string {
				if posted && (path != "/server" || !c.back) {
					return "ep_BWSYNTH-3"
				}
				return "ep_BWSYNTH-2"
			}
			post := f.post
			f.post = func(w http.ResponseWriter, path string) { post(w, path); posted = true; f.post = post }
		})
		code, out, errOut := runCtl(t, "approval", "revoke", "apr_BWSYNTH", "--reason", "BWSYNTH")
		if code != 0 || strings.Contains(out, "is revoked") || !strings.HasSuffix(out, c.want+" after the revocation was recorded, "+
			"so whether it still stands is not known. Read the plan with: plan show 'pln_BWSYNTH'\n"+revokeLimits+"\n") {
			t.Errorf("revoke, %s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
		f.took()
	}
}

// TestApprovalEscapes sends hostile text in each server string that approve, approval show and
// approval revoke print: no value starts a line of its own, and a printed command quotes it.
func TestApprovalEscapes(t *testing.T) {
	const bad = "x\x1b[31m\nForged: line 'q"
	const esc = `x\x1b[31m\x0aForged: line 'q`
	js := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	f := planServer(t)
	f.set(func(f *planFake) {
		f.fail = map[string]string{"/profile-revisions/prv_b": bad}
		f.post = func(w http.ResponseWriter, path string) {
			if strings.HasSuffix(path, "/approvals") {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, `{"id":`+js(bad)+`,"plan":"pln_BWSYNTH","epoch":"ep_BWSYNTH-2","approver":{"principal":"h-approver","role":"approver"},`+
					`"selfApproval":{"marked":true,"reasons":[`+js(bad)+`]},"unseenOverwrite":false}`)
				return
			}
			fmt.Fprint(w, `{"approval":`+js(bad)+`,"plan":`+js(bad)+`,"machine":"mch_a","revokedBy":`+js(bad)+`,"role":`+js(bad)+
				`,"reason":"BWSYNTH","act":`+js(bad)+`,"epoch":"ep_BWSYNTH-2","at":"2026-11-01T06:10:00Z"}`)
		}
	})
	check := func(name string, code int, out, errOut string, want ...string) {
		t.Helper()
		if code != 0 || strings.Contains(out+errOut, "\nForged") {
			t.Errorf("%s: exit %d, a forged line in stdout %q, stderr %q", name, code, out, errOut)
		}
		for _, w := range want {
			if !strings.Contains(out+errOut, w) {
				t.Errorf("%s: no %q in stdout %q, stderr %q", name, w, out, errOut)
			}
		}
	}
	setStdin(t, "pln_BWSYNTH\n")
	code, out, errOut := runCtl(t, "approve", "pln_BWSYNTH")
	check("approve", code, out, errOut, "the profile revision prv_b: ", "Approved ("+esc+"). Recorded self-approval reasons: "+esc+".")
	f.took()

	f.set(func(f *planFake) {
		f.raw = map[string]string{"/approvals/apr_BWSYNTH": `{"id":` + js(bad) + `,"plan":` + js(bad) + `,"epoch":` + js(bad) +
			`,"approver":{"principal":` + js(bad) + `,"role":` + js(bad) + `},"selfApproval":{"marked":true,"reasons":[` + js(bad) + `]},"unseenOverwrite":false}`}
	})
	code, out, errOut = runCtl(t, "approval", "show", "apr_BWSYNTH")
	check("approval show", code, out, errOut, "Approval "+esc+" of plan "+esc+", epoch "+esc, "recorded reasons: "+esc,
		`plan show 'x\x1b[31m\x0aForged: line '\''q'`)

	code, out, errOut = runCtl(t, "approval", "revoke", "apr_BWSYNTH", "--reason", "BWSYNTH")
	check("revoke", code, out, errOut, "Revocation of approval "+esc+" recorded by "+esc+" ("+esc+"), act "+esc+".", "The plan "+esc+" cannot be read: ")
}

package main

import (
	"strings"
	"testing"
)

// TestPlanShowWaitsForAnotherMachinesHostname: plan show of an approved apply-config plan says the same sentence as the
// apply row, in place of nothing, and says nothing of the kind where no holder is named.
func TestPlanShowWaitsForAnotherMachinesHostname(t *testing.T) {
	for _, c := range []struct {
		label, held, want string
	}{
		{"a held name", "node-9", "This plan waits. The last release applied to machine beta records the Kubernetes node name node-9, and this release gives the same name to machine alpha. " +
			"This plan has sent nothing to either machine. Apply the release for machine beta first, or give machine alpha another hostname."},
		{"a node name not recorded", "-", hostWaitsUnread},
		{"no holder", "", ""},
	} {
		f := planServer(t)
		f.set(func(f *planFake) {
			f.state, f.approval, f.approvalEpoch, f.held = "approved", "apr_BWSYNTH", "ep_BWSYNTH-2", c.held
		})
		code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
		if code != 0 {
			t.Fatalf("%s: exit %d, %s", c.label, code, errOut)
		}
		if c.want == "" {
			if strings.Contains(out, "This plan waits") {
				t.Errorf("%s: a plan with no holder waits:\n%s", c.label, out)
			}
			continue
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: no %q in\n%s", c.label, c.want, out)
		}
	}
}

// TestPlanShowHeldIsNotRead: a plan that is not approved shows no waiting sentence even where the position names a holder.
func TestPlanShowHeldIsNotRead(t *testing.T) {
	f := planServer(t)
	f.set(func(f *planFake) { f.state, f.held = "proposed", "node-9" })
	_, out, _ := runCtl(t, "plan", "show", "pln_BWSYNTH")
	if strings.Contains(out, "This plan waits") {
		t.Errorf("a proposed plan waits:\n%s", out)
	}
}

// TestReleaseReviewNodeNotice: the CLI's release review ends its hostname lines with the notice of A9 item 8 where no
// node-name clash was checked, as the web's nodeNotice does: null with an input the review hides, and absent. A string
// gets none, and -o json prints the review unchanged.
func TestReleaseReviewNodeNotice(t *testing.T) {
	const head = `{"release":"rel_a","machine":"mch_a","mode":"metal","configuration":"machine:\n  type: worker\n","provenance":[],"hostname":`
	answers := map[string]string{
		"GET /api/v1/machines/mch_a": `{"id":"mch_a","name":"node-a"}`,
	}
	for _, c := range []struct {
		label, host, notice string
	}{
		{"null, machine-name", `{"source":"machine-name","name":"node-a","nodeName":null}`, nodeNotCheckedHidden},
		{"absent, machine-name", `{"source":"machine-name","name":"node-a"}`, nodeNotCheckedOld},
		{"a name", `{"source":"machine-name","name":"node-a","nodeName":"node-a"}`, ""},
		{"null, none", `{"source":"none","nodeName":null}`, ""},
	} {
		answers["GET /api/v1/releases/rel_a/machines/mch_a/review"] = head + c.host + "}"
		out, _ := reviewRun(t, answers)
		for _, n := range []string{nodeNotCheckedHidden, nodeNotCheckedOld} {
			if has := strings.Contains(out, n+"\n"); has != (n == c.notice) {
				t.Errorf("%s: notice %q present = %v, want %v:\n%s", c.label, n, has, n == c.notice, out)
			}
		}
		if c.notice != "" && !strings.Contains(out, c.notice+"\nmachine:\n") {
			t.Errorf("%s: the notice is not the last hostname line:\n%s", c.label, out)
		}
	}
	out, _ := reviewRun(t, answers, "-o", "json")
	if strings.Contains(out, nodeNotCheckedOld) || strings.Contains(out, nodeNotCheckedHidden) {
		t.Errorf("-o json prints a notice: %q", out)
	}
}

// TestApplyRowWaitsForAnotherMachinesHostname: an approved plan whose position names a holder shows A9 item 8's waiting
// sentence in place of "approved already", with both machines named by name; where the node name is not recorded in the
// review, the short sentence. Without a holder the row reads as before.
func TestApplyRowWaitsForAnotherMachinesHostname(t *testing.T) {
	for _, c := range []struct {
		label, held string
		want, not   string
	}{
		{"a held name", "node-9",
			"This plan waits. The last release applied to machine beta records the Kubernetes node name node-9, and this release gives the same name to machine alpha. " +
				"This plan has sent nothing to either machine. Apply the release for machine beta first, or give machine alpha another hostname.", "approved already"},
		{"a node name not recorded", "-", hostWaitsUnread, "approved already"},
		// The node name is remote text: it is printed escaped, never as the control bytes it holds.
		{"a node name with a control byte", `node-\u001b[31m9`, "records the Kubernetes node name node-\\x1b[31m9, and", "\x1b"},
		{"no holder", "", "approved already: the controller sends it when its gates allow", "This plan waits"},
	} {
		f := applyServer(t)
		f.held = c.held
		setStdin(t, "n\n")
		_, _, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all") // the answer n stops the command with a non-zero exit
		if !strings.Contains(errOut, c.want) || strings.Contains(errOut, c.not) {
			t.Errorf("%s: want %q and not %q in\n%s", c.label, c.want, c.not, errOut)
		}
		if c.held != "" && (strings.Contains(errOut, "refused") || strings.Contains(errOut, "failed")) {
			t.Errorf("%s: a waiting plan is described as refused or failed:\n%s", c.label, errOut)
		}
		if s := strings.Join(f.took(), "\n"); strings.Contains(s, "/approvals") {
			t.Errorf("%s: approved a plan on the answer n: %s", c.label, s)
		}
	}
}

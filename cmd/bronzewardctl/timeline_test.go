package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// entry is one synthetic timeline entry, recorded at 06:00:<rev> UTC.
func entry(rev int, kind, record string) string {
	return fmt.Sprintf(`{"revision":%d,"epoch":"ep_BWSYNTH-2","at":"2026-11-01T06:00:%02dZ","kind":%q,"entry":%s}`, rev, rev, kind, record)
}

// TestMachineTimeline checks T24 against a fake server: the line of each plan and operation kind,
// another kind printed as JSON, and --follow: the newest page in revision order, the forward
// reads from tail following next, a refused cursor read again from the newest page, Ctrl-C's
// exit 130, and --follow refused with --all or --cursor.
func TestMachineTimeline(t *testing.T) {
	kinds := []string{
		entry(1, "plan", `{"plan":"pln_a","operation":"apply-config","release":"rel_a","principal":"h-all","role":"publisher"}`),
		entry(2, "approval", `{"plan":"pln_a","approval":"apr_a","principal":"h-all","role":"approver","selfApproval":{"marked":true,"reasons":["created-plan","published"]},"unseenOverwrite":false}`),
		entry(3, "approval-revocation", `{"approval":"apr_a","plan":"pln_a","principal":"h-approver","role":"approver","reason":"wrong release"}`),
		entry(4, "plan-cancellation", `{"plan":"pln_a","principal":"h-all","role":"publisher","reason":"superseded"}`),
		entry(5, "refusal", `{"transaction":"T6","comparison":"6","plan":"pln_b","operation":"op_b","cause":"the machine's Desired moved","controller":"ctl_b","generation":1}`),
		entry(6, "commitment", `{"plan":"pln_c","operation":"op_c","approval":"apr_c","observation":"obs_c","controller":"ctl_b","generation":1}`),
		entry(7, "drift", `{"observation":"obs_d","digest":"BWSYNTH"}`),
	}
	var mu sync.Mutex
	var seen []string
	refused := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"normal"}`)
			return
		case "/api/v1/machines/mch_a/timeline":
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		seen = append(seen, q.Get("order")+" "+q.Get("cursor"))
		switch q.Get("order") + " " + q.Get("cursor") {
		case " ":
			fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(kinds, ","))
		case "newest ":
			if !refused {
				fmt.Fprintf(w, `{"items":[%s,%s],"next":"older","tail":"t3"}`, entry(3, "plan", `{"plan":"pln_3"}`), entry(2, "plan", `{"plan":"pln_2"}`))
			} else {
				fmt.Fprintf(w, `{"items":[%s,%s],"tail":"t6"}`, entry(6, "plan", `{"plan":"pln_6"}`), entry(5, "plan", `{"plan":"pln_5"}`))
			}
		case "oldest t3":
			fmt.Fprintf(w, `{"items":[%s],"next":"n4","tail":"t4"}`, entry(4, "plan", `{"plan":"pln_4"}`))
		case "oldest n4":
			fmt.Fprintf(w, `{"items":[%s],"tail":"t5"}`, entry(5, "plan", `{"plan":"pln_5"}`))
		case "oldest t5":
			refused = true
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:cursor-invalid","title":"The cursor is not valid here","status":400,"instance":"urn:bronzeward:request:BWSYNTH"}`)
		case "oldest t6":
			fmt.Fprint(w, `{"items":[],"tail":"t6"}`)
			syscall.Kill(syscall.Getpid(), syscall.SIGINT) // Ctrl-C, once the follow is up to date
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:invalid-request","title":"unexpected","status":400}`)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	local, every := time.Local, followEvery
	time.Local, followEvery = time.UTC, 10*time.Millisecond
	defer func() { time.Local, followEvery = local, every }()

	var out, errb bytes.Buffer
	if code := run([]string{"machine", "timeline", "mch_a"}, &out, &errb); code != 0 {
		t.Fatalf("machine timeline: exit %d\n%s%s", code, out.String(), errb.String())
	}
	for _, want := range []string{
		"plan                 plan pln_a, operation apply-config, release rel_a, by h-all (publisher)",
		"approval             approval apr_a, plan pln_a, by h-all (approver), self-approval: created-plan, published",
		"approval-revocation  approval apr_a, plan pln_a, by h-approver (approver), reason wrong release",
		"plan-cancellation    plan pln_a, by h-all (publisher), reason superseded",
		"refusal              operation op_b, plan pln_b, comparison 6, cause the machine's Desired moved",
		"commitment           operation op_c, plan pln_c, approval apr_c, observation obs_c, controller ctl_b, generation 1",
		`drift                {"digest":"BWSYNTH","observation":"obs_d"}`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("machine timeline: no line %q in\n%s", want, out.String())
		}
	}

	out.Reset()
	errb.Reset()
	code := run([]string{"machine", "timeline", "mch_a", "--follow"}, &out, &errb)
	var revs []string
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n")[1:] {
		revs = append(revs, strings.Fields(l)[0])
	}
	if got := strings.Join(revs, " "); code != 130 || got != "2 3 4 5 (the 5 6" {
		t.Errorf("--follow: exit %d, revisions %q; want 130 and 2 3 4 5, the clear line, 5 6:\n%s%s", code, got, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "(the entries above may no longer hold; the newest are shown again)") ||
		!strings.Contains(errb.String(), "the timeline's cursor is no longer valid") || !strings.Contains(errb.String(), "Stopped following the timeline of machine mch_a") {
		t.Errorf("--follow: no clear line, notice or stop line:\n%s%s", out.String(), errb.String())
	}
	mu.Lock()
	if got := strings.Join(seen, "|"); got != " |newest |oldest t3|oldest n4|oldest t5|newest |oldest t6" {
		t.Errorf("--follow read %q; want the list, then newest, oldest from each tail following next, newest again after the refusal", got)
	}
	mu.Unlock()

	for _, args := range [][]string{{"--all"}, {"--cursor", "x"}} {
		out.Reset()
		errb.Reset()
		if code := run(append([]string{"machine", "timeline", "mch_a", "--follow"}, args...), &out, &errb); code != 2 {
			t.Errorf("--follow %v: exit %d; want 2\n%s", args, code, errb.String())
		}
	}
}

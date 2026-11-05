package main

import (
	"bytes"
	"errors"
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
		entry(6, "commitment", `{"plan":"pln_c","operation":"op_c","approval":"apr_c","observation":"obs_c","controller":"ctl_b","generation":9007199254740993}`),
		entry(7, "drift", `{"observation":"obs_d","digest":"BWSYNTH"}`),
	}
	var mu sync.Mutex
	var seen []string
	var times []time.Time // when each timeline request arrived, in step with seen
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
		times = append(times, time.Now())
		switch q.Get("order") + " " + q.Get("cursor") {
		case " ":
			fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(kinds, ","))
		case "newest ":
			if !refused {
				fmt.Fprintf(w, `{"items":[%s,%s],"next":"older","tail":"t3"}`, entry(3, "plan", `{"plan":"pln_3"}`),
					entry(2, "commitment", `{"plan":"pln_2","generation":9007199254740993}`))
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
	every := followEvery
	followEvery = 40 * time.Millisecond // long enough that a read sent with no wait shows in the spacing check
	defer func() { followEvery = every }()

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
		"commitment           operation op_c, plan pln_c, approval apr_c, observation obs_c, controller ctl_b, generation 9007199254740993",
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
	if !strings.Contains(out.String(), "plan pln_2, generation 9007199254740993") {
		t.Errorf("--follow: the generation lost its digits:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "(the entries above may no longer hold; the newest are shown again)") ||
		!strings.Contains(errb.String(), "the timeline's cursor is no longer valid") || !strings.Contains(errb.String(), "Stopped following the timeline of machine mch_a") {
		t.Errorf("--follow: no clear line, notice or stop line:\n%s%s", out.String(), errb.String())
	}
	mu.Lock()
	got := strings.Join(seen, "|")
	// Ctrl-C comes with the answer of the idle read; when the loop's 10 ms wait ends before the
	// signal is on its channel, that read repeats once more. The row is the sequence up to it.
	for strings.HasSuffix(got, "|oldest t6|oldest t6") {
		got = strings.TrimSuffix(got, "|oldest t6")
	}
	if got != " |newest |oldest t3|oldest n4|oldest t5|newest |oldest t6" {
		t.Errorf("--follow read %q; want the list, then newest, oldest from each tail following next, newest again after the refusal", got)
	}
	// Every read after the newest page waits followEvery: the first of a cycle, the next page of a
	// two-page backlog (oldest n4) and the newest page after the refused cursor.
	for i, n := 2, len(strings.Split(got, "|")); i < n; i++ {
		if gap := times[i].Sub(times[i-1]); gap < followEvery {
			t.Errorf("--follow sent %q %v after %q; want at least %v", strings.Split(got, "|")[i], gap, strings.Split(got, "|")[i-1], followEvery)
		}
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

// TestMachineTimelineRevision2 checks VAL-2127-4: a server at API revision 2 refuses order and
// serves no tail (client-contracts.md §8.6 item 6), so only the paths that send order or read tail
// are gated (§7 item 1). The plain read works and sends neither; --follow refuses before its
// header and before any request; a server that drops to revision 2 during --follow is refused on
// the next poll, after the answer that showed its new epoch, with no further timeline request.
func TestMachineTimelineRevision2(t *testing.T) {
	const refusal = "the server serves API revision 2; machine timeline --follow needs revision 3 or later"
	var mu sync.Mutex
	var queries []string // the raw query of each timeline request
	revision, epoch, moveAt := 2, "ep_BWSYNTH-2", ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", epoch)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/server":
			fmt.Fprintf(w, `{"version":"BWSYNTH","apiRevision":%d,"startMode":"normal"}`, revision)
		case "/api/v1/machines/mch_a/timeline":
			queries = append(queries, r.URL.RawQuery)
			q := r.URL.Query()
			if revision < 3 && (q.Has("order") || q.Has("tail")) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"type":"urn:bronzeward:problem:invalid-request","title":"order is not served at this revision","status":400}`)
				return
			}
			switch q.Get("order") + " " + q.Get("cursor") {
			case " ":
				fmt.Fprintf(w, `{"items":[%s]}`, entry(1, "plan", `{"plan":"pln_1"}`))
			case "newest ":
				fmt.Fprintf(w, `{"items":[%s],"tail":"t1"}`, entry(1, "plan", `{"plan":"pln_1"}`))
			case "oldest t1":
				// The answer shows a new epoch of a server that now serves revision 2.
				epoch, revision, moveAt = "ep_BWSYNTH-3", 2, "oldest t1"
				w.Header().Set("Bronzeward-Epoch", epoch)
				fmt.Fprint(w, `{"items":[],"tail":"t1"}`)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	every := followEvery
	followEvery = 10 * time.Millisecond
	defer func() { followEvery = every }()
	reset := func(rev int) {
		mu.Lock()
		defer mu.Unlock()
		queries, revision, epoch, moveAt = nil, rev, "ep_BWSYNTH-2", ""
	}
	sent := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(queries, "|")
	}

	reset(2)
	var out, errb bytes.Buffer
	if code := run([]string{"machine", "timeline", "mch_a"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "pln_1") || sent() != "" {
		t.Errorf("plain read at revision 2: exit %d, requests %q; want 0, the entry and one request with no query:\n%s%s", code, sent(), out.String(), errb.String())
	}

	reset(2)
	out.Reset()
	errb.Reset()
	if code := run([]string{"machine", "timeline", "mch_a", "--follow"}, &out, &errb); code == 0 || code == 130 ||
		!strings.Contains(errb.String(), refusal) || out.Len() != 0 || sent() != "" {
		t.Errorf("--follow at revision 2: exit %d, requests %q; want a refusal naming the revision, no stdout and no timeline request:\n%s%s", code, sent(), out.String(), errb.String())
	}

	reset(3)
	out.Reset()
	errb.Reset()
	code := run([]string{"machine", "timeline", "mch_a", "--follow"}, &out, &errb)
	mu.Lock()
	moved := moveAt
	mu.Unlock()
	if code == 0 || code == 130 || !strings.Contains(errb.String(), refusal) || moved != "oldest t1" || sent() != "order=newest|cursor=t1&order=oldest" {
		t.Errorf("--follow dropping to revision 2: exit %d, requests %q; want the refusal after the read that showed the new epoch, and no request after it:\n%s%s", code, sent(), out.String(), errb.String())
	}
}

// TestMachineTimelineInterruptedRead checks that Ctrl-C ends --follow with exit 130 even when the
// read that is in flight fails: the failure does not turn the stop into exit 1.
func TestMachineTimelineInterruptedRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"normal"}`)
		case "/api/v1/machines/mch_a/timeline":
			if q.Get("order") == "newest" {
				fmt.Fprintf(w, `{"items":[%s],"tail":"t1"}`, entry(1, "plan", `{"plan":"pln_1"}`))
				return
			}
			syscall.Kill(syscall.Getpid(), syscall.SIGINT) // Ctrl-C while this read is in flight
			time.Sleep(50 * time.Millisecond)              // the signal is on the channel before the failure is
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:invalid-request","title":"unexpected","status":400}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	every := followEvery
	followEvery = 10 * time.Millisecond
	defer func() { followEvery = every }()

	var out, errb bytes.Buffer
	if code := run([]string{"machine", "timeline", "mch_a", "--follow"}, &out, &errb); code != 130 ||
		!strings.Contains(errb.String(), "Stopped following the timeline of machine mch_a") {
		t.Errorf("--follow, Ctrl-C during a failing read: exit %d; want 130 and the stop line:\n%s%s", code, out.String(), errb.String())
	}
}

// failWriter is an output that fails as a full disk does. With on set, it fails only the writes that
// hold on and accepts every other one. Otherwise it accepts its first ok writes and then fails.
type failWriter struct {
	ok int
	on string
}

func (w *failWriter) Write(p []byte) (int, error) {
	if w.on != "" {
		if !strings.Contains(string(p), w.on) {
			return len(p), nil
		}
	} else if w.ok > 0 {
		w.ok--
		return len(p), nil
	}
	return 0, errors.New("no space left on device")
}

// TestMachineTimelineFollowWriteError checks that --follow ends with an error when its output cannot
// be written, instead of polling on with nothing shown. Each case fails one write and no other, and
// names the requests that may be sent before the follow ends: a failed header sends none, a failed
// entry line or yaml separator sends no read after the newest page, and a failed notice after a
// refused cursor sends no read after that one.
func TestMachineTimelineFollowWriteError(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	reset := false // the server refuses the cursor of an oldest read
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"normal"}`)
		case "/api/v1/machines/mch_a/timeline":
			q := r.URL.Query()
			seen = append(seen, q.Get("order")+" "+q.Get("cursor"))
			if reset && q.Get("order") == "oldest" {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"type":"urn:bronzeward:problem:cursor-invalid","title":"The cursor is not valid here","status":400,"instance":"urn:bronzeward:request:BWSYNTH"}`)
				return
			}
			fmt.Fprintf(w, `{"items":[%s],"tail":"t1"}`, entry(1, "plan", `{"plan":"pln_1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	every := followEvery
	followEvery = 10 * time.Millisecond
	defer func() { followEvery = every }()

	for _, c := range []struct {
		name  string
		out   failWriter // the output
		reset bool       // the server refuses the cursor of the first oldest read
		reads string     // the timeline requests that may be sent
		flags []string   // output flags after --follow
	}{
		{"the header", failWriter{}, false, "", nil},
		{"an entry line", failWriter{ok: 1}, false, "newest ", nil},
		{"a yaml separator", failWriter{on: "---"}, false, "newest ", []string{"-o", "yaml"}},
		{"a reset notice", failWriter{on: "may no longer hold"}, true, "newest |oldest t1", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			mu.Lock()
			seen = nil
			reset = c.reset
			mu.Unlock()
			var errb bytes.Buffer
			done := make(chan int, 1)
			args := append([]string{"machine", "timeline", "mch_a", "--follow"}, c.flags...)
			out := c.out
			go func() { done <- run(args, &out, &errb) }()
			var code int
			select {
			case code = <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("--follow kept polling after a failed write of %s", c.name)
			}
			mu.Lock()
			got := strings.Join(seen, "|")
			mu.Unlock()
			if code == 0 || code == 130 || !strings.Contains(errb.String(), "no space left on device") || got != c.reads {
				t.Errorf("failed write of %s: exit %d, requests %q; want a write error and requests %q:\n%s", c.name, code, got, c.reads, errb.String())
			}
		})
	}
}

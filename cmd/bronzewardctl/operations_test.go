package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// opFake serves one operation, op_BWSYNTH: its body, its event stream, which sends stream and
// ends, and its JSON event pages, which hold pages.
type opFake struct {
	mu            sync.Mutex
	body          string
	stream, pages []string
	reads         int           // operation reads so far
	late          string        // from the third operation read on, the epoch this answers instead
	hang          chan struct{} // when set, a stream request sends its header and then waits for this
	streams       int           // stream requests so far
	pageReqs      int           // JSON event page requests so far
	pageHang      chan struct{} // when set, a page request from number pageHangFrom on waits for this
	pageHangFrom  int
	swapFrom      int // from this operation read on (when set), the body is swapBody: the operation changed after a read
	swapBody      string
	beat          time.Duration // when set, a held stream sends a keep-alive comment this often
	lateFrom      int           // from this page request on (when set), the pages are latePages: events written after a restore
	latePages     []string
	badServer     bool          // the server read answers 200 with a body that is no JSON
	pagesFail     bool          // the JSON event pages answer 404: reading the events fails
	badReadFrom   int           // from this operation read on (when set), the answer is 200 with a body that is no JSON: an ambiguous answer
	forbidFrom    int           // from this operation read on (when set), the answer is a 403 problem: a refusal the server named
	cutFrom       int           // from this operation read on (when set), the answer is a 403 whose body stops short: a status with no problem document
	serverHang    chan struct{} // when set, the server read waits for this before it answers
	serverReqs    int           // server reads so far
	readHang      chan struct{} // when set, an operation read waits for this before it answers
	readStalls    int           // operation reads that began to wait
}

// stall holds the request until release closes or the client gives up. The caller holds f.mu.
func (f *opFake) stall(r *http.Request, release chan struct{}) {
	f.mu.Unlock()
	select {
	case <-release:
	case <-r.Context().Done():
	}
	f.mu.Lock()
}

func (f *opFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
	if r.URL.Path == "/api/v1/operations/op_BWSYNTH" {
		f.reads++
		if f.late != "" && f.reads >= 3 {
			w.Header().Set("Bronzeward-Epoch", f.late)
		}
	}
	switch strings.TrimPrefix(r.URL.Path, "/api/v1") {
	case "/server":
		w.Header().Set("Content-Type", "application/json")
		f.serverReqs++
		if f.serverHang != nil {
			f.stall(r, f.serverHang)
		}
		if f.badServer {
			fmt.Fprint(w, "not json")
			return
		}
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"normal"}`)
	case "/operations/op_BWSYNTH":
		w.Header().Set("Content-Type", "application/json")
		if f.readHang != nil {
			f.readStalls++
			f.stall(r, f.readHang)
		}
		if f.forbidFrom > 0 && f.reads >= f.forbidFrom {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:forbidden","title":"Forbidden","status":403}`)
			return
		}
		if f.cutFrom > 0 && f.reads >= f.cutFrom {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Content-Length", "200")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:forb`)
			return
		}
		if f.badReadFrom > 0 && f.reads >= f.badReadFrom {
			fmt.Fprint(w, "not json")
			return
		}
		if f.swapFrom > 0 && f.reads >= f.swapFrom {
			fmt.Fprint(w, f.swapBody)
			return
		}
		fmt.Fprint(w, f.body)
	case "/operations/op_BWSYNTH/events":
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			f.streams++
			if hang := f.hang; hang != nil {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				beat := f.beat
				f.mu.Unlock()
				var tick <-chan time.Time
				if beat > 0 {
					t := time.NewTicker(beat)
					defer t.Stop()
					tick = t.C
				}
				for held := true; held; {
					select {
					case <-hang:
						held = false
					case <-r.Context().Done():
						held = false
					case <-tick:
						fmt.Fprint(w, ": keep-alive\n\n")
						w.(http.Flusher).Flush()
					}
				}
				f.mu.Lock()
				return
			}
			for _, ev := range f.stream {
				fmt.Fprint(w, ev)
			}
			return
		}
		f.pageReqs++
		if f.pagesFail {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:not-found","title":"Not found","status":404}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if hang := f.pageHang; hang != nil && f.pageReqs >= f.pageHangFrom {
			f.mu.Unlock()
			select {
			case <-hang:
			case <-r.Context().Done():
			}
			f.mu.Lock()
		}
		pages := f.pages
		if f.lateFrom > 0 && f.pageReqs >= f.lateFrom {
			pages = f.latePages
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(pages, ","))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const opJSON = `{"id":"op_BWSYNTH","kind":%q,"state":%q,"subject":{"plan":"pln_BWSYNTH","machine":"mch_a"},"epoch":"ep_BWSYNTH-2",` +
	`"createdAt":"2026-11-01T06:00:00Z","createdBy":null,"lastEvent":3,"result":%s,"error":%s,"attempts":%s}`

// sse is one streamed event.
func sse(n int, typ, data string) string {
	return fmt.Sprintf("id: ep_BWSYNTH-2:%d\nevent: %s\ndata: %s\n\n", n, typ, data)
}

// evPage is one JSON page event, written at 06:00:0n UTC.
func evPage(n int, typ, data string) string {
	return fmt.Sprintf(`{"id":"ep_BWSYNTH-2:%d","type":%q,"at":"2026-11-01T06:00:0%dZ","data":%s}`, n, typ, n, data)
}

var (
	commitment = `{"plan":"pln_BWSYNTH","operation":"op_BWSYNTH","approval":"apr_BWSYNTH","observation":"obs_a","controller":"ctl_b","generation":3}`
	attempt    = `{"attempt":"att_a","operation":"op_BWSYNTH","number":1,"controller":"ctl_b","generation":3,"route":"10.5.0.5:50000"}`
	toState    = func(from, to, cause string) string {
		return fmt.Sprintf(`{"operation":"op_BWSYNTH","from":%q,"to":%q,"cause":%q}`, from, to, cause)
	}
	receivedAt = regexp.MustCompile(`received \d\d:\d\d:\d\d  `)
)

// TestOperationWatch checks T23 against a fake server: each outcome's line and exit code, the
// receipt-marked event lines, streamed or drained, the completion line's server time from the last
// page item, an unknown type printed raw and quoted, --timeout's exit 5, and -o json.
func TestOperationWatch(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	set := func(kind, state, result, errBody, attempts string, stream, pages []string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.body = fmt.Sprintf(opJSON, kind, state, result, errBody, attempts)
		f.stream, f.pages = stream, pages
	}
	all := []string{evPage(1, "commitment", commitment), evPage(2, "attempt", attempt), evPage(3, "operation-state", toState("verifying", "completed", ""))}
	watch := []string{"operation", "watch", "op_BWSYNTH", "--timeout", "2s"}

	set("apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]",
		[]string{sse(1, "commitment", commitment), sse(2, "attempt", attempt)}, all)
	code, out, errOut := runCtl(t, watch...)
	completedAt := "Completed at 2026-11-01 06:00:03 UTC (server): applied rel_BWSYNTH, confirmed by observation obs_c."
	for _, w := range []string{"apply-config  plan pln_BWSYNTH, machine mch_a\n", "Times marked \"received\"",
		"commitment            plan pln_BWSYNTH, approval apr_BWSYNTH, observation obs_a, controller ctl_b, generation 3\n",
		"attempt               attempt 1, route 10.5.0.5:50000, controller ctl_b, generation 3\n",
		"operation-state       from verifying, to completed\n", completedAt} {
		if !strings.Contains(out, w) {
			t.Errorf("completed: no %q in\n%s", w, out)
		}
	}
	if code != 0 || len(receivedAt.FindAllString(out, -1)) != 3 || strings.Count(out, "commitment ") != 1 || errOut != "" {
		t.Errorf("completed: exit %d, %d received lines, stdout\n%s\nstderr %q", code, len(receivedAt.FindAllString(out, -1)), out, errOut)
	}

	for _, c := range []struct{ kind, state, result, errBody, want string }{
		{"adopt", "completed", `{"adoptionRecord":"pln_BWSYNTH"}`, "null", "Completed at 2026-11-01 06:00:03 UTC (server): adopted; " +
			"the adoption of plan pln_BWSYNTH records Applied from the node's own bytes. Nothing was sent to the machine."},
		{"apply-config", "failed", "null", `{"cause":"verification-mismatch","attempts":2,"observation":"obs_d"}`,
			"Failed at 2026-11-01 06:00:03 UTC (server): verification-mismatch, after 2 attempts.\n" +
				"Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show 'mch_a'"},
		{"apply-config", "rejected", "null", `{"cause":"invalid-configuration","attempts":1}`,
			"Rejected at 2026-11-01 06:00:03 UTC (server): invalid-configuration, after 1 attempt.\n" +
				"The node refused the configuration as invalid, so it applied nothing. Review the plan's release before a new plan: bronzewardctl plan show 'pln_BWSYNTH'"},
		// A failed publish or ingest operation records the problem that ended it: no cause, a title.
		{"apply-config", "failed", "null", `{"title":"Recovery-mode entry ended the job","status":409}`,
			"Failed at 2026-11-01 06:00:03 UTC (server): Recovery-mode entry ended the job.\n" +
				"Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show 'mch_a'"},
		// The problem shows its title, its detail when present and its instance (client-contracts.md §8.2), each escaped.
		{"apply-config", "failed", "null", `{"title":"Ingestion abandoned","status":409,"detail":"the staging claim passed its lease; ingest the input again\u001b[31m","instance":"op_BWSYNTH"}`,
			"Failed at 2026-11-01 06:00:03 UTC (server): Ingestion abandoned: the staging claim passed its lease; ingest the input again\\x1b[31m (instance op_BWSYNTH).\n" +
				"Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show 'mch_a'"},
		// A cancelled operation records neither result nor error: its cause is its last event's.
		{"apply-config", "cancelled", "null", "null",
			"Cancelled at 2026-11-01 06:00:03 UTC (server): resolution.\n" +
				"Nothing more is sent for this plan. Create a new plan: bronzewardctl plan create --release <release> --machine 'mch_a' --operation <operation>"},
	} {
		pages := all
		if c.state == "cancelled" {
			pages = []string{all[0], all[1], evPage(3, "operation-state", toState("unresolved", "cancelled", "resolution"))}
		} else if c.state != "completed" { // the event that records the end names the state the operation read shows
			pages = []string{all[0], all[1], evPage(3, "operation-state", toState("verifying", c.state, ""))}
		}
		set(c.kind, c.state, c.result, c.errBody, "[]", nil, pages)
		code, out, errOut := runCtl(t, watch...)
		want := map[bool]int{true: 0, false: 4}[c.state == "completed"]
		if code != want || !strings.Contains(out, c.want+"\n") || (code == 4) != strings.Contains(errOut, "operation op_BWSYNTH ended "+c.state) {
			t.Errorf("%s: exit %d, want %d and %q:\n%s\nstderr %q", c.state, code, want, c.want, out, errOut)
		}
		wantShow := strings.ReplaceAll(c.want, " at 2026-11-01 06:00:03 UTC (server)", "")
		if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, wantShow) {
			t.Errorf("%s: operation show, exit %d:\n%s", c.state, code, show)
		}
		if c.state == "cancelled" { // show takes the cause of a cancellation from the last event, not its time
			// An operation created before a recovery has the epoch member it was created in, while the
			// answer and its events carry the current one: the cause is still its own.
			f.mu.Lock()
			f.body = strings.Replace(f.body, `"epoch":"ep_BWSYNTH-2"`, `"epoch":"ep_BWSYNTH-1"`, 1)
			f.mu.Unlock()
			if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, wantShow) {
				t.Errorf("%s: operation show of an operation created in an earlier epoch, exit %d, want %q in\n%s", c.state, code, wantShow, show)
			}
			// An event of another epoch than the operation shown is not its event: the placeholder stays.
			f.mu.Lock()
			f.pages = []string{strings.ReplaceAll(pages[2], "ep_BWSYNTH-2", "ep_BWSYNTH-3")}
			f.mu.Unlock()
			placeholder := strings.ReplaceAll(wantShow, "resolution", "no cause in the operation; its machine timeline holds the entry that ended it")
			if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, placeholder) {
				t.Errorf("%s: operation show with an event of another epoch, exit %d, want %q in\n%s", c.state, code, placeholder, show)
			}
		}
	}

	// Unresolved is not terminal: the watch goes on until --timeout, then names the state.
	set("apply-config", "unresolved", "null", "null", `[{"id":"att_a","number":1,"accounting":null}]`,
		[]string{sse(4, "operation-state", toState("sending", "unresolved", "transport-deadline")), sse(5, "BWSYNTH-new", "{\"x\":\"\\u001b[31m\",\"n\":9007199254740993}")}, nil)
	began := time.Now()
	code, out, errOut = runCtl(t, watch...)
	if took := time.Since(began); took < 2*time.Second { // the watch waits for the timer, not for the first read
		t.Errorf("unresolved: the watch ended after %s, before --timeout 2s", took)
	}
	for _, w := range []string{"operation-state       from sending, to unresolved, cause transport-deadline\n",
		`BWSYNTH-new           {"n":9007199254740993,"x":"\u001b[31m"}` + "\n",
		"Still unresolved after --timeout 2s.\nUnresolved: completion and retry safety are unknown. A recovery administrator must account for attempt 1.",
		"Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("unresolved: no %q in\n%s", w, out)
		}
	}
	if code != 5 || strings.Contains(out, "\x1b") || !strings.Contains(errOut, "operation op_BWSYNTH is still unresolved after --timeout 2s") {
		t.Errorf("unresolved: exit %d, stdout\n%q\nstderr %q", code, out, errOut)
	}

	// -o json: the lines go to standard error, the operation resource alone to standard output.
	set("apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]", nil, all)
	code, out, errOut = runCtl(t, append(watch, "-o", "json")...)
	if code != 0 || !strings.HasPrefix(out, `{"id":"op_BWSYNTH"`) || strings.Count(out, "\n") != 1 || !strings.Contains(errOut, completedAt) {
		t.Errorf("-o json: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	if code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "0s"); code != 2 || !strings.Contains(errOut, "--timeout: give a positive duration") {
		t.Errorf("--timeout 0s: exit %d, %q", code, errOut)
	}
}

// TestCancelledNextStep: the next step after a cancelled operation names the command that makes a new plan,
// never plan show (a plan whose operation was cancelled before any attempt stays committed, and plan show
// then points back at the operation). The operation holds the machine only: the release and the operation
// of the new plan are placeholders, left bare. The machine is the server's text, so it stays one quoted
// shell word, whatever it looks like, and an operation without one gets the placeholder.
func TestCancelledNextStep(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.pages = []string{evPage(3, "operation-state", toState("unresolved", "cancelled", "resolution"))}
	for _, c := range []struct{ name, subject, machine string }{
		{"a machine", `{"plan":"pln_BWSYNTH","machine":"mch_a"}`, `'mch_a'`},
		{"no machine", `{"plan":"pln_BWSYNTH"}`, `<machine>`},
		{"a machine named like a placeholder", `{"plan":"pln_BWSYNTH","machine":"<release>"}`, `'<release>'`},
		{"a machine with a quote", `{"plan":"pln_BWSYNTH","machine":"mch'a"}`, `'mch'\''a'`},
	} {
		f.mu.Lock()
		f.body = strings.Replace(fmt.Sprintf(opJSON, "apply-config", "cancelled", "null", "null", "[]"),
			`{"plan":"pln_BWSYNTH","machine":"mch_a"}`, c.subject, 1)
		f.mu.Unlock()
		want := "Nothing more is sent for this plan. Create a new plan: bronzewardctl plan create --release <release> --machine " +
			c.machine + " --operation <operation>\n"
		if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, want) || strings.Contains(show, "plan show") {
			t.Errorf("%s: operation show, exit %d, want %q and no plan show in\n%s", c.name, code, want, show)
		}
	}
}

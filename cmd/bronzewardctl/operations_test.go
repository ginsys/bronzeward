package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// opFake serves one operation, op_BWSYNTH: its body and its JSON event pages.
type opFake struct {
	mu    sync.Mutex
	body  string
	pages []string
}

func (f *opFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
	switch strings.TrimPrefix(r.URL.Path, "/api/v1") {
	case "/server":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"normal"}`)
	case "/operations/op_BWSYNTH":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, f.body)
	case "/operations/op_BWSYNTH/events":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(f.pages, ","))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const opJSON = `{"id":"op_BWSYNTH","kind":%q,"state":%q,"subject":{"plan":"pln_BWSYNTH","machine":"mch_a"},"epoch":"ep_BWSYNTH-2",` +
	`"createdAt":"2026-11-01T06:00:00Z","createdBy":null,"lastEvent":3,"result":%s,"error":%s,"attempts":%s}`

// evPage is one JSON page event, written at 06:00:0n UTC.
func evPage(n int, typ, data string) string {
	return fmt.Sprintf(`{"id":"ep_BWSYNTH-2:%d","type":%q,"at":"2026-11-01T06:00:0%dZ","data":%s}`, n, typ, n, data)
}

var toState = func(from, to, cause string) string {
	return fmt.Sprintf(`{"operation":"op_BWSYNTH","from":%q,"to":%q,"cause":%q}`, from, to, cause)
}

// TestOperationShowOutcome: operation show prints the outcome lines of a terminal operation, as the
// server last read it and without a server time, the problem that ended a publish or ingest with its
// title, its detail when present and its instance, each escaped, and a cancellation's cause from the
// event that records its end.
func TestOperationShowOutcome(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, c := range []struct{ kind, state, result, errBody, want string }{
		{"adopt", "completed", `{"adoptionRecord":"pln_BWSYNTH"}`, "null", "Completed: adopted; " +
			"the adoption of plan pln_BWSYNTH records Applied from the node's own bytes. Nothing was sent to the machine."},
		{"apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null",
			"Completed: applied rel_BWSYNTH, confirmed by observation obs_c."},
		{"apply-config", "failed", "null", `{"cause":"verification-mismatch","attempts":2,"observation":"obs_d"}`,
			"Failed: verification-mismatch, after 2 attempts.\n" +
				"Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show 'mch_a'"},
		{"apply-config", "rejected", "null", `{"cause":"invalid-configuration","attempts":1}`,
			"Rejected: invalid-configuration, after 1 attempt.\n" +
				"The node refused the configuration as invalid, so it applied nothing. Review the plan's release before a new plan: bronzewardctl plan show 'pln_BWSYNTH'"},
		// A failed publish or ingest operation records the problem that ended it: no cause, a title.
		{"apply-config", "failed", "null", `{"title":"Recovery-mode entry ended the job","status":409}`,
			"Failed: Recovery-mode entry ended the job.\n" +
				"Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show 'mch_a'"},
		// The problem shows its title, its detail when present and its instance (client-contracts.md §8.2), each escaped.
		{"apply-config", "failed", "null", `{"title":"Ingestion abandoned","status":409,"detail":"the staging claim passed its lease; ingest the input again\u001b[31m","instance":"op_BWSYNTH"}`,
			"Failed: Ingestion abandoned: the staging claim passed its lease; ingest the input again\\x1b[31m (instance op_BWSYNTH).\n" +
				"Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show 'mch_a'"},
	} {
		f.mu.Lock()
		f.body = fmt.Sprintf(opJSON, c.kind, c.state, c.result, c.errBody, "[]")
		f.mu.Unlock()
		if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, c.want) || strings.Contains(show, "\x1b") {
			t.Errorf("%s %s: operation show, exit %d, want %q in\n%s", c.kind, c.state, code, c.want, show)
		}
	}

	// A cancelled operation records neither result nor error: show takes its cause from the event that
	// records its end, and not the event's time.
	// The next step names the command that makes a new plan and never sends the reader through plan show,
	// which for a plan that is still committed points back at the operation. The operation holds the
	// machine only; the release and the operation of the new plan stay placeholders, unquoted.
	want := "Cancelled: resolution.\n" +
		"Nothing more is sent for this plan. Create a new plan: bronzewardctl plan create --release <release> --machine 'mch_a' --operation <operation>"
	f.mu.Lock()
	f.body = fmt.Sprintf(opJSON, "apply-config", "cancelled", "null", "null", "[]")
	f.pages = []string{evPage(1, "commitment", `{"plan":"pln_BWSYNTH"}`), evPage(3, "operation-state", toState("unresolved", "cancelled", "resolution"))}
	f.mu.Unlock()
	if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, want) {
		t.Errorf("cancelled: operation show, exit %d, want %q in\n%s", code, want, show)
	}
	// An operation created before a recovery has the epoch member it was created in, while the answer and
	// its events carry the current one: the cause is still its own.
	f.mu.Lock()
	f.body = strings.Replace(f.body, `"epoch":"ep_BWSYNTH-2"`, `"epoch":"ep_BWSYNTH-1"`, 1)
	f.mu.Unlock()
	if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, want) {
		t.Errorf("cancelled, created in an earlier epoch: operation show, exit %d, want %q in\n%s", code, want, show)
	}
	// An operation whose subject has no machine gets the placeholder for it too.
	f.mu.Lock()
	f.body = strings.Replace(f.body, `,"machine":"mch_a"`, "", 1)
	f.mu.Unlock()
	noMachine := strings.ReplaceAll(want, "'mch_a'", "<machine>")
	if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, noMachine) || strings.Contains(show, "plan show") {
		t.Errorf("cancelled, no subject machine: operation show, exit %d, want %q and no plan show in\n%s", code, noMachine, show)
	}
	// An event of another epoch than the operation shown is not its event: the placeholder stays.
	f.mu.Lock()
	f.pages = []string{strings.ReplaceAll(f.pages[1], "ep_BWSYNTH-2", "ep_BWSYNTH-3")}
	f.mu.Unlock()
	placeholder := strings.ReplaceAll(noMachine, "resolution", "no cause in the operation; its machine timeline holds the entry that ended it")
	if code, show, _ := runCtl(t, "operation", "show", "op_BWSYNTH"); code != 0 || !strings.Contains(show, placeholder) {
		t.Errorf("cancelled, an event of another epoch: operation show, exit %d, want %q in\n%s", code, placeholder, show)
	}
}

// TestReadLastKeepsOneOperation: the end event is read only when the operation read that follows is the
// state and the epoch asked for, so a cause never goes with the outcome of another operation.
func TestReadLastKeepsOneOperation(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.body = fmt.Sprintf(opJSON, "apply-config", "cancelled", "null", "null", "[]")
	// The pages hold an event that records a cancellation and one that records a completion; the operation
	// read stays cancelled, so only the state check of readLast can refuse the completion event.
	f.pages = []string{evPage(1, "operation-state", toState("verifying", "cancelled", "")), evPage(2, "operation-state", toState("verifying", "completed", ""))}
	e := &env{output: "table", out: io.Discard, err: io.Discard}
	c, err := e.open()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Server(); err != nil {
		t.Fatal(err)
	}
	if ev := readLast(c, "op_BWSYNTH", "cancelled", "ep_BWSYNTH-2"); ev == nil || ev.At == nil {
		t.Errorf("the state and epoch asked for: readLast gave %v; want the end event with its time", ev)
	}
	if ev := readLast(c, "op_BWSYNTH", "completed", "ep_BWSYNTH-2"); ev != nil {
		t.Errorf("another state than the operation read: readLast gave %v; want nil", ev)
	}
	if ev := readLast(c, "op_BWSYNTH", "cancelled", "ep_BWSYNTH-1"); ev != nil {
		t.Errorf("another epoch than asked for: readLast gave %v; want nil", ev)
	}
}

// TestRecordsEnd: the event that records an operation's end, by kind of operation as the server writes
// it: a state change to the state for apply-config, the adoption event for an adopt, the event named
// after the state for publish and ingest. A response recorded after the end, and the applied-change
// event after an adoption, record none.
func TestRecordsEnd(t *testing.T) {
	for _, c := range []struct {
		name  string
		ev    lastEvent
		state string
		want  bool
	}{
		{"state change to the state", lastEvent{Type: "operation-state", Data: []byte(toState("verifying", "failed", ""))}, "failed", true},
		{"state change to another state", lastEvent{Type: "operation-state", Data: []byte(toState("verifying", "failed", ""))}, "completed", false},
		{"adoption", lastEvent{Type: "adoption"}, "completed", true},
		{"adoption, not completed", lastEvent{Type: "adoption"}, "failed", false},
		{"applied-change after an adoption", lastEvent{Type: "applied-change"}, "completed", false},
		{"publish succeeded", lastEvent{Type: "succeeded"}, "succeeded", true},
		{"ingest failed", lastEvent{Type: "failed"}, "failed", true},
		{"failed event of a succeeded operation", lastEvent{Type: "failed"}, "succeeded", false},
		{"late response", lastEvent{Type: "response"}, "completed", false},
	} {
		if got := recordsEnd(c.ev, c.state); got != c.want {
			t.Errorf("%s: recordsEnd = %v, want %v", c.name, got, c.want)
		}
	}
}

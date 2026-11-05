package clitest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
)

// watchEnv is TestOperations' server: newPlanEnv's, with the seeded machine adopted (plan adopted,
// operation adoption), and the operation EVT-02 leaves sending, which EVT-05 watches.
type watchEnv struct {
	*planEnv
	adopted, adoption, sending string
}

// eventLine is the type of an event line of operation watch.
var eventLine = regexp.MustCompile(`(?m)^(?:received|server) +\d\d:\d\d:\d\d  (\S+)`)

// clearedLine is what operation watch prints when what it showed no longer holds.
const clearedLine = "(the events above no longer hold; they are shown again from the start)"

// shown are the event types of operation watch's lines in out, in order, one list for each display:
// a clear starts the next.
func shown(out string) [][]string {
	var all [][]string
	for _, part := range strings.Split(out, clearedLine) {
		var types []string
		for _, m := range eventLine.FindAllStringSubmatch(part, -1) {
			types = append(types, m[1])
		}
		all = append(all, types)
	}
	return all
}

// lockedBuffer is a process's output, which the test reads while the process writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// watch is operation watch of op on the context "human".
func (e *watchEnv) watch(op string, extra ...string) result {
	return e.ctl(append([]string{"--context", "human", "operation", "watch", op}, extra...)...)
}

// served are the IDs, types and data of op's events, read from its JSON event pages as h-all.
func (e *watchEnv) served(t *testing.T, op string) (ids, types []string, data []map[string]any) {
	t.Helper()
	for _, ev := range e.servedEvents(t, op) {
		ids, types, data = append(ids, ev.ID), append(types, ev.Type), append(data, ev.Data)
	}
	return ids, types, data
}

// servedAt are the server's times of op's events, in the order served reads them.
func (e *watchEnv) servedAt(t *testing.T, op string) (at []time.Time) {
	t.Helper()
	for _, ev := range e.servedEvents(t, op) {
		at = append(at, ev.At)
	}
	return at
}

// servedEvent is one event of op's JSON event page.
type servedEvent struct {
	ID, Type string
	At       time.Time
	Data     map[string]any
}

func (e *watchEnv) servedEvents(t *testing.T, op string) []servedEvent {
	t.Helper()
	q, _ := http.NewRequest(http.MethodGet, e.f.base+"/api/v1/operations/"+op+"/events?limit=500", nil)
	q.Header.Set("Authorization", "Bearer "+e.f.mint("h-all", ""))
	var page struct {
		Items []servedEvent
		Next  string
	}
	dec := json.NewDecoder(bytes.NewReader(e.f.do(q, http.StatusOK)))
	dec.UseNumber() // a number keeps its digits, as the CLI prints them
	if err := dec.Decode(&page); err != nil || page.Next != "" {
		t.Fatalf("the events of %s: %v, next %q", op, err, page.Next)
	}
	return page.Items
}

// printedMembers are, by event type, the members of the event's data that operation watch prints in
// its line (eventText in cmd/bronzewardctl); a type not listed prints its data as a whole.
var printedMembers = map[string][]string{
	"commitment":           {"plan", "approval", "observation", "controller", "generation"},
	"attempt":              {"number", "route", "transportDeadline", "controller", "generation"},
	"refusal":              {"plan", "comparison", "cause", "observation"},
	"use-time-check":       {"result", "plan", "controller"},
	"response":             {"number", "class", "code"},
	"accounting":           {"attempt", "basis", "class", "decidedBy", "role"},
	"classification":       {"outcome", "observation", "controller", "generation"},
	"ownership-transition": {"from", "to", "generation", "reason", "requestedBy"},
	"operation-state":      {"from", "to", "comparison", "cause", "decidedBy"},
	"adoption":             {"plan", "approval", "observation", "controller"},
	"applied-change":       {"from", "to", "source"},
}

// payloadsDiffer says how the last display of operation watch's output differs from the events the
// server holds: the line of each event must carry the printed members of that event's data, so a line
// that reuses another event's payload fails. It is "" when the lines carry the data.
func payloadsDiffer(out string, data []map[string]any) string {
	parts := strings.Split(out, clearedLine)
	lines := eventText.FindAllStringSubmatch(parts[len(parts)-1], -1)
	if len(lines) != len(data) {
		return fmt.Sprintf("%d event lines for %d events", len(lines), len(data))
	}
	for i, d := range data {
		typ, text := lines[i][1], lines[i][2]
		for _, k := range printedMembers[typ] {
			if v, ok := d[k]; ok && v != nil && !strings.Contains(text, fmt.Sprint(v)) {
				return fmt.Sprintf("event %d (%s): member %s = %v is not in its line %q", i+1, typ, k, v, text)
			}
		}
	}
	return ""
}

// replayDiffers says how the output of a watch that showed its events, cleared them and showed them
// again differs from that: two displays, the second with every event the server holds (their types
// and payloads) and more than the first. It is "" when the output is that.
func replayDiffers(out string, types []string, data []map[string]any) string {
	got := shown(out)
	switch {
	case len(got) != 2 || len(got[0]) == 0:
		return fmt.Sprintf("%d displays; want events, a clear and the events again", len(got))
	case !slices.Equal(got[1], types):
		return fmt.Sprintf("the second display shows %q; the server holds %q", got[1], types)
	case len(got[1]) <= len(got[0]):
		return fmt.Sprintf("the second display (%d events) has no event the first (%d) lacked", len(got[1]), len(got[0]))
	}
	return payloadsDiffer(out, data)
}

// eventText is the type and the text of an event line of operation watch.
var eventText = regexp.MustCompile(`(?m)^(?:received|server) +\d\d:\d\d:\d\d  (\S+) *(.*)$`)

// TestPayloadsDifferFindsAReusedPayload is the check's positive control: lines with the right types
// but the first operation-state payload twice, or a missing line, differ from the events served.
func TestPayloadsDifferFindsAReusedPayload(t *testing.T) {
	data := []map[string]any{
		{"from": "committed", "to": "sending"},
		{"from": "sending", "to": "completed", "cause": json.Number("7")},
	}
	line := func(typ, text string) string { return "received 10:00:00  " + typ + "  " + text + "\n" }
	good := line("operation-state", "from committed, to sending") + line("operation-state", "from sending, to completed, cause 7")
	reused := line("operation-state", "from committed, to sending") + line("operation-state", "from committed, to sending")
	if d := payloadsDiffer(good, data); d != "" {
		t.Errorf("the lines that carry the data differ: %s", d)
	}
	if payloadsDiffer(reused, data) == "" || payloadsDiffer(line("operation-state", "from committed, to sending"), data) == "" {
		t.Error("a reused payload or a missing line is not found")
	}
	if payloadsDiffer("a\n"+clearedLine+"\n"+good, data) != "" {
		t.Error("only the last display is compared")
	}
}

// TestReplayDiffersFindsAWrongReplay is the check's positive control: a replay that shows an old event
// twice instead of the new one, or one old payload for another, differs from the events served.
func TestReplayDiffersFindsAWrongReplay(t *testing.T) {
	types := []string{"operation-state", "operation-state", "attempt"}
	data := []map[string]any{{"from": "committed", "to": "sending"}, {"from": "sending", "to": "unresolved"}, {"number": json.Number("1")}}
	line := func(typ, text string) string { return "received 10:00:00  " + typ + "  " + text + "\n" }
	first := line("operation-state", "from committed, to sending")
	second := line("operation-state", "from sending, to unresolved") + line("attempt", "number 1")
	good := first + clearedLine + "\n" + line("operation-state", "from committed, to sending") + second
	if d := replayDiffers(good, types, data); d != "" {
		t.Errorf("the replay differs: %s", d)
	}
	redo := first + clearedLine + "\n" + line("operation-state", "from committed, to sending")
	reused := redo + line("operation-state", "from committed, to sending") + line("attempt", "number 1")
	retyped := redo + line("operation-state", "from sending, to unresolved") + line("operation-state", "number 1")
	shorter := first + clearedLine + "\n" + first
	for name, out := range map[string]string{"an old payload twice": reused, "an old type twice": retyped, "no new event": shorter, "no clear": first + second} {
		if replayDiffers(out, types, data) == "" {
			t.Errorf("%s: not found", name)
		}
	}
}

// secretsIn fails the test if the stand-in's secrets are in the output or the arguments of a
// command run outside e.ctl, as e.ctl itself checks every command it runs.
func (e *watchEnv) secretsIn(t *testing.T, r result, args []string) {
	t.Helper()
	for _, where := range []string{strings.Join(args, " "), r.out, r.err} {
		if l := e.st.leaks(where); len(l) > 0 {
			t.Errorf("bronzewardctl %v: output or argv holds the stand-in's %v", args, l)
		}
	}
}

// operationOf is the operation plan is committed to, once it is.
func (e *watchEnv) operationOf(t *testing.T, plan string) string {
	t.Helper()
	e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, plan)
	var op string
	if err := e.f.db.QueryRow(`SELECT operation FROM plan_state WHERE plan = $1`, plan).Scan(&op); err != nil {
		t.Fatal(err)
	}
	return op
}

// held is an apply-config operation of rel whose one apply the stand-in holds until release runs.
func (e *watchEnv) held(t *testing.T) (op string, release func()) {
	t.Helper()
	arrived, release := e.st.node.HoldApply(t)
	e.in.set(nil, nil)
	p := planID.FindString(e.ctl(e.create("apply-config", "--transport-deadline", "2m", "--verification-deadline", "2m", "--max-attempts", "1")...).out)
	e.approve(t, p)
	select {
	case <-arrived:
	case <-time.After(2 * time.Minute):
		t.Fatal("the apply did not reach the stand-in within two minutes")
	}
	return e.operationOf(t, p), release
}

// recovery sends a recovery-admin's POST to base, h-recovery's, and returns the answer's body.
func (f *fixture) recovery(base, path, key, body string, status int) []byte {
	f.t.Helper()
	q, _ := http.NewRequest(http.MethodPost, base+"/api/v1"+path, strings.NewReader(body))
	q.Header.Set("Authorization", "Bearer "+f.mint("h-recovery", ""))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", key)
	return f.do(q, status)
}

// epochIs waits, at most 5 s, until the server answers with epoch in Bronzeward-Epoch: a committed
// epoch is the server's answer only after the serve has read it, and a case that orders a client's
// reads against it needs that moment.
func (f *fixture) epochIs(t *testing.T, epoch string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1/server", nil)
		q.Header.Set("Authorization", "Bearer "+f.mint("h-all", ""))
		if a, err := http.DefaultClient.Do(q); err == nil {
			a.Body.Close()
			if a.Header.Get("Bronzeward-Epoch") == epoch {
				return
			}
		}
	}
	t.Errorf("the server did not answer epoch %s within 5 s", epoch)
}

// relay passes the event stream request r to the server and its answer to w, event by event. After
// each event it flushes and asks each, with the event's ID and data; it stops once each answers
// false, or the server ends the stream.
func (f *fixture) relay(t *testing.T, w http.ResponseWriter, r *http.Request, each func(id, data string) bool) {
	q, _ := http.NewRequestWithContext(r.Context(), r.Method, f.base+r.URL.RequestURI(), nil)
	q.Header = r.Header.Clone()
	a, err := http.DefaultClient.Do(q)
	if err != nil {
		t.Errorf("opening the stream: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer a.Body.Close()
	maps.Copy(w.Header(), a.Header)
	w.WriteHeader(a.StatusCode)
	lines := bufio.NewReader(a.Body)
	var id, data string
	for {
		line, err := lines.ReadString('\n')
		if err != nil {
			return
		}
		io.WriteString(w, line)
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		case line == "\n":
			w.(http.Flusher).Flush()
			if id != "" && !each(id, data) {
				return
			}
			id, data = "", ""
		}
	}
}

// another starts a second bronzeward serve on the fixture's database and configuration at another
// address, with flags, and returns its base URL and its stop; the test's end stops it too.
func (f *fixture) another(t *testing.T, flags ...string) (string, func()) {
	t.Helper()
	b, err := os.ReadFile(f.srv.cfg)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	old := []byte("listen: " + strings.TrimPrefix(f.base, "http://") + "\n")
	if !bytes.Contains(b, old) {
		t.Fatalf("the configuration holds no %q", old)
	}
	cfg := filepath.Join(t.TempDir(), "another.yaml")
	if err := os.WriteFile(cfg, bytes.Replace(b, old, []byte("listen: "+addr+"\n"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &served{cfg: cfg}
	s.cmd = exec.Command(filepath.Join(f.bin, "bronzeward"), append([]string{"serve", "-config", cfg}, flags...)...)
	s.cmd.Stdout, s.cmd.Stderr = &s.log, &s.log
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop := sync.OnceFunc(s.stop)
	t.Cleanup(func() {
		stop()
		if t.Failed() {
			t.Logf("serve %s log:\n%s", flags, s.log.String())
		}
	})
	base := "http://" + addr
	for i := 0; ; i++ {
		if r, err := http.Get(base + "/livez"); err == nil && r.StatusCode == http.StatusNoContent {
			r.Body.Close()
			return base, stop
		} else if i == 100 {
			t.Fatalf("the second serve did not answer /livez: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// operationCases are the client-contracts.md §12 cases of operation watch (#354);
// testdata/operation-cases.yaml holds their records in the order they run, and TestOperations runs
// exactly those. The machine holds one operation at a time, so each case's operation ends before
// the next case's starts, except EVT-02's, which EVT-05 watches.
var operationCases = map[string]func(*testing.T, *watchEnv){
	"PAGE-01": func(t *testing.T, e *watchEnv) {
		var pages atomic.Int32
		// The stream is answered with the JSON page, as a server without one answers, and every page
		// holds one event.
		e.in.set(func(_ http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/events") {
				q := r.URL.Query()
				q.Set("limit", "1")
				r.URL.RawQuery = q.Encode()
				r.Header.Del("Accept")
				pages.Add(1)
			}
			return false
		}, nil)
		// The CLI shows the server's time in its own local zone, and the fixture's CLI gets no TZ: this
		// run pins UTC so that the expected text does not depend on the host.
		args := []string{"--context", "human", "operation", "watch", e.adoption}
		r := e.f.scan(e.f.exec([]string{"TZ=UTC"}, "", args...), args)
		e.secretsIn(t, r, args)
		_, types, data := e.served(t, e.adoption)
		// The time on the completion line is the server's time of the event that records the end: the
		// adoption event, the first. Two events of one adoption can share a time. Then this proves the value
		// and the type order above proves the choice.
		at := e.servedAt(t, e.adoption)
		if len(at) < 2 {
			t.Fatalf("%d event times; want the adoption and applied-change events", len(at))
		}
		e.f.want(r, 0, "Completed at "+at[0].UTC().Format("2006-01-02 15:04:05 MST")+" (server): adopted; the adoption of plan "+e.adopted+
			" records Applied from the node's own bytes. Nothing was sent to the machine.\n")
		got := shown(r.out)
		// An adoption records two events: adoption and applied-change. Every event line is marked
		// "received" with this client's time; only the completion line shows the server's.
		if len(types) < 2 || len(got) != 1 || !slices.Equal(got[0], types) || strings.Count(r.out, "received ") != len(types) {
			t.Errorf("shown %q; want each of the %d events %q once, in order, marked received:\n%s", got, len(types), types, r.out)
		}
		if d := payloadsDiffer(r.out, data); d != "" {
			t.Errorf("the lines do not carry the events' data: %s\n%s", d, r.out)
		}
		// The stream request, one page per event, then one page per event again for the server time of
		// the event that records the end (readLast). An adoption's operation is inserted completed with
		// its adoption event, so that event, the first of the two, ends it (recordsEnd).
		if len(types) == 0 || types[0] != "adoption" {
			t.Errorf("events %q; want the adoption event first, as the one that records the end", types)
		}
		if n := int(pages.Load()); n != 2*len(types)+1 {
			t.Errorf("%d events requests for %d events; want the stream's, one page per event and one per event for the end event's time", n, len(types))
		}
	},

	"EVT-01": func(t *testing.T, e *watchEnv) {
		op, release := e.held(t)
		defer release()
		var mu sync.Mutex
		var first string
		var resumed []string
		var streams atomic.Int32
		// The first stream is cut after its first event; the stand-in accepts the apply once the
		// watch has opened the stream again.
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Header.Get("Accept") != "text/event-stream" {
				return false
			}
			if streams.Add(1) == 1 {
				e.f.relay(t, w, r, func(id, _ string) bool { mu.Lock(); first = id; mu.Unlock(); return false })
				return true
			}
			mu.Lock()
			resumed = append(resumed, r.Header.Get("Last-Event-ID"))
			mu.Unlock()
			release()
			return false
		}, nil)
		r := e.watch(op)
		e.f.want(r, 0, "Completed at ", " (server): applied "+e.rel+", confirmed by observation obs_")
		_, types, data := e.served(t, op)
		mu.Lock()
		defer mu.Unlock()
		if got := shown(r.out); first == "" || len(resumed) == 0 || resumed[0] != first || len(got) != 1 || !slices.Equal(got[0], types) || payloadsDiffer(r.out, data) != "" {
			t.Errorf("cut after %q, reopened with Last-Event-ID %q; shown %q, want %q once each with their data (%s)", first, resumed, got, types, payloadsDiffer(r.out, data))
		}
	},

	"AUTH-06": func(t *testing.T, e *watchEnv) {
		f := e.f
		op, release := e.held(t)
		defer release()
		// 80 s left: 20 s more than the 60 s renewal margin, which is the time the CLI has to start
		// and open the stream on this token; the server ends the stream when it expires.
		short, err := f.iss.Mint("h-all", "", time.Now().Add(80*time.Second-issuer.Lifetime))
		if err != nil {
			t.Fatal(err)
		}
		renewed := f.mint("h-all", "")
		f.store(f.humanFile(), short, "BWSYNTH-rt-6")
		f.idp.mu.Lock()
		f.idp.answer = func(rt string) (int, any) {
			if rt != "BWSYNTH-rt-6" {
				return http.StatusBadRequest, map[string]string{"error": "invalid_grant"}
			}
			return issued(renewed, "BWSYNTH-rt-7")
		}
		f.idp.mu.Unlock()
		defer func() {
			f.idp.mu.Lock()
			f.idp.answer = nil
			f.idp.mu.Unlock()
			f.store(f.humanFile(), f.mint("h-all", ""), "")
		}()
		calls := f.idp.calls.Load()
		type opened struct{ bearer, last string }
		var mu sync.Mutex
		var streams []opened
		var refused int
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if r.Header.Get("Accept") == "text/event-stream" {
				mu.Lock()
				streams = append(streams, opened{bearer, r.Header.Get("Last-Event-ID")})
				mu.Unlock()
				if r.Header.Get("Last-Event-ID") != "" {
					release()
				}
			}
			// The server's verifier accepts a token for 60 s after it expired (internal/auth skew), so
			// an operation read right after the stream ended would be answered 200. The answer the
			// record names, for a token past that time, is sent here; the read is not forwarded.
			// The read before the stream is the watch's first and is answered as usual.
			if bearer == short && r.Method == http.MethodGet && r.URL.Path == "/api/v1/operations/"+op {
				mu.Lock()
				after := len(streams) > 0
				if after {
					refused++
				}
				mu.Unlock()
				if after {
					problem(w, http.StatusUnauthorized, "unauthenticated", "")
					return true
				}
			}
			return false
		}, nil)
		r := e.watch(op)
		e.f.want(r, 0, "Completed at ", " (server): applied "+e.rel)
		_, types, data := e.served(t, op)
		mu.Lock()
		defer mu.Unlock()
		switch got := shown(r.out); {
		case len(streams) > 0 && streams[0].bearer == renewed:
			t.Error("first stream not on the expiring token: setup exceeded the 20 s tolerance")
		case len(streams) < 2 || streams[0].bearer != short || streams[0].last != "" || streams[1].bearer != renewed || streams[1].last == "":
			// The tokens are not printed: the test's output is no place for them.
			for i, s := range streams {
				t.Errorf("stream %d: the expiring token %v, the renewed one %v, Last-Event-ID %q", i, s.bearer == short, s.bearer == renewed, s.last)
			}
			t.Errorf("%d streams; want the first on the expiring token without Last-Event-ID, the next on the renewed one with it", len(streams))
		case refused != 1:
			t.Errorf("the operation read on the expiring token was answered 401 %d times; want once, then the read again on the renewed token", refused)
		case f.idp.calls.Load() != calls+1:
			t.Errorf("%d refresh redemptions; want 1", f.idp.calls.Load()-calls)
		case f.stored(f.humanFile()).RefreshToken != "BWSYNTH-rt-7":
			t.Error("the renewed sign-in was not stored")
		case len(got) != 1 || !slices.Equal(got[0], types):
			t.Errorf("shown %q; want %q once each", got, types)
		case payloadsDiffer(r.out, data) != "":
			t.Errorf("the lines do not carry the events' data: %s", payloadsDiffer(r.out, data))
		}
	},

	"EVT-03": func(t *testing.T, e *watchEnv) {
		f := e.f
		// No attempt can be recorded, so the operation stays committed, and a resolution can cancel it
		// once it is unresolved: cancelled says that nothing was sent.
		for _, q := range []string{`CREATE FUNCTION bwsynth_no_attempt() RETURNS trigger LANGUAGE plpgsql AS
				$$BEGIN RAISE EXCEPTION 'BWSYNTH: EVT-03 records no attempt'; END$$`,
			`CREATE TRIGGER bwsynth_no_attempt BEFORE INSERT ON attempt FOR EACH ROW EXECUTE FUNCTION bwsynth_no_attempt()`} {
			if _, err := f.db.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		defer func() {
			for _, q := range []string{`DROP TRIGGER bwsynth_no_attempt ON attempt`, `DROP FUNCTION bwsynth_no_attempt()`} {
				if _, err := f.db.Exec(q); err != nil {
					t.Errorf("%s: %v", q, err)
				}
			}
		}()
		e.in.set(nil, nil)
		p := planID.FindString(e.ctl(e.create("apply-config", "--transport-deadline", "2m", "--verification-deadline", "2m", "--max-attempts", "1")...).out)
		e.approve(t, p)
		op := e.operationOf(t, p)
		var gen int
		if err := f.db.QueryRow(`SELECT owner_gen FROM operation WHERE id = $1 AND state = 'committed'`, op).Scan(&gen); err != nil {
			t.Fatalf("the operation %s is not committed: %v", op, err)
		}
		unresolved := make(chan struct{})
		seen := sync.OnceFunc(func() { close(unresolved) })
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Header.Get("Accept") != "text/event-stream" {
				return false
			}
			e.f.relay(t, w, r, func(_, data string) bool {
				if strings.Contains(strings.ReplaceAll(data, " ", ""), `"to":"unresolved"`) {
					seen()
				}
				return true
			})
			return true
		}, nil)
		done := make(chan result, 1)
		go func() { done <- e.watch(op, "--timeout", "5m") }()
		f.recovery(f.base, "/operations/"+op+"/takeovers", "k-evt03-takeover-"+op,
			`{"generation":`+strconv.Itoa(gen)+`,"reason":"BWSYNTH: EVT-03 takes the operation over before its first attempt"}`, http.StatusCreated)
		select {
		case <-unresolved:
		case <-time.After(2 * time.Minute):
			t.Fatal("the stream did not send the move to unresolved within two minutes")
		}
		f.recovery(f.base, "/operations/"+op+"/resolutions", "k-evt03-resolution-"+op,
			`{"to":"cancelled","reason":"BWSYNTH: no attempt was recorded"}`, http.StatusCreated)
		var r result
		select {
		case r = <-done:
		case <-time.After(2 * time.Minute):
			t.Fatal("the watch did not end within two minutes of the resolution")
		}
		f.want(r, 4, "operation-state       from committed, to unresolved, cause takeover\n", "Cancelled at ", " (server): resolution.\n"+
			"Nothing more is sent for this plan. Create a new plan: bronzewardctl plan create --release <release> --machine '"+e.s.machine+"' --operation <operation>\n",
			"operation "+op+" ended cancelled")
		_, types, data := e.served(t, op)
		if got := shown(r.out); len(got) != 1 || !slices.Equal(got[0], types) || payloadsDiffer(r.out, data) != "" ||
			strings.Index(r.out, "to unresolved") > strings.Index(r.out, "Cancelled at") {
			t.Errorf("shown %q; want %q once each with their data (%s), unresolved before the end", got, types, payloadsDiffer(r.out, data))
		}
	},

	// EVT-02 runs the restore of a watched operation in both orders a real client can meet, each
	// made deterministic: the new epoch is committed, and the server answers it, either after the
	// reader's operation read that follows the cut stream (the reconnect then carries the stale
	// Last-Event-ID and takes the server's reset), or before it (the operation read shows the new
	// epoch, and the reader clears and reopens with none).
	"EVT-02": func(t *testing.T, e *watchEnv) {
		op, release := e.held(t)
		t.Cleanup(release)
		e.sending = op
		for _, c := range []struct {
			name     string
			beforeOp bool // the new epoch is committed before the operation read, not at the reconnect
		}{{"stale Last-Event-ID, the server's reset", false}, {"new epoch seen first, the reader's clear", true}} {
			var mu sync.Mutex
			var first, resumed string
			var streams atomic.Int32
			// The first stream is cut after its first event.
			e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
				if r.Header.Get("Accept") != "text/event-stream" {
					return false
				}
				if streams.Add(1) == 1 {
					e.f.relay(t, w, r, func(id, _ string) bool {
						mu.Lock()
						first = id
						mu.Unlock()
						if c.beforeOp {
							e.f.epochIs(t, e.f.newEpoch())
						}
						return false
					})
					return true
				}
				mu.Lock()
				if resumed == "" {
					resumed = r.Header.Get("Last-Event-ID")
				}
				mu.Unlock()
				if !c.beforeOp && streams.Load() == 2 {
					e.f.epochIs(t, e.f.newEpoch())
				}
				return false
			}, nil)
			r := e.watch(op, "--timeout", "20s")
			e.f.want(r, 5, clearedLine, "restored or entered recovery mode; the operation's events are shown again from the start",
				"Still sending after --timeout 20s.\n", "Follow it again with: bronzewardctl operation watch '"+op+"'\n")
			_, types, data := e.served(t, op)
			mu.Lock()
			wantResumed := first // the reader sent the stale ID, or none when the operation read showed the new epoch
			if c.beforeOp {
				wantResumed = ""
			}
			if got := shown(r.out); first == "" || streams.Load() < 2 || resumed != wantResumed || len(got) != 2 || len(got[0]) != 1 || !slices.Equal(got[1], types) || payloadsDiffer(r.out, data) != "" {
				t.Errorf("%s: cut after %q, reopened with Last-Event-ID %q (want %q); shown %q, want one event, a clear, then %q with their data (%s)", c.name, first, resumed, wantResumed, got, types, payloadsDiffer(r.out, data))
			}
			mu.Unlock()
		}
	},

	// EVT-05 watches EVT-02's operation, which a restarted serve takes over, so its controller holds
	// the epoch, and ends the watch with Ctrl-C, which stops only the watcher.
	"EVT-05": func(t *testing.T, e *watchEnv) {
		f, op := e.f, e.sending
		if op == "" {
			t.Fatal("EVT-02 left no operation")
		}
		f.restart()
		e.until(t, "unresolved", `SELECT state FROM operation WHERE id = $1`, op)
		var mu sync.Mutex
		var lasts []string
		opened, reopened := make(chan struct{}), make(chan struct{})
		first, again := sync.OnceFunc(func() { close(opened) }), sync.OnceFunc(func() { close(reopened) })
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Header.Get("Accept") != "text/event-stream" {
				return false
			}
			mu.Lock()
			lasts = append(lasts, r.Header.Get("Last-Event-ID"))
			n := len(lasts)
			mu.Unlock()
			if n == 1 {
				first()
			} else {
				again()
			}
			return false
		}, nil)
		args := []string{"--context", "human", "operation", "watch", op, "--timeout", "5m"}
		cmd := f.command(nil, args...)
		var out lockedBuffer
		var errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer cmd.Process.Kill()
		select {
		case <-opened:
		case <-time.After(time.Minute):
			t.Fatal("the watch opened no stream within a minute")
		}
		// The recovery entry on another process, a recovery start over the same database, while the
		// stream stays open; then a normal start, whose controller takes the operation over in the
		// new epoch, which writes the stream's next event.
		recov, stopRecov := f.another(t, "-recovery-start")
		at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
		f.recovery(recov, "/recovery/entries", "k-evt05-recovery-entry", `{"restored":[{"family":"database","backup":"BWSYNTH-pg",`+
			`"takenAt":"`+at(time.Hour)+`"}],"stoppedAt":"`+at(time.Minute)+`","reason":"BWSYNTH: EVT-05 restore drill"}`, http.StatusCreated)
		stopRecov()
		f.another(t)
		select {
		case <-reopened:
		case <-time.After(2 * time.Minute):
			t.Fatal("the watch did not open the stream again within two minutes of the entry")
		}
		// The second stream's request comes before its events: Ctrl-C waits until the watch shows
		// them again, with the new one.
		for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
			_, types, data := e.served(t, op)
			if replayDiffers(out.String(), types, data) == "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the watch did not show the events again with the new one within a minute:\n%s", out.String())
			}
		}
		cmd.Process.Signal(os.Interrupt)
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		r := result{code: -1}
		select {
		case err := <-waited:
			var exit *exec.ExitError
			if r.code = 0; errors.As(err, &exit) {
				r.code = exit.ExitCode()
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the watch did not end within 30 s of Ctrl-C")
		}
		r.out, r.err = out.String(), errb.String()
		f.scan(r, args)
		e.secretsIn(t, r, args) // this run bypasses e.ctl, which checks the stand-in's secrets
		// The stop line has two designed forms, by whether the watch returned within the grace time
		// after Ctrl-C ("is still unresolved, as the server last read it") or not ("the state it has
		// now was not read"), which depends on an event or keep-alive on the quiet stream. The row
		// proves the clear and the reopening; the stop asserts what both forms share.
		f.want(r, 130, clearedLine, "entered recovery mode or was restored while the events were read",
			"Stopped watching at your request. Operation "+op+" ", "goes on", "Follow it again with: bronzewardctl operation watch '"+op+"'\n")
		var state string
		if err := f.db.QueryRow(`SELECT state FROM operation WHERE id = $1`, op).Scan(&state); err != nil || state != "unresolved" {
			t.Errorf("after Ctrl-C the operation is %q, %v; want unresolved", state, err)
		}
		mu.Lock()
		defer mu.Unlock()
		_, types, data := e.served(t, op)
		if d := replayDiffers(r.out, types, data); len(lasts) < 2 || lasts[0] != "" || lasts[1] != "" || d != "" {
			t.Errorf("streams opened with Last-Event-ID %q; the replay: %s; want the events, a clear and the events again, with the new one and the payloads served", lasts, d)
		}
	},
}

// newWatchEnv is newPlanEnv's server with the seeded machine adopted: an adopt plan of rel,
// approved by h-approver, whose adoption completed.
func newWatchEnv(t *testing.T) *watchEnv {
	t.Helper()
	e := &watchEnv{planEnv: newPlanEnv(t)}
	e.in.set(nil, nil)
	e.adopted = planID.FindString(e.ctl(e.create("adopt")...).out)
	e.approve(t, e.adopted)
	e.adoption = e.operationOf(t, e.adopted)
	if state := e.f.ended(e.adoption); state != "completed" {
		t.Fatalf("the adoption %s ended %s", e.adoption, state)
	}
	return e
}

// TestOperations runs operation watch (#354, decision T23 of #258) through the CLI against a
// server with a provider and a Talos stand-in: the cases of testdata/operation-cases.yaml, in their
// order, after the seeded machine's adoption.
func TestOperations(t *testing.T) {
	ids := cases(t, "testdata/operation-cases.yaml", operationCases)
	e := newWatchEnv(t)
	for _, id := range ids {
		if !t.Run(id, func(t *testing.T) { operationCases[id](t, e) }) {
			break // each case's operation must end for the next one's to start
		}
	}
	e.f.scanFiles()
}

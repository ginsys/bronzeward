package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
)

// lateCtrlC is a Ctrl-C context that is not cancelled while a request runs and reports the Ctrl-C only
// afterwards, as a signal does that arrives as the read ends.
type lateCtrlC struct {
	context.Context
	taken *atomic.Bool
}

func (l lateCtrlC) Err() error {
	if l.taken.Load() {
		return context.Canceled
	}
	return nil
}

// TestContextAddStopsOnACtrlCTakenAsTheProbeEnds: /livez answers and the Ctrl-C comes as the answer
// does. The probe may answer 204 or a refusal: the command stops with the Ctrl-C either way, and
// records no context.
func TestContextAddStopsOnACtrlCTakenAsTheProbeEnds(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var taken atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				taken.Store(true) // the Ctrl-C is taken before the answer reaches the client
				w.WriteHeader(status)
			}))
			defer srv.Close()
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			t.Setenv("XDG_STATE_HOME", home)
			tok := filepath.Join(home, "token")
			if err := os.WriteFile(tok, []byte("bwt_BWSYNTH.BWSYNTH-secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			e := &env{output: "table", out: &out, err: &errOut, heldCtrlC: lateCtrlC{context.Background(), &taken}}
			err := e.contextAdd([]string{"--server", srv.URL, "--automation-token-file", tok, "late"})
			var cut *client.Interrupted
			if !errors.As(err, &cut) || cut.Route != "GET /livez" {
				t.Errorf("error %v; want the probe stopped by the Ctrl-C taken as it ended", err)
			}
			cfg, lerr := client.LoadConfig()
			if lerr != nil || cfg.Find("late") != nil {
				t.Errorf("config %v, error %v; want no context \"late\" recorded", cfg, lerr)
			}
			if out.Len() != 0 {
				t.Errorf("stdout %q; want nothing", out.String())
			}
		})
	}
}

// TestOperationShowStopsOnCtrlCDuringTheEventRead: a cancelled operation's cause is read from its events,
// and the events never answer. The Ctrl-C ends the command with exit 130 and the stop line, and nothing
// of the operation is printed (it exited 0 with the operation shown before).
func TestOperationShowStopsOnCtrlCDuringTheEventRead(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "cancelled", "null", "null", "[]"), pageHang: make(chan struct{}), pageHangFrom: 1}
	srv := httptest.NewServer(f)
	defer srv.Close()
	defer close(f.pageHang)
	automationContext(t, srv.URL)
	go func() {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			f.mu.Lock()
			n := f.pageReqs
			f.mu.Unlock()
			if n > 0 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
				return
			}
		}
	}()
	start := time.Now()
	code, out, errOut := runCtl(t, "operation", "show", "op_BWSYNTH")
	const stop = "Stopped at your request while reading GET /api/v1/operations/op_BWSYNTH/events"
	if code != 130 || out != "" || !strings.HasPrefix(errOut, stop) {
		t.Errorf("exit %d, stdout %q, stderr %q; want 130, no output and the stop line %q...", code, out, errOut, stop)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the command took %s; want a few seconds at most", d)
	}
}

// stalledServer takes every request and never answers. arrived gets one value for each request.
func stalledServer(t *testing.T) (srv *httptest.Server, arrived chan struct{}) {
	t.Helper()
	arrived, hang := make(chan struct{}, 16), make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); srv.Close() })
	return srv, arrived
}

// TestReadCtrlCExits130WithOneStopLine: a read command whose server never answers. The first Ctrl-C
// ends it within a few seconds, not after the retries and the request timeout of one minute each
// (#643). It exits 130 and prints one stop line, naming the request it was reading, and nothing else.
func TestReadCtrlCExits130WithOneStopLine(t *testing.T) {
	srv, arrived := stalledServer(t)
	automationContext(t, srv.URL)
	go func() {
		<-arrived // the handler is armed before the request is sent
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	start := time.Now()
	code, out, errOut := runCtl(t, "cluster", "list")
	const stop = "Stopped at your request while reading GET /api/v1/"
	if code != 130 || out != "" || !strings.HasPrefix(errOut, stop) || strings.Count(errOut, "\n") != 1 {
		t.Errorf("exit %d, stdout %q, stderr %q; want 130, no output and the one stop line %q...", code, out, errOut, stop)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the command took %s after the Ctrl-C; want a few seconds at most", d)
	}
}

// TestContextAddFetchEndsOnCtrlC: context add --from reads bronzeward-client.json before the probe,
// and that file never comes. The Ctrl-C ends the command with exit 130 and the one stop line, not
// with the default action of SIGINT (a reviewer's finding on the first publication of #643).
func TestContextAddFetchEndsOnCtrlC(t *testing.T) {
	srv, arrived := stalledServer(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	tok := filepath.Join(home, "token")
	if err := os.WriteFile(tok, []byte("bwt_BWSYNTH.BWSYNTH-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		<-arrived // the handler is armed before the request is sent
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	code, out, errOut := runCtl(t, "context", "add", "--server", srv.URL, "--from", srv.URL, "--automation-token-file", tok, "lab")
	const stop = "Stopped at your request while reading GET /bronzeward-client.json.\n"
	if code != 130 || out != "" || errOut != stop {
		t.Errorf("exit %d, stdout %q, stderr %q; want 130, no output and %q", code, out, errOut, stop)
	}
	if cfg, err := client.LoadConfig(); err != nil || cfg.Find("lab") != nil {
		t.Errorf("config %v, error %v; want no context \"lab\" recorded", cfg, err)
	}
}

// TestRecoveryStatusStopsOnCtrlCInTheHolderLookup: recovery status looks up the operation that holds an
// unresolved scope, as a best effort that turns any failure into advice. The lookup never answers and
// the Ctrl-C ends it. The command exits 130 with the stop line, not 0 (the helper dropped the error).
func TestRecoveryStatusStopsOnCtrlCInTheHolderLookup(t *testing.T) {
	arrived, hang := make(chan struct{}, 16), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"recovery"}`)
		case "/api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case "/api/v1/recovery":
			fmt.Fprint(w, `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_b","state":"unresolved"}]}`)
		case "/api/v1/operations":
			arrived <- struct{}{}
			select {
			case <-hang:
			case <-r.Context().Done():
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	defer close(hang)
	automationContext(t, srv.URL)
	go func() {
		<-arrived
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	code, _, errOut := runCtl(t, "recovery", "status")
	const stop = "Stopped at your request while reading GET /api/v1/operations"
	if code != 130 || !strings.HasPrefix(errOut, stop) || strings.Count(errOut, "\n") != 1 {
		t.Errorf("exit %d, stderr %q; want 130 and the one stop line %q...", code, errOut, stop)
	}
}

// TestAfterCutKeepsOtherFailuresAndWriteOutcomes: a Ctrl-C taken by a read wins the exit code, but an
// error the command also returned is printed first, and an unknown write outcome keeps its exit 3.
func TestAfterCutKeepsOtherFailuresAndWriteOutcomes(t *testing.T) {
	first := &client.Interrupted{Route: "GET /api/v1/clusters"}
	for _, tc := range []struct {
		name string
		err  error
		want int
		line string
	}{
		{"no error", nil, 130, ""},
		{"an ordinary error", errors.New("the lookup failed"), 130, "bronzewardctl: the lookup failed\n"},
		{"an unknown write outcome", &client.Unknown{}, 3, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &env{}
			e.cutNow(first)
			var out, errOut bytes.Buffer
			err := e.afterCut(tc.err, &errOut)
			code := report(err, &out, &errOut)
			if code != tc.want || !strings.HasPrefix(errOut.String(), tc.line) {
				t.Errorf("exit %d, stderr %q; want exit %d after %q", code, errOut.String(), tc.want, tc.line)
			}
			if tc.want == 130 && !strings.HasSuffix(errOut.String(), "Stopped at your request while reading GET /api/v1/clusters.\n") {
				t.Errorf("stderr %q; want it to end with the stop line of the first Ctrl-C", errOut.String())
			}
		})
	}
}

// TestReportKeepsWhatIsJoinedWithACut: report keeps every error that a command joined with the Ctrl-C
// of a read. An unknown outcome keeps exit 3 and its text. Any other error is printed before the stop
// line, which ends the output, and the exit stays 130.
func TestReportKeepsWhatIsJoinedWithACut(t *testing.T) {
	cut := &client.Interrupted{Route: "GET /api/v1/clusters"}
	stop := "Stopped at your request while reading GET /api/v1/clusters.\n"
	for _, tc := range []struct {
		name string
		err  error
		want int
		seen string // text that must show on standard error
	}{
		{"a cut alone", cut, 130, ""},
		{"a wrapped cut alone", fmt.Errorf("reading the plan: %w", cut), 130, ""},
		{"an unknown write outcome", errors.Join(&client.Unknown{}, cut), 3, "stopped at your request"},
		{"an unknown outcome of an operation", errors.Join(outcomeUnknown{errors.New("the mark may be applied")}, cut), 3, "the mark may be applied"},
		{"a blocked mark and a failed draft read", errors.Join(&client.Blocked{Err: errors.New("the mark was not applied")}, cut), 130, "the mark was not applied"},
		{"a failed state save", errors.Join(cut, errors.New("saving the state failed")), 130, "saving the state failed"},
		{"a failed output write", errors.Join(cut, outputFailed{errors.New("write /dev/stdout: no space left on device")}), 130, "no space left on device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := report(tc.err, &out, &errOut)
			if code != tc.want || !strings.Contains(errOut.String(), tc.seen) {
				t.Errorf("exit %d, stderr %q; want exit %d showing %q", code, errOut.String(), tc.want, tc.seen)
			}
			if tc.want == 130 && (!strings.HasSuffix(errOut.String(), stop) || strings.Count(errOut.String(), "Stopped at your request") != 1) {
				t.Errorf("stderr %q; want one stop line, at the end", errOut.String())
			}
		})
	}
}

// TestArmAfterACutEndsAtOnce: after a read recorded its Ctrl-C, a later read gets a context that has
// ended and a disarm that says so, and the first cut stays the one the run reports.
func TestArmAfterACutEndsAtOnce(t *testing.T) {
	e := &env{}
	e.cutNow(&client.Interrupted{Route: "GET /api/v1/clusters"})
	ctx, disarm := e.arm()
	if ctx.Err() == nil || !disarm() {
		t.Errorf("context error %v, disarm false; want an ended context and a taken Ctrl-C", ctx.Err())
	}
	e.cutNow(&client.Interrupted{Route: "GET /api/v1/machines"})
	if got := e.cutNow(nil).Route; got != "GET /api/v1/clusters" {
		t.Errorf("cut %q; want the first, GET /api/v1/clusters", got)
	}
}

// TestCtrlCIsNotHeldBetweenReads: until a read has recorded its Ctrl-C (cutNow), arm hands out a
// handler for each read. Once it did, every later read ends at once (TestArmAfterACutEndsAtOnce).
func TestCtrlCIsNotHeldBetweenReads(t *testing.T) {
	e := &env{}
	for i := 0; i < 2; i++ {
		ctx, disarm := e.arm()
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT) // the handler is set by arm
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("read %d: the Ctrl-C did not reach the context within 5 s", i+1)
		}
		if !disarm() {
			t.Errorf("read %d: disarm says no Ctrl-C was taken", i+1)
		}
	}
}

// listedErrors is a custom join: Unwrap returns its members, a nil one included. errors.Join never
// keeps a nil member, so only a type like this one can carry it.
type listedErrors []error

func (l listedErrors) Error() string   { return "listed errors" }
func (l listedErrors) Unwrap() []error { return l }

// TestReportListsEachErrorBesideACutOnce (ginsys/bronzeward#670): whatever shape holds the Ctrl-C of a
// read, each other error shows once, before the one stop line, and the exit stays 130. The shapes are a
// join under a wrapper that is not itself a join, a nil member of a custom join, and one error joined
// twice, at the top or in a nested join.
func TestReportListsEachErrorBesideACutOnce(t *testing.T) {
	cut := &client.Interrupted{Route: "GET /api/v1/clusters"}
	stop := "Stopped at your request while reading GET /api/v1/clusters.\n"
	readErr, save := errors.New("GET /server answered 503"), errors.New("saving the state failed")
	for _, tc := range []struct {
		name string
		err  error
		seen map[string]int // text on standard error and how often it shows
	}{
		{"a join under a wrapper", fmt.Errorf("reading the review: %w", errors.Join(readErr, cut)), map[string]int{"answered 503": 1}},
		{"a join under two wrappers", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errors.Join(cut, readErr))), map[string]int{"answered 503": 1}},
		{"a nil member", listedErrors{nil, cut, save}, map[string]int{"saving the state failed": 1}},
		{"a nil member alone with the cut", listedErrors{cut, nil}, map[string]int{"listed errors": 0}},
		{"one error twice", errors.Join(cut, save, save), map[string]int{"saving the state failed": 1}},
		{"one error twice in a nested join", errors.Join(save, errors.Join(cut, save)), map[string]int{"saving the state failed": 1}},
		{"two different errors keep both", errors.Join(readErr, cut, save), map[string]int{"answered 503": 1, "saving the state failed": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := report(tc.err, &out, &errOut)
			got := errOut.String()
			if code != 130 || strings.Count(got, "Stopped at your request") != 1 || !strings.HasSuffix(got, stop) {
				t.Errorf("exit %d, stderr %q; want 130 and the one stop line last", code, got)
			}
			for text, n := range tc.seen {
				if c := strings.Count(got, text); c != n {
					t.Errorf("stderr %q shows %q %d times; want %d", got, text, c, n)
				}
			}
		})
	}
}

// TestFailedSaveAfterACtrlCOfAReadIsPrinted (ginsys/bronzeward#647, item 2): the first read of the
// run showed the epoch, so the second read's answer shows a new one and the client has state to keep.
// The configuration directory becomes read-only, the second read stalls, and the Ctrl-C ends it. The
// command exits 130, prints the failed save once, and ends with the one stop line.
func TestFailedSaveAfterACtrlCOfAReadIsPrinted(t *testing.T) {
	var stall atomic.Bool
	arrived, hang := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/server") {
			w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
			return
		}
		if !stall.Load() {
			w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		arrived <- struct{}{}
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); srv.Close() })
	automationContext(t, srv.URL)
	if code, _, errOut := runCtl(t, "cluster", "list"); code != 0 {
		t.Fatalf("first read: exit %d, stderr %q", code, errOut)
	}
	stall.Store(true)
	var out, errOut lockedBuffer
	go func() {
		<-arrived
		// The notice of the new epoch shows once the client has read the headers, which is when it has
		// state to keep.
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !strings.Contains(errOut.String(), "entered recovery mode"); time.Sleep(time.Millisecond) {
		}
		if err := os.Chmod(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward"), 0o500); err != nil {
			t.Errorf("the configuration directory stays writable: %v", err)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward"), 0o700) })
	code := run([]string{"cluster", "list"}, &out, &errOut)
	got := errOut.String()
	const stop = "Stopped at your request while reading GET /api/v1/clusters"
	if code != 130 || strings.Count(got, "keeping the epoch") != 1 || strings.Count(got, "Stopped at your request") != 1 ||
		strings.Index(got, "keeping the epoch") > strings.Index(got, "Stopped at your request") || !strings.Contains(got, stop) {
		t.Errorf("exit %d, stderr %q; want 130, the failed save once, and the one stop line after it", code, got)
	}
}

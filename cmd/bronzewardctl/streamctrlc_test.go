package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
)

// stallStream answers an event stream with its headers, under the epoch epoch, and then sends no
// event and no keep-alive until the client ends the request. It tells stall once the headers are out.
func stallStream(w http.ResponseWriter, r *http.Request, stall chan<- struct{}, epoch string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Bronzeward-Epoch", epoch)
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	stall <- struct{}{}
	<-r.Context().Done()
}

// ctrlCAtStream runs args, a command whose event stream stalls after its headers, and sends the
// Ctrl-C once the client has read those headers: they carry an epoch that moves the context's, and
// the notice that says so shows on standard error. It returns the exit code and both outputs.
func ctrlCAtStream(t *testing.T, stall <-chan struct{}, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut lockedBuffer
	go func() {
		<-stall
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			if strings.Contains(errOut.String(), "entered recovery mode or was restored") {
				break
			}
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	start := time.Now()
	code := run(args, &out, &errOut)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the command took %s; want it to end at the Ctrl-C", d)
	}
	return code, out.String(), errOut.String()
}

// savedEpoch is the epoch the config holds for the context lab.
func savedEpoch(t *testing.T) string {
	t.Helper()
	cfg, err := client.LoadConfig()
	if err != nil || cfg.Find("lab") == nil {
		t.Fatalf("config %v, error %v; want the context lab", cfg, err)
	}
	return cfg.Find("lab").LastEpoch
}

// TestPublishStopsOnCtrlCDuringTheEventStream: the publication was accepted, then its event stream
// stalls. The Ctrl-C comes after the write completed, so it is a read's: the command exits 130 with
// the one stop line naming the stream, and its deferred Save runs (the epoch the stream's answer
// showed is kept). Before, the default action of SIGINT ended the process with neither (#671).
func TestPublishStopsOnCtrlCDuringTheEventStream(t *testing.T) {
	f := &publishFake{draftFake: &draftFake{rev: 1, state: "open"}, state: "succeeded"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	if code, _, errOut := runCtl(t, "draft", "show", "drf_BWSYNTH"); code != 0 {
		t.Fatalf("draft show: exit %d, stderr %q", code, errOut)
	}
	f.mu.Lock()
	f.stall, f.stallEpoch = make(chan struct{}, 1), "ep_BWSYNTH-2"
	f.mu.Unlock()
	code, out, errOut := ctrlCAtStream(t, f.stall, "publish", "drf_BWSYNTH")
	const stop = "Stopped at your request while reading GET /api/v1/operations/op_BWSYNTH-pub/events.\n"
	switch {
	case code != 130:
		t.Errorf("exit %d, stdout %q, stderr %q; want 130", code, out, errOut)
	case strings.Count(errOut, "Stopped at your request") != 1 || !strings.HasSuffix(errOut, stop):
		t.Errorf("stderr %q; want it to end with the one stop line %q", errOut, stop)
	case strings.Contains(out, "Publication succeeded"):
		t.Errorf("stdout %q; want no outcome for a publication whose result was not read", out)
	}
	if w := f.took(); len(w) != 1 {
		t.Errorf("sent %q; want the one publication", w)
	}
	if got := savedEpoch(t); got != "ep_BWSYNTH-2" {
		t.Errorf("saved epoch %q; want ep_BWSYNTH-2, the one the stream showed: the deferred Save did not run", got)
	}
}

// TestImportStopsOnCtrlCDuringTheEventStream is the same for the follow of an import start.
func TestImportStopsOnCtrlCDuringTheEventStream(t *testing.T) {
	f := &importFake{draftFake: &draftFake{rev: 1, state: "open"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	leaseMargin = 0
	t.Cleanup(func() { leaseMargin = 10 * time.Second })
	if code, _, errOut := runCtl(t, "draft", "show", "drf_BWSYNTH"); code != 0 {
		t.Fatalf("draft show: exit %d, stderr %q", code, errOut)
	}
	f.set("running", 1, time.Hour, "running", "", "")
	f.mu.Lock()
	f.stall, f.stallEpoch = make(chan struct{}, 1), "ep_BWSYNTH-2"
	f.mu.Unlock()
	code, out, errOut := ctrlCAtStream(t, f.stall, "import", "start", "--machine", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	const stop = "Stopped at your request while reading GET /api/v1/operations/op_BWSYNTH-ing/events.\n"
	if code != 130 || strings.Count(errOut, "Stopped at your request") != 1 || !strings.HasSuffix(errOut, stop) {
		t.Errorf("exit %d, stdout %q, stderr %q; want 130 and stderr ending with the one stop line %q", code, out, errOut, stop)
	}
	if w := f.took(); len(w) != 1 || !strings.HasPrefix(w[0], "POST /ingestions ") {
		t.Errorf("sent %q; want the one import start", w)
	}
	if got := savedEpoch(t); got != "ep_BWSYNTH-2" {
		t.Errorf("saved epoch %q; want ep_BWSYNTH-2, the one the stream showed: the deferred Save did not run", got)
	}
}

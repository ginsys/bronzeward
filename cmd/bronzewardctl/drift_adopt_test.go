package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// adoptFake serves a person with the author and publisher roles over the import fake. The second read
// of the ingestion is the one drift adopt makes after the import ended; fail makes it answer 500.
// Once the import fake has moved to its second epoch, every answer of this fake shows that epoch too.
type adoptFake struct {
	*importFake
	reads int
	fail  bool
}

// movedTo is the epoch header a write sets before it sends its status, so that the fake's own
// answers (identity, server) agree with the import fake's.
type movedTo struct {
	http.ResponseWriter
	epoch string
}

func (m movedTo) WriteHeader(status int) {
	m.Header().Set("Bronzeward-Epoch", m.epoch)
	m.ResponseWriter.WriteHeader(status)
}

func (m movedTo) Write(b []byte) (int, error) {
	m.Header().Set("Bronzeward-Epoch", m.epoch)
	return m.ResponseWriter.Write(b)
}

func (f *adoptFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.importFake.mu.Lock()
	moved := f.importFake.moveAfterOp > 0 && f.importFake.opReads >= f.importFake.moveAfterOp
	f.importFake.mu.Unlock()
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/identity" {
		epoch := "ep_BWSYNTH-1"
		if moved {
			epoch = "ep_BWSYNTH-2"
		}
		w.Header().Set("Bronzeward-Epoch", epoch)
		fmt.Fprint(w, `{"kind":"human","principal":"h_BWSYNTH","roles":["author","publisher"]}`)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/ingestions/ing_BWSYNTH" {
		f.importFake.mu.Lock()
		f.reads++
		n := f.reads
		f.importFake.mu.Unlock()
		if n == 2 && f.fail {
			w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, problemHead+`forbidden","title":"forbidden","status":403`+problemTail)
			return
		}
	}
	if moved && r.URL.Path == "/api/v1/server" {
		w = movedTo{w, "ep_BWSYNTH-2"}
	}
	f.importFake.ServeHTTP(w, r)
}

// adoptServer starts the fake with an import paused for review.
func adoptServer(t *testing.T) *adoptFake {
	t.Helper()
	f := &adoptFake{importFake: &importFake{draftFake: &draftFake{rev: 1, state: "open"}}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	saved := leaseMargin
	leaseMargin = 0
	t.Cleanup(func() { leaseMargin = saved })
	f.importFake.set("paused", 1, time.Hour, "running", "", "",
		ev("started", `{"type":"started"}`), ev("staged", `{"type":"staged"}`), ev("paused", `{"type":"paused","generation":1}`))
	// The import write needs the draft's ETag, which a draft read keeps.
	if code, out, errOut := runDrift(t, "draft", "show", "drf_BWSYNTH"); code != 0 {
		t.Fatalf("draft show: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	return f
}

// TestDriftAdoptSavesTheEpochOfItsLastRead: the read of the import after it paused answers under a
// later epoch. The command saves the context after that read, so the next command starts from it.
// Before, the save ran ahead of the read and the context kept the epoch of the import's own steps.
func TestDriftAdoptSavesTheEpochOfItsLastRead(t *testing.T) {
	f := adoptServer(t)
	f.importFake.moveAfterOp = 1
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--review")
	if code != 0 || !strings.Contains(out, "Paused for review") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want the paused import", code, out, errOut)
	}
	if got := savedEpoch(t); got != "ep_BWSYNTH-2" {
		t.Errorf("saved epoch %q; want ep_BWSYNTH-2, the epoch of the last read (stderr %q)", got, errOut)
	}
}

// TestDriftAdoptFailedRereadPrintsTheSteps: the import was accepted, then the read of its state
// fails with an answer that is no retry. The command exits non-zero and still prints how to read the
// import and the later steps.
func TestDriftAdoptFailedRereadPrintsTheSteps(t *testing.T) {
	f := adoptServer(t)
	f.fail = true
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--review")
	if code == 0 {
		t.Errorf("exit 0; want a failure, stdout %q", out)
	}
	for _, want := range []string{
		"The import was accepted, but its state is not known. Read it with: bronzewardctl import status ing_BWSYNTH",
		"The later steps, once it is released into the draft:",
		"bronzewardctl publish 'drf_BWSYNTH'",
		"--operation 'adopt'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout %q; want it to contain %q (stderr %q)", out, want, errOut)
		}
	}
}

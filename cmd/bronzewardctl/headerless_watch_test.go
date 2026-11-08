package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// headerlessRestore wraps a fake server for ginsys/bronzeward#695. For the operation op:
//
//   - the event stream ends with no event;
//   - the first read of the operation is terminal (succeeded) and has no Bronzeward-Epoch header,
//     which is a server defect (client-contracts.md section 8.5);
//   - the event pages answer epoch ep_BWSYNTH-2 with one failed event, so the installation was
//     restored since that read;
//   - from the pages on, every answer carries epoch ep_BWSYNTH-2, GET /server included, and the
//     operation is failed in the restored installation.
//
// Section 8.6 item 2 says the first read does not stand then. A command must not print the success
// of that read as its result.
type headerlessRestore struct {
	next     http.Handler
	op       string // the operation's ID
	succeed  string // the operation's JSON before the restore, with its state succeeded
	failed   string // the operation's JSON in the restored installation, with its state failed
	mu       sync.Mutex
	opReads  int
	restored bool
}

const (
	restoreE1 = "ep_BWSYNTH-1"
	restoreE2 = "ep_BWSYNTH-2"
)

// epochWriter sets the epoch header on every answer it forwards.
type epochWriter struct {
	http.ResponseWriter
	epoch string
}

func (w epochWriter) WriteHeader(code int) {
	w.Header().Set("Bronzeward-Epoch", w.epoch)
	w.ResponseWriter.WriteHeader(code)
}

func (w epochWriter) Write(b []byte) (int, error) {
	w.Header().Set("Bronzeward-Epoch", w.epoch)
	return w.ResponseWriter.Write(b)
}

func (h *headerlessRestore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	h.mu.Lock()
	restored := h.restored
	h.mu.Unlock()
	switch {
	case path == "/operations/"+h.op+"/events" && r.Header.Get("Accept") == "text/event-stream":
		w.Header().Set("Bronzeward-Epoch", restoreE1)
		w.Header().Set("Content-Type", "text/event-stream") // a stream that ends with no event
	case path == "/operations/"+h.op+"/events":
		h.mu.Lock()
		h.restored = true
		h.mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", restoreE2)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"id":"`+restoreE2+`:1","at":"2026-11-01T06:00:01Z","type":"failed","data":{}}]}`)
	case path == "/operations/"+h.op:
		h.mu.Lock()
		h.opReads++
		first := h.opReads == 1
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case restored:
			w.Header().Set("Bronzeward-Epoch", restoreE2)
			fmt.Fprint(w, h.failed)
		case first:
			fmt.Fprint(w, h.succeed) // no Bronzeward-Epoch header
		default:
			w.Header().Set("Bronzeward-Epoch", restoreE1)
			fmt.Fprint(w, h.succeed)
		}
	case restored:
		h.next.ServeHTTP(epochWriter{w, restoreE2}, r)
	default:
		h.next.ServeHTTP(w, r)
	}
}

// TestPublishHeaderlessTerminalRestored reproduces ginsys/bronzeward#695 through publish. The watch
// returns the headerless succeeded read made before the restore, and publish prints it as the result.
func TestPublishHeaderlessTerminalRestored(t *testing.T) {
	t.Skip("ginsys/bronzeward#728: publish prints the state of a headerless terminal read after a restore; remove this skip with the fix")
	f := &publishFake{draftFake: &draftFake{rev: 1, state: "open"}, state: "succeeded"}
	h := &headerlessRestore{next: f, op: "op_BWSYNTH-pub",
		succeed: fmt.Sprintf(publishOp, "succeeded", `{"release":"rel_BWSYNTH"}`, `null`),
		failed: fmt.Sprintf(publishOp, "failed", `null`,
			`{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422}`)}
	srv := httptest.NewServer(h)
	defer srv.Close()
	automationContext(t, srv.URL)
	var o, e bytes.Buffer
	if code := run([]string{"draft", "show", "drf_BWSYNTH"}, &o, &e); code != 0 {
		t.Fatalf("draft show: exit %d, stdout %q, stderr %q; want 0", code, o.String(), e.String())
	}
	o.Reset()
	e.Reset()
	code := run([]string{"publish", "drf_BWSYNTH"}, &o, &e)
	t.Logf("publish: exit %d\nstdout:\n%s\nstderr:\n%s", code, o.String(), e.String())
	if code == 0 || strings.Contains(o.String(), "Publication succeeded") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a non-zero exit and no success printed: the succeeded read was made before the restore, and the restored installation shows the publication failed",
			code, o.String(), e.String())
	}
}

// TestImportWatchHeaderlessTerminalRestored reproduces ginsys/bronzeward#695 through the import watch
// (import continue). The same scenario as for publish: the claim and the operation are of the first
// epoch, and the pages show the restored one.
func TestImportWatchHeaderlessTerminalRestored(t *testing.T) {
	t.Skip("ginsys/bronzeward#728: the import watch prints the state of a headerless terminal read after a restore; remove this skip with the fix")
	f := &importFake{draftFake: &draftFake{rev: 1, state: "open"}}
	f.set("released", 1, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "")
	op := func(state, result, problem, epoch string) string {
		return fmt.Sprintf(`{"id":"op_BWSYNTH-ing","kind":"ingest","state":%q,"subject":{"draft":"drf_BWSYNTH","draftRevision":1},`+
			`"result":%s,"error":%s,"createdAt":"2026-11-01T06:00:00Z","epoch":%q,"lastEvent":1}`, state, result, problem, epoch)
	}
	h := &headerlessRestore{next: f, op: "op_BWSYNTH-ing",
		succeed: op("succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, `null`, restoreE1),
		failed:  op("failed", `null`, `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422}`, restoreE2)}
	srv := httptest.NewServer(h)
	defer srv.Close()
	automationContext(t, srv.URL)
	var o, e bytes.Buffer
	code := run([]string{"import", "continue", "ing_BWSYNTH"}, &o, &e)
	t.Logf("import continue: exit %d\nstdout:\n%s\nstderr:\n%s", code, o.String(), e.String())
	if code == 0 || strings.Contains(o.String(), "released into draft") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a non-zero exit and no success printed: the succeeded read was made before the restore, and the restored installation shows the import failed",
			code, o.String(), e.String())
	}
}

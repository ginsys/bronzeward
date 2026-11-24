package client

import (
	"net/http"
	"strings"
	"testing"
)

// TestWriteAnswerOfNewEpochReadsServer checks that a write answer that shows a new epoch is followed by
// one GET /server, as a read's answer is, so the start mode and the recovery banner are those of the new
// epoch whether or not the command reads again (client-contracts.md section 8.5, start mode).
func TestWriteAnswerOfNewEpochReadsServer(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH-1", "ep_BWSYNTH-2"
	// serverReadsAfterPost counts the GET /server the client sent after the write's POST.
	serverReadsAfterPost := func(f *fake) int {
		n, posted := 0, false
		for _, r := range f.requests() {
			switch r {
			case "POST /api/v1/clusters":
				posted = true
			case "GET /api/v1/server":
				if posted {
					n++
				}
			}
		}
		return n
	}
	t.Run("a success that shows a new epoch is followed by one read of the server", func(t *testing.T) {
		f := &fake{epoch: e1, pathEpoch: map[string]string{"/api/v1/clusters": e2}, post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
		c, notices, _ := writeClient(t, f, e1)
		if _, err := c.Send(clusterWrite()); err != nil {
			t.Fatal(err)
		}
		if n := serverReadsAfterPost(f); n != 1 || strings.Contains(notices.String(), "could not be read again") {
			t.Errorf("%d reads of GET /server after the write, notices %q; want one, and no notice", n, notices)
		}
	})
	t.Run("a failed read leaves the write's result, and says so", func(t *testing.T) {
		// After the POST, GET /server answers 404: the context-use read before the write has answered.
		f := &fake{epoch: e1, entered: e2, serverFails: true, pathEpoch: map[string]string{"/api/v1/clusters": e2}, post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
		c, notices, _ := writeClient(t, f, e1)
		r, err := c.Send(clusterWrite())
		if err != nil || r == nil {
			t.Fatalf("send: %v, %v; want the write's success", r, err)
		}
		if n := serverReadsAfterPost(f); n != 1 || !strings.Contains(notices.String(), "GET /server could not be read again") {
			t.Errorf("%d reads of GET /server after the write, notices %q; want one, and the notice that it failed", n, notices)
		}
	})
	t.Run("a recovery start's refusal reads nothing, though this fake would answer: GET /server is refused before the entry", func(t *testing.T) {
		f := &fake{epoch: e1, pathEpoch: map[string]string{"/api/v1/clusters": e2},
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusConflict, "recovery-mode-active", "")
			}}
		c, notices, _ := writeClient(t, f, e1)
		if _, err := c.Send(clusterWrite()); err == nil {
			t.Fatal("send succeeded; want the refusal")
		}
		if n := serverReadsAfterPost(f); n != 0 || strings.Contains(notices.String(), "could not be read again") {
			t.Errorf("%d reads of GET /server after the refusal, notices %q; want none, and no notice", n, notices)
		}
	})
	t.Run("an answer of the epoch already known reads nothing", func(t *testing.T) {
		f := &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
		c, _, _ := writeClient(t, f, e1)
		if _, err := c.Send(clusterWrite()); err != nil {
			t.Fatal(err)
		}
		if n := serverReadsAfterPost(f); n != 0 {
			t.Errorf("%d reads of GET /server after the write; want none", n)
		}
	})
}

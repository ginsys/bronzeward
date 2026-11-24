package client

import (
	"net/http"
	"slices"
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
	t.Run("a read between two sends that shows the new epoch is no answer of the write", func(t *testing.T) {
		// The first POST is refused as unavailable, at the epoch known; the confirming GET /recovery before the
		// repeat shows e2 and stops the write. The POST itself showed nothing new, so nothing reads GET /server.
		f := &fake{epoch: e1, recovery: func(int) string { return e2 },
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
			}}
		c, notices, _ := writeClient(t, f, e1)
		if _, err := c.Send(clusterWrite()); err == nil {
			t.Fatal("send succeeded; want the stop at the confirming read")
		}
		if n := serverReadsAfterPost(f); n != 0 || !slices.Contains(f.requests(), "GET /api/v1/recovery") {
			t.Errorf("%d reads of GET /server after the write, requests %q, notices %q; want none after a confirming read that showed e2", n, f.requests(), notices)
		}
	})
	t.Run("a complete answer that is no problem document, and shows a new epoch, is followed by the read", func(t *testing.T) {
		f := &fake{epoch: e1, pathEpoch: map[string]string{"/api/v1/clusters": e2},
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("bad gateway"))
			}}
		c, notices, _ := writeClient(t, f, e1)
		if _, err := c.Send(clusterWrite()); err == nil {
			t.Fatal("send succeeded; want the refusal")
		}
		if n := serverReadsAfterPost(f); n != 1 {
			t.Errorf("%d reads of GET /server after the write, notices %q; want one", n, notices)
		}
	})
	t.Run("a recovery start's refusal of an operator's repeat reads nothing either", func(t *testing.T) {
		// A repeat that is refused is reported as an unknown outcome, which keeps the problem out of the error chain.
		f := &fake{epoch: e1, pathEpoch: map[string]string{"/api/v1/clusters": e2},
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusConflict, "recovery-mode-active", "")
			}}
		c, notices, _ := writeClient(t, f, e1)
		w := clusterWrite()
		// The outcome's own read of the resource goes through Get, as the commands' callbacks do.
		w.Read = func(c *Client) (string, error) { _, err := c.Get("/clusters"); return "BWSYNTH shows", err }
		w.Repeat = &Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: e1, Principal: "svc_BWSYNTH"}
		_, err := c.Send(w)
		if err == nil {
			t.Fatal("send succeeded; want the refusal")
		}
		if n := serverReadsAfterPost(f); n != 0 || strings.Contains(notices.String(), "could not be read again") || !slices.Contains(f.requests(), "POST /api/v1/clusters") {
			t.Errorf("%d reads of GET /server after the refusal, requests %q, notices %q; want none after a POST that was sent, and no notice", n, f.requests(), notices)
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

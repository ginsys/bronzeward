package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestGetOnceTriesOnce: a retryable 503 is answered with one request, no wait and no retry notice by
// GetOnce, and with five requests and four waits by Get on the same server, the control that makes the
// one request mean something.
func TestGetOnceTriesOnce(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
	}))
	defer srv.Close()
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = srv.URL
	var waits []time.Duration
	waitFor = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	t.Cleanup(func() { waitFor = defaultWait })

	if _, err := c.GetOnce("/principals"); err == nil {
		t.Fatal("GetOnce: no error for a 503")
	}
	if asked.Load() != 1 || len(waits) != 0 || notices.Len() != 0 {
		t.Errorf("GetOnce: %d requests, waits %v, notices %q; want 1, none, none", asked.Load(), waits, notices)
	}
	asked.Store(0)
	if _, err := c.Get("/principals"); err == nil {
		t.Fatal("Get: no error for a 503")
	}
	if asked.Load() != 5 || len(waits) != 4 {
		t.Errorf("Get: %d requests, %d waits; want 5 and 4", asked.Load(), len(waits))
	}
}

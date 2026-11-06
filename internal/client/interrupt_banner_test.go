package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// bannerText is the start of the recovery banner (client-contracts.md §8.5 item 3).
const bannerText = "RECOVERY MODE is on in "

// recoveryReads is a server that answers every read with status and the epoch, and with the
// recovery header when recovery is true. It records the paths it was asked for.
type recoveryReads struct {
	mu    sync.Mutex
	paths []string
}

func (r *recoveryReads) serve(t *testing.T, status int, epoch string, recovery bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, q.URL.Path)
		r.mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", epoch)
		if recovery {
			w.Header().Set("Bronzeward-Recovery-Mode", "true")
		}
		if status == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		problemAnswer(w, status, "dependency-unavailable", "")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (r *recoveryReads) asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

// ctrlCInTheWait makes the Ctrl-C come while the client waits before its second try.
func ctrlCInTheWait(t *testing.T, a *armed) {
	t.Helper()
	waitFor = func(ctx context.Context, d time.Duration) error {
		a.cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { waitFor = defaultWait })
}

// TestCtrlCInTheWaitAfterARecoveryAnswerShowsTheBanner: the server answers a retryable 503 that
// carries the recovery header. The Ctrl-C ends the wait before the second try. The read that the
// Ctrl-C ended still shows the banner, once for the command: a second read shows no second one.
func TestCtrlCInTheWaitAfterARecoveryAnswerShowsTheBanner(t *testing.T) {
	var s recoveryReads
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.serve(t, http.StatusServiceUnavailable, "ep_BWSYNTH-1", true)
	a := newArmed(t)
	c.Arm = a.hook
	ctrlCInTheWait(t, a)
	_, err := c.Get("/clusters")
	_, again := c.Get("/clusters")
	switch {
	case !errors.As(err, new(*Interrupted)):
		t.Errorf("error %v; want the read stopped by Ctrl-C", err)
	case !errors.As(again, new(*Interrupted)):
		t.Errorf("second error %v; want the second read stopped too", again)
	case strings.Count(notices.String(), bannerText) != 1:
		t.Errorf("notices %q; want the recovery banner once", notices)
	case len(s.asked()) != 1:
		t.Errorf("requests %v; want the one read: a Ctrl-C sends no second try", s.asked())
	}
}

// TestCtrlCTakenAsTheAnswerEndsShowsTheBanner: the server answers 200 with the recovery header, and
// the disarm reports a Ctrl-C taken as the read ended. The read stops, and the banner is shown.
func TestCtrlCTakenAsTheAnswerEndsShowsTheBanner(t *testing.T) {
	var s recoveryReads
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.serve(t, http.StatusOK, "ep_BWSYNTH-1", true)
	newArmed(t)
	c.Arm = func() (context.Context, func() bool) {
		return context.Background(), func() bool { return true }
	}
	r, err := c.Get("/clusters")
	switch {
	case !errors.As(err, new(*Interrupted)) || r != nil:
		t.Errorf("answer %v, error %v; want the read stopped by the Ctrl-C taken as it ended", r, err)
	case strings.Count(notices.String(), bannerText) != 1:
		t.Errorf("notices %q; want the recovery banner once", notices)
	}
}

// TestCtrlCAfterANewEpochShowsTheBannerAndRefreshesNothing: the answer shows another epoch than the
// client knows and the recovery header. A refresh would read GET /server. The Ctrl-C ends the
// command first: the banner is shown, and the refresh sends no request.
func TestCtrlCAfterANewEpochShowsTheBannerAndRefreshesNothing(t *testing.T) {
	var s recoveryReads
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.serve(t, http.StatusServiceUnavailable, "ep_BWSYNTH-2", true)
	a := newArmed(t)
	c.Arm = a.hook
	ctrlCInTheWait(t, a)
	before := a.calls.Load()
	_, err := c.Get("/clusters")
	switch {
	case !errors.As(err, new(*Interrupted)):
		t.Errorf("error %v; want the read stopped by Ctrl-C", err)
	case strings.Count(notices.String(), bannerText) != 1:
		t.Errorf("notices %q; want the recovery banner once", notices)
	case len(s.asked()) != 1 || s.asked()[0] != "/api/v1/clusters":
		t.Errorf("requests %v; want the one read and no GET /server after the Ctrl-C", s.asked())
	case a.calls.Load()-before != 1:
		t.Errorf("%d arms for the command; want 1", a.calls.Load()-before)
	}
}

// TestCtrlCShowsNoBannerWithoutTheRecoveryHeader is a control: the same read and Ctrl-C as the first
// test, with no recovery header, show no banner. The banner follows the header, not the Ctrl-C.
func TestCtrlCShowsNoBannerWithoutTheRecoveryHeader(t *testing.T) {
	var s recoveryReads
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.serve(t, http.StatusServiceUnavailable, "ep_BWSYNTH-1", false)
	a := newArmed(t)
	c.Arm = a.hook
	ctrlCInTheWait(t, a)
	_, err := c.Get("/clusters")
	switch {
	case !errors.As(err, new(*Interrupted)):
		t.Errorf("error %v; want the read stopped by Ctrl-C", err)
	case strings.Contains(notices.String(), bannerText):
		t.Errorf("notices %q; want no banner: the answer had no recovery header", notices)
	case len(s.asked()) != 1:
		t.Errorf("requests %v; want the one read", s.asked())
	}
}

// TestRecoveryAnswerShowsTheBannerOnce is a control for the tests above: with no Ctrl-C, a 200 with the
// recovery header shows the banner once. It proves that bannerText is what the client writes.
func TestRecoveryAnswerShowsTheBannerOnce(t *testing.T) {
	var s recoveryReads
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.serve(t, http.StatusOK, "ep_BWSYNTH-1", true)
	newArmed(t)
	for range 2 {
		if _, err := c.Get("/clusters"); err != nil {
			t.Fatalf("read failed: %v", err)
		}
	}
	if n := strings.Count(notices.String(), bannerText); n != 1 {
		t.Errorf("notices %q; want the recovery banner once for two reads, got %d", notices, n)
	}
}

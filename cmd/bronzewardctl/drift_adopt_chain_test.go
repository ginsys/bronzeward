package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// adoptChain serves all three steps of drift adopt: the import and the publication share one draft,
// and the adopt plan is created on the plan fake. With moveAfterPublish set, the first GET /identity
// after the publication's operation was read (the /server read that opens the plan write's preflight)
// moves the epoch to ep_BWSYNTH-2 for every answer from there on.
type adoptChain struct {
	imp              *importFake
	pub              *publishFake
	plan             *planFake
	mu               sync.Mutex
	moveAfterPublish bool
	// pubUnknown answers the publication with a redirect, which the client cannot read: its outcome is unknown.
	pubUnknown  bool
	read, moved bool
	// moveOnReread makes the reread of the ingestion that step 1 ends with, the second GET of it,
	// answer ep_BWSYNTH-2. The /server refresh that follows answers ep_BWSYNTH-1 again, as every
	// other answer does: the epoch changed and came back. rereads counts those GETs.
	moveOnReread bool
	rereads      int
	log          []string
}

func (f *adoptChain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	f.mu.Lock()
	if f.moveAfterPublish && f.read && r.Method == http.MethodGet && path == "/server" {
		f.moved = true
	}
	if path == "/operations/op_BWSYNTH-pub" {
		f.read = true
	}
	moved := f.moved
	if f.moveOnReread && r.Method == http.MethodGet && path == "/ingestions/ing_BWSYNTH" {
		f.rereads++
		moved = moved || f.rereads == 2
	}
	f.log = append(f.log, fmt.Sprintf("%s %s moved=%v", r.Method, path, moved))
	f.mu.Unlock()
	if moved {
		w = movedTo{w, "ep_BWSYNTH-2"}
	}
	switch {
	case path == "/identity":
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		fmt.Fprint(w, `{"kind":"human","principal":"h_BWSYNTH","roles":["author","publisher"]}`)
	case strings.HasPrefix(path, "/ingestions"), strings.HasPrefix(path, "/operations/op_BWSYNTH-ing"):
		f.imp.ServeHTTP(w, r)
	case strings.HasSuffix(path, "/publications") && r.Method == http.MethodPost && f.pubUnknown:
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusTemporaryRedirect)
	case strings.HasSuffix(path, "/publications"), strings.HasPrefix(path, "/operations/op_BWSYNTH-pub"):
		f.pub.ServeHTTP(w, r)
	case path == "/plans":
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		f.plan.ServeHTTP(w, r)
	default:
		f.imp.ServeHTTP(w, r)
	}
}

func adoptChainServer(t *testing.T) *adoptChain {
	t.Helper()
	d := &draftFake{rev: 1, state: "open"}
	f := &adoptChain{imp: &importFake{draftFake: d}, pub: &publishFake{draftFake: d, state: "succeeded"},
		plan: &planFake{state: "proposed", op: "adopt", evidence: adoptEvidence, desired: "rel_BWSYNTH", assignment: "asr_BWSYNTH-1"}}
	f.plan.epochFor = func(string) string { return "ep_BWSYNTH-1" }
	f.plan.post = func(w http.ResponseWriter, _ string) {
		w.WriteHeader(http.StatusCreated)
		f.plan.mu.Unlock()
		fmt.Fprint(w, f.plan.plan())
		f.plan.mu.Lock()
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	saved := leaseMargin
	leaseMargin = 0
	t.Cleanup(func() { leaseMargin = saved })
	f.imp.set("released", 1, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		ev("succeeded", `{"type":"succeeded"}`))
	// The import write needs the draft's ETag, which a draft read keeps; the import's own write then
	// makes it revision 2.
	if code, out, errOut := runDrift(t, "draft", "show", "drf_BWSYNTH"); code != 0 {
		t.Fatalf("draft show: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	f.imp.onIngested = func() {
		d.mu.Lock()
		d.rev = 2
		d.mu.Unlock()
	}
	return f
}

// TestDriftAdoptBindsItsPlanToThePublication: the adopt plan is created on the publication's client,
// bound to the epoch that publication left. When the epoch moves before the plan is sent, nothing is
// sent; and the context keeps the newer epoch, which a client saved earlier does not overwrite.
// Before, the plan write opened its own client and bound nothing, so it was sent.
func TestDriftAdoptBindsItsPlanToThePublication(t *testing.T) {
	f := adoptChainServer(t)
	f.moveAfterPublish = true
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	if got := f.plan.took(); code == 0 || len(got) != 0 || !strings.Contains(errOut, "after the request was built") || !strings.Contains(out, "Step 3 again:") {
		t.Errorf("exit %d, plan sent %q, stdout %q, stderr %q; want a refusal that sent no plan and names step 3 again\nrequests: %q", code, got, out, errOut, f.log)
	}
	if got := savedEpoch(t); got != "ep_BWSYNTH-2" {
		t.Errorf("saved epoch %q; want ep_BWSYNTH-2, the newest one", got)
	}
}

// TestDriftAdoptSendsItsPlanWhenTheEpochHolds is the control of the test above: with no move the
// same chain sends the one adopt plan.
func TestDriftAdoptSendsItsPlanWhenTheEpochHolds(t *testing.T) {
	f := adoptChainServer(t)
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	if got := f.plan.took(); code != 0 || len(got) != 1 || !strings.Contains(got[0], `"operation":"adopt"`) || !strings.Contains(got[0], `"releaseId":"rel_BWSYNTH"`) {
		t.Errorf("exit %d, plan sent %q, stdout %q, stderr %q; want exit 0 and one adopt plan of rel_BWSYNTH", code, got, out, errOut)
	}
	if !strings.Contains(out, "drift adopt never approves: an approver runs bronzewardctl approve 'pln_BWSYNTH'") {
		t.Errorf("stdout %q does not give the adoption guidance", out)
	}
	if got := savedEpoch(t); got != "ep_BWSYNTH-1" {
		t.Errorf("saved epoch %q; want ep_BWSYNTH-1, which never moved", got)
	}
}

// TestDriftAdoptBindsItsPublicationToTheIngestionAnswer: the publication is sent on the client that
// read the import, bound to the epoch of that answer. When the epoch changed and came back while
// the import was read, the answer was given under another epoch, so nothing is published and the
// output names step 2 again. Before, the publication opened a new client that read the epoch again,
// found E1, and sent.
func TestDriftAdoptBindsItsPublicationToTheIngestionAnswer(t *testing.T) {
	f := adoptChainServer(t)
	f.moveOnReread = true
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	sent := false
	for _, l := range f.log {
		sent = sent || strings.HasPrefix(l, "POST /drafts/drf_BWSYNTH/publications")
	}
	if code == 0 || sent || !strings.Contains(out, "Step 2 again:") {
		t.Errorf("exit %d, publication sent %v, stdout %q, stderr %q; want a refusal that published nothing and names step 2 again\nrequests: %q", code, sent, out, errOut, f.log)
	}
}

// TestDriftAdoptPublishesWhenTheIngestionAnswerHolds is the control of the test above: the same
// chain with no move publishes once.
func TestDriftAdoptPublishesWhenTheIngestionAnswerHolds(t *testing.T) {
	f := adoptChainServer(t)
	f.moveOnReread = false
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	n := 0
	for _, l := range f.log {
		if strings.HasPrefix(l, "POST /drafts/drf_BWSYNTH/publications") {
			n++
		}
	}
	if code != 0 || n != 1 {
		t.Errorf("exit %d, publications sent %d, stdout %q, stderr %q; want exit 0 and one publication", code, n, out, errOut)
	}
}

// TestDriftAdoptNamesTheCommandToRepeat: after an unknown outcome of step 2 or step 3, the block names
// the command that repeats that step, with the flags it printed. Repeating drift adopt would give
// those flags to the import of step 1, whose key, epoch and fingerprint are another request.
func TestDriftAdoptNamesTheCommandToRepeat(t *testing.T) {
	for _, c := range []struct {
		name, want string
		set        func(*adoptChain)
	}{
		{"step 2, the publication", "give the same input, with the command bronzewardctl publish 'drf_BWSYNTH' in place of drift adopt, and: --idempotency-key ",
			func(f *adoptChain) { f.pubUnknown = true }},
		{"step 3, the adopt plan", "give the same input, with the command bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_BWSYNTH' --operation 'adopt' in place of drift adopt, and: --idempotency-key ",
			func(f *adoptChain) {
				f.plan.post = func(w http.ResponseWriter, _ string) {
					w.Header().Set("Location", "/elsewhere")
					w.WriteHeader(http.StatusTemporaryRedirect)
				}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := adoptChainServer(t)
			c.set(f)
			code, _, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
			if code != 3 || !strings.Contains(errOut, c.want) {
				t.Errorf("exit %d, stderr %q; want exit 3 and %q", code, errOut, c.want)
			}
		})
	}
}

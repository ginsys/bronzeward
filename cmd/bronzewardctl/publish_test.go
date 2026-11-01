package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// publishFake is draftFake with a publication: its operation ends in state, with the release
// rel_BWSYNTH of two machines, mch_a with an Applied release and mch_b with none, whose reviews are
// rendered and withheld. first is the type of the operation's first event; bare answers the
// publication with an operation that has no ID, and pagesRefused refuses the JSON event pages.
type publishFake struct {
	*draftFake
	mu           sync.Mutex
	state        string
	streams      int
	first        string
	bare         bool
	pagesRefused bool
}

const (
	publishOp = `{"id":"op_BWSYNTH-pub","kind":"publish","state":%q,"subject":{"draft":"drf_BWSYNTH","draftRevision":1},` +
		`"result":%s,"error":%s,"createdAt":"2026-11-01T06:00:00Z","epoch":"ep_BWSYNTH-1","lastEvent":2}`
	rendered = "machine:\n  registries:\n    config:\n      registry.example.invalid:\n        auth:\n" +
		"          password: <redacted:registry/example-pass@1>\n  nodeLabels:\n    team: \"\"\n    tabbed: \"a\tb\"\n"
)

func (f *publishFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	f.mu.Lock()
	state, first, bare, pagesRefused := f.state, f.first, f.bare, f.pagesRefused
	f.mu.Unlock()
	if first == "" {
		first = "queued"
	}
	result, problem := `null`, `null`
	if state == "succeeded" {
		result = `{"release":"rel_BWSYNTH"}`
	} else {
		problem = `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422}`
	}
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && path == "/drafts/drf_BWSYNTH/publications":
		if r.Header.Get("If-Match") != f.etag() {
			f.refuse(w)
			return
		}
		f.draftFake.mu.Lock()
		f.writes = append(f.writes, "POST "+path+" if-match="+r.Header.Get("If-Match"))
		f.draftFake.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		if bare {
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprintf(w, publishOp, "queued", `null`, `null`)
	case path == "/operations/op_BWSYNTH-pub/events" && r.Header.Get("Accept") == "text/event-stream":
		f.mu.Lock()
		f.streams++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "id: ep_BWSYNTH-1:1\nevent: "+first+"\ndata: {}\n\n: keep-alive\n\nid: ep_BWSYNTH-1:2\nevent: "+state+"\ndata: {}\n\n")
	case path == "/operations/op_BWSYNTH-pub/events" && pagesRefused:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"urn:bronzeward:problem:forbidden","title":"BWSYNTH","status":403}`)
	case path == "/operations/op_BWSYNTH-pub/events":
		fmt.Fprintf(w, `{"items":[{"id":"ep_BWSYNTH-1:1","at":"2026-11-01T06:00:00Z","type":"queued","data":{}},`+
			`{"id":"ep_BWSYNTH-1:2","at":"2026-11-01T06:00:01Z","type":%q,"data":{}}]}`, state)
	case path == "/operations/op_BWSYNTH-pub":
		fmt.Fprintf(w, publishOp, state, result, problem)
	case path == "/releases/rel_BWSYNTH":
		fmt.Fprint(w, `{"id":"rel_BWSYNTH","cluster":"cl_BWSYNTH","machines":[{"machine":"mch_a"},{"machine":"mch_b"}]}`)
	case path == "/machines/mch_a":
		fmt.Fprint(w, `{"id":"mch_a","applied":{"release":"rel_BWSYNTH-old","source":"operation"}}`)
	case path == "/machines/mch_b":
		fmt.Fprint(w, `{"id":"mch_b","applied":null}`)
	case path == "/releases/rel_BWSYNTH/machines/mch_a/review":
		fmt.Fprintf(w, `{"release":"rel_BWSYNTH","machine":"mch_a","mode":"metal","configuration":%q,"provenance":[`+
			`{"reference":"registry/example-pass","version":1,"source":{"revision":"frv_BWSYNTH-a","path":"doc[0]/machine/registries/config/registry.example.invalid/auth/password","digest":"d","occurrence":0},"output":"doc[0]/machine/registries/config/registry.example.invalid/auth/password"},`+
			`{"reference":"tls/key","version":2,"member":1,"source":{"revision":"frv_BWSYNTH-b","path":"doc[0]/machine/x\u001b[2J","digest":"d","occurrence":0},"overriddenBy":{"revision":"frv_BWSYNTH-c","digest":"d"}}]}`, rendered)
	case path == "/releases/rel_BWSYNTH/machines/mch_b/review":
		fmt.Fprint(w, `{"release":"rel_BWSYNTH","machine":"mch_b","mode":"metal","configuration":null,"notice":"BWSYNTH no redacted form\u001b[2J","provenance":[]}`)
	default:
		f.draftFake.ServeHTTP(w, r)
	}
}

// TestPublication checks publish (T18 of #256): the kept ETag sent, the wait through the progress
// reader with the events on standard error, the release and each machine's next step named, the
// ETag forgotten once published, --no-wait, a failed publication, and -o json printing the answer
// unchanged; then release review and provenance.
func TestPublication(t *testing.T) {
	f := &publishFake{draftFake: &draftFake{rev: 1, state: "open"}, state: "succeeded"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	var stderr string // the last step's standard error
	step := func(name string, args []string, code int, out, errText string, absent ...string) string {
		t.Helper()
		var o, e bytes.Buffer
		got := run(args, &o, &e)
		stderr = e.String()
		if got != code || !strings.Contains(o.String(), out) || !strings.Contains(e.String(), errText) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d, %q, %q", name, got, o.String(), e.String(), code, out, errText)
		}
		for _, a := range absent {
			if strings.Contains(o.String(), a) {
				t.Errorf("%s: stdout %q holds %q", name, o.String(), a)
			}
		}
		return o.String()
	}
	pub := []string{"publish", "drf_BWSYNTH"}
	step("publish before a read", pub, 1, "", "Read the draft first")
	step("draft show", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	step("publish", pub, 0, "Publishing draft drf_BWSYNTH revision 1 (operation op_BWSYNTH-pub).\n"+
		"Publication succeeded: release rel_BWSYNTH is now Desired for 2 machines. Nothing has been sent to any machine.\n"+
		"  mch_a has Applied release rel_BWSYNTH-old: plan an apply-config.\n"+
		"  mch_b has no Applied release: hand it over with an adopt plan.\n", "  queued\n  succeeded\n")
	if w := f.took(); len(w) != 1 || w[0] != `POST /drafts/drf_BWSYNTH/publications if-match="1-BWSYNTH"` {
		t.Errorf("sent %q; want one publication with the kept ETag", w)
	}
	step("the published draft's ETag is forgotten", pub, 1, "", "Read the draft first")

	step("an ETag kept after the last one was forgotten", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	f.streams = 0
	step("--no-wait", append(pub, "--no-wait"), 0, "Nothing has been sent to any machine. Follow it with: bronzewardctl operation show op_BWSYNTH-pub", "",
		"Publication succeeded")
	if f.streams != 0 {
		t.Errorf("--no-wait opened %d streams", f.streams)
	}
	f.mu.Lock()
	f.state = "failed"
	f.mu.Unlock()
	step("a failed publication exits 4", pub, 4, "Publishing draft", `publication operation op_BWSYNTH-pub failed: {"type":"urn:bronzeward:problem:validation-failed",`+
		`"title":"BWSYNTH","status":422}; nothing was sent to any machine`, "Publication succeeded")
	if out := step("-o json prints the answer unchanged", append(pub, "-o", "json"), 4, "", "failed"); out != fmt.Sprintf(publishOp, "queued", `null`, `null`)+"\n" {
		t.Errorf("-o json printed %q", out)
	}
	f.mu.Lock()
	f.pagesRefused, f.first = true, "queued\x1b[2J"
	f.mu.Unlock()
	step("a failed publication whose last events cannot be read still exits 4", pub, 4, "", "publication operation op_BWSYNTH-pub failed")
	if !strings.Contains(stderr, "  queued\\x1b[2J\n") || strings.Contains(stderr, "\x1b") || !strings.Contains(stderr, "the operation's last events were not shown") {
		t.Errorf("stderr %q; want the event type escaped and the unread events named", stderr)
	}
	f.mu.Lock()
	f.pagesRefused, f.first, f.bare = false, "", true
	f.mu.Unlock()
	step("--no-wait on an operation with no ID", append(pub, "--no-wait"), 1, "Publishing draft drf_BWSYNTH revision ?.",
		"the publication was accepted without naming its operation", "(operation", "Follow it with")
	f.mu.Lock()
	f.bare = false
	f.mu.Unlock()

	step("review: verbatim, the token kept and the tab a tab", []string{"release", "review", "rel_BWSYNTH", "mch_a"}, 0,
		"Release rel_BWSYNTH, machine mch_a, validated: metal\n"+rendered, "")
	step("review withheld: the notice, escaped, and no document", []string{"release", "review", "rel_BWSYNTH", "mch_b"}, 0,
		"Configuration not shown: it could not be redacted.\nServer notice: BWSYNTH no redacted form\\x1b[2J\n", "", "machine:", "configuration", "\x1b")
	if out := step("review -o json", []string{"release", "review", "rel_BWSYNTH", "mch_b", "-o", "json"}, 0, "", ""); !strings.Contains(out, `"configuration":null`) {
		t.Errorf("-o json printed %q", out)
	}
	step("provenance: exactly the records served, a path's control character escaped", []string{"release", "provenance", "rel_BWSYNTH", "mch_a"}, 0,
		"registry/example-pass  1        frv_BWSYNTH-a, doc[0]/machine/registries/config/registry.example.invalid/auth/password  doc[0]/machine/registries/config/registry.example.invalid/auth/password\n"+
			"tls/key#1              2        frv_BWSYNTH-b, doc[0]/machine/x\\x1b[2J                                                  overridden by frv_BWSYNTH-c\n"+
			"Provenance covers secret references only; other fields are not traced.\n", "", "\x1b")
	step("provenance with none", []string{"release", "provenance", "rel_BWSYNTH", "mch_b"}, 0,
		"Provenance covers secret references only; other fields are not traced.", "", "registry")
}

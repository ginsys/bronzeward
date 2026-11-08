package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

// draftFake serves one draft whose revision and ETag move on every write, as the server's do
// (internal/api/drafts.go). It records each write's method, path, If-Match, Content-Type and body.
type draftFake struct {
	mu     sync.Mutex
	rev    int
	state  string
	writes []string
	// unreadable adds an entry no SourceEntry decodes: its name is a number.
	unreadable bool
	// lost redirects a profile PUT, so its outcome is unknown; reads records each GET's route.
	lost  bool
	reads []string
	// answer replaces the {} a draft write is answered with; empty answers it with no body.
	answer string
	empty  bool
	// epoch replaces ep_BWSYNTH-1 on every answer; recovery marks GET /recovery's answer as served in
	// recovery mode.
	epoch    string
	recovery bool
	// serverEpoch replaces the epoch on GET /server's answer alone; fragmentsEpoch does the same for
	// GET /fragments.
	serverEpoch    string
	fragmentsEpoch string
	// identityEpoch does the same for GET /identity, which a write reads before and after GET /server.
	identityEpoch string
}

func (f *draftFake) etag() string { return fmt.Sprintf(`"%d-BWSYNTH"`, f.rev) }

func (f *draftFake) draft() string {
	extra := ""
	if f.unreadable {
		extra = `,{"kind":"fragment","name":7,"base":1,"head":"frv_BWSYNTH-odd","revision":"frv_BWSYNTH-odd"}`
	}
	return fmt.Sprintf(`{"id":"drf_BWSYNTH","cluster":"cl_BWSYNTH","title":"BWSYNTH","state":%q,"revision":%d,"entries":[`+
		`{"kind":"fragment","name":"worker-labels","base":1,"head":"frv_BWSYNTH-base","revision":"frv_BWSYNTH-proposed"},`+
		`{"kind":"fragment","name":"gone","base":1,"head":"frv_BWSYNTH-gone","revision":null}%s]}`, f.state, f.rev, extra)
}

func (f *draftFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	epoch := "ep_BWSYNTH-1"
	if f.epoch != "" {
		epoch = f.epoch
	}
	w.Header().Set("Bronzeward-Epoch", epoch)
	w.Header().Set("Content-Type", "application/json")
	route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.Method != http.MethodGet {
		body, _ := io.ReadAll(r.Body)
		f.writes = append(f.writes, fmt.Sprintf("%s if-match=%s type=%s body=%s", route, r.Header.Get("If-Match"), r.Header.Get("Content-Type"), body))
	} else {
		f.reads = append(f.reads, route)
	}
	if f.lost && r.Method == http.MethodPut && strings.Contains(route, "/profiles/") {
		http.Redirect(w, r, "https://BWSYNTH.example.invalid/", http.StatusTemporaryRedirect)
		return
	}
	switch route {
	case "GET /recovery":
		if f.recovery {
			w.Header().Set("Bronzeward-Recovery-Mode", "true")
		}
		fmt.Fprint(w, `{}`)
	case "GET /server":
		if f.serverEpoch != "" {
			w.Header().Set("Bronzeward-Epoch", f.serverEpoch)
		}
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
	case "GET /identity":
		if f.identityEpoch != "" {
			w.Header().Set("Bronzeward-Epoch", f.identityEpoch)
		}
		fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
	case "GET /fragments":
		if f.fragmentsEpoch != "" {
			w.Header().Set("Bronzeward-Epoch", f.fragmentsEpoch)
		}
		fmt.Fprint(w, `{"items":[{"id":"frg_1","name":"worker-sysctls","cluster":"cl_BWSYNTH","revision":"frv_BWSYNTH-head"},`+
			`{"id":"frg_2","name":"worker-sysctls","cluster":"cl_BWSYNTH-other","revision":"frv_BWSYNTH-elsewhere"}]}`)
	case "GET /drafts/drf_BWSYNTH":
		w.Header().Set("ETag", f.etag())
		fmt.Fprint(w, f.draft())
	case "POST /drafts":
		f.rev, f.state = 1, "open"
		w.Header().Set("ETag", f.etag())
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, f.draft())
	case "POST /drafts/drf_BWSYNTH/discard":
		if r.Header.Get("If-Match") != f.etag() {
			f.refuse(w)
			return
		}
		f.rev, f.state = f.rev+1, "discarded"
		fmt.Fprint(w, f.draft())
	default:
		if r.Header.Get("If-Match") != f.etag() {
			f.refuse(w)
			return
		}
		f.rev++
		w.Header().Set("ETag", f.etag())
		if f.empty {
			return
		}
		if f.answer != "" {
			fmt.Fprint(w, f.answer)
			return
		}
		if r.Method == http.MethodPut && strings.Contains(route, "/fragments/") {
			fmt.Fprint(w, `{"draft":"drf_BWSYNTH","entry":{"kind":"fragment","name":"worker-labels","base":1,"head":null,"revision":"frv_BWSYNTH-new"},"ingestion":"ing_BWSYNTH"}`)
			return
		}
		fmt.Fprint(w, `{}`)
	}
}

func (f *draftFake) refuse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusPreconditionFailed)
	fmt.Fprint(w, `{"type":"urn:bronzeward:problem:precondition-failed","title":"BWSYNTH","status":412,"instance":"req_BWSYNTH"}`)
}

func (f *draftFake) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.writes
	f.writes, f.reads = nil, nil
	return w
}

// TestProfileSetRepeat: the outcome-unknown block of profile set names the revisions it resolved;
// a repeat takes only those, resolves nothing, and is refused after item 3's reads alone when the
// epoch differs (client-contracts.md §8.4 item 4).
func TestProfileSetRepeat(t *testing.T) {
	f := &draftFake{rev: 2, state: "open"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	step := func(name string, args []string, code int, errText string, reads []string, writes ...string) {
		t.Helper()
		var o, e bytes.Buffer
		got := run(args, &o, &e)
		f.mu.Lock()
		read := f.reads
		f.mu.Unlock()
		took := f.took()
		if got != code || !strings.Contains(e.String(), errText) {
			t.Errorf("%s: exit %d, stderr %q; want %d, %q", name, got, e.String(), code, errText)
		}
		if reads != nil && strings.Join(read, ", ") != strings.Join(reads, ", ") {
			t.Errorf("%s: read %v, want %v", name, read, reads)
		}
		if strings.Join(took, "\n") != strings.Join(writes, "\n") {
			t.Errorf("%s: sent\n  %s\nwant\n  %s", name, strings.Join(took, "\n  "), strings.Join(writes, "\n  "))
		}
	}
	const put = `PUT /drafts/drf_BWSYNTH/profiles/workers if-match="2-BWSYNTH" type=application/json body={"fragments":["frv_BWSYNTH-proposed","frv_BWSYNTH-head"]}`
	step("show keeps the ETag", []string{"draft", "show", "drf_BWSYNTH"}, 0, "", nil)
	f.mu.Lock()
	f.lost = true
	f.mu.Unlock()
	step("a lost write names the revisions it pinned", []string{"profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "worker-labels,worker-sysctls"},
		3, "give the same input, with --fragments 'frv_BWSYNTH-proposed,frv_BWSYNTH-head', and: --idempotency-key ", nil, put)
	f.mu.Lock()
	f.lost = false
	f.mu.Unlock()
	repeat := []string{"profile", "set", "workers", "--draft", "drf_BWSYNTH", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa",
		"--expect-principal", "svc_BWSYNTH", `--if-match="2-BWSYNTH"`}
	step("a repeat that names a fragment", append(repeat, "--expect-epoch", "ep_BWSYNTH-1", "--fragments", "worker-labels,frv_BWSYNTH-head"),
		2, "a repeat of profile set gives --fragments as the revisions the outcome-unknown block printed, not 'worker-labels'", []string{})
	step("a repeat at another epoch reads only item 3's", append(repeat, "--expect-epoch", "ep_BWSYNTH-0", "--fragments", "frv_BWSYNTH-proposed,frv_BWSYNTH-head"),
		2, "not the given --expect-epoch ep_BWSYNTH-0", []string{"GET /identity", "GET /recovery"})
	step("a repeat resolves nothing and sends the same body", append(repeat, "--expect-epoch", "ep_BWSYNTH-1", "--fragments", "frv_BWSYNTH-proposed,frv_BWSYNTH-head"),
		0, "", []string{"GET /identity", "GET /recovery", "GET /server"}, put)

	// A command whose prep reads: a repeat at another epoch is refused before those reads.
	prepRead := false
	e := &env{output: "table", out: io.Discard, err: io.Discard}
	err := e.writeAfter(func(c *client.Client) error {
		prepRead = true
		_, err := c.Get("/drafts/drf_BWSYNTH")
		return err
	}, &client.Write{Method: http.MethodDelete, Path: "/drafts/drf_BWSYNTH/profiles/workers", Draft: "drf_BWSYNTH",
		Repeat: &client.Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: "ep_BWSYNTH-0", Principal: "svc_BWSYNTH"}}, nil)
	f.mu.Lock()
	read := f.reads
	f.mu.Unlock()
	f.took()
	var refused client.RefusedRepeat
	if !errors.As(err, &refused) || prepRead || strings.Join(read, ", ") != "GET /identity, GET /recovery" {
		t.Errorf("a repeat at another epoch with a reading prep: %v, prep ran %v, read %v; want refused before prep", err, prepRead, read)
	}

	// A write that succeeded with an answer -o yaml cannot convert still exits 0, the answer as served.
	f.mu.Lock()
	f.answer = `{"BWSYNTH":`
	f.mu.Unlock()
	var o, eb bytes.Buffer
	code := run([]string{"-o", "yaml", "profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "frv_BWSYNTH-given"}, &o, &eb)
	f.took()
	if code != 0 || o.String() != "{\"BWSYNTH\":\n" || !strings.Contains(eb.String(), "succeeded, but its answer cannot be read") {
		t.Errorf("-o yaml with a malformed answer: exit %d, stdout %q, stderr %q; want 0, the answer as served, the failure on stderr", code, o.String(), eb.String())
	}
	// profile set reads nothing of its answer: one that is not JSON is unreadable under -o json and
	// yaml alike, though YAML could convert it.
	f.mu.Lock()
	f.answer = "result: BWSYNTH"
	f.mu.Unlock()
	for _, mode := range []string{"json", "yaml"} {
		o.Reset()
		eb.Reset()
		code = run([]string{"-o", mode, "profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "frv_BWSYNTH-given"}, &o, &eb)
		f.took()
		if code != 0 || o.String() != "result: BWSYNTH\n" || !strings.Contains(eb.String(), "succeeded, but its answer cannot be read: the answer is not JSON") {
			t.Errorf("-o %s with an answer that is not JSON: exit %d, stdout %q, stderr %q; want 0, the answer as served, the failure on stderr", mode, code, o.String(), eb.String())
		}
	}
	// An empty answer is not JSON either: every draft write is answered with a body.
	f.mu.Lock()
	f.answer, f.empty = "", true
	f.mu.Unlock()
	for _, mode := range []string{"json", "yaml"} {
		o.Reset()
		eb.Reset()
		code = run([]string{"-o", mode, "profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "frv_BWSYNTH-given"}, &o, &eb)
		f.took()
		if code != 0 || o.String() != "\n" || !strings.Contains(eb.String(), "succeeded, but its answer cannot be read: the answer is not JSON") {
			t.Errorf("-o %s with an empty answer: exit %d, stdout %q, stderr %q; want 0, the empty answer, the failure on stderr", mode, code, o.String(), eb.String())
		}
	}
	f.mu.Lock()
	f.empty = false
	f.mu.Unlock()

	// The server entered recovery mode since the lost write: the repeat is refused after the identity
	// and confirming reads alone, with no GET /server between, and shows the recovery banner.
	f.mu.Lock()
	f.epoch, f.recovery = "ep_BWSYNTH-2", true
	f.mu.Unlock()
	o.Reset()
	eb.Reset()
	code = run(append(repeat, "--expect-epoch", "ep_BWSYNTH-1", "--fragments", "frv_BWSYNTH-proposed,frv_BWSYNTH-head"), &o, &eb)
	f.mu.Lock()
	read = f.reads
	f.mu.Unlock()
	if took := f.took(); code != 2 || len(took) != 0 || strings.Join(read, ", ") != "GET /identity, GET /recovery" ||
		!strings.Contains(eb.String(), "not the given --expect-epoch ep_BWSYNTH-1") || !strings.Contains(eb.String(), "RECOVERY MODE is on in ") {
		t.Errorf("a repeat after a recovery entry: exit %d, read %v, sent %v, stderr %q; want 2, only item 3's reads, nothing sent, the banner", code, read, took, eb.String())
	}
}

// shortWriter takes half of each write and fails it, as a closed pipe can; calls counts the writes.
type shortWriter struct{ calls int }

func (w *shortWriter) Write(p []byte) (int, error) {
	w.calls++
	return len(p) / 2, io.ErrShortWrite
}

// automationContext records the context "lab" on srv with a synthetic automation token.
func automationContext(t *testing.T, srv string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	tok := filepath.Join(home, "token")
	if err := os.WriteFile(tok, []byte("bwt_BWSYNTH.BWSYNTH-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Current, c.Contexts = "lab", []client.Context{{Name: "lab", Server: srv, Credential: "automation", Automation: &client.Automation{TokenFile: tok}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestDraftEditing runs the draft writes in order against one draft: T11's kept ETag (#256) from
// create, show and each write; its refusal with none kept; ETAG-01's 412, never refetched and
// resent; T17's pins; the request bodies; and discard forgetting the ETag.
func TestDraftEditing(t *testing.T) {
	f := &draftFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	step := func(name string, args []string, code int, out, errText string, writes ...string) {
		t.Helper()
		var o, e bytes.Buffer
		got := run(args, &o, &e)
		took := f.took()
		if got != code || !strings.Contains(o.String(), out) || !strings.Contains(e.String(), errText) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d, %q, %q", name, got, o.String(), e.String(), code, out, errText)
		}
		if strings.Join(took, "\n") != strings.Join(writes, "\n") {
			t.Errorf("%s: sent\n  %s\nwant\n  %s", name, strings.Join(took, "\n  "), strings.Join(writes, "\n  "))
		}
	}
	const ctype = "application/json"
	step("a write before any read", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "",
		"no ETag of draft drf_BWSYNTH is kept for this context at its current epoch. Read the draft first: bronzewardctl --context 'lab' draft show 'drf_BWSYNTH'")
	step("create", []string{"draft", "create", "--cluster", "cl_BWSYNTH", "--title", "BWSYNTH"}, 0, "Draft drf_BWSYNTH open, revision 1.", "",
		`POST /drafts if-match= type=`+ctype+` body={"cluster":"cl_BWSYNTH","title":"BWSYNTH"}`)
	step("profile set: proposed, head and given pins", []string{"profile", "set", "workers", "--draft", "drf_BWSYNTH",
		"--fragments", "worker-labels,worker-sysctls,frv_BWSYNTH-given"}, 0,
		"Profile workers saved: pins worker-labels frv_BWSYNTH-proposed, worker-sysctls frv_BWSYNTH-head, frv_BWSYNTH-given (draft revision 2).", "",
		`PUT /drafts/drf_BWSYNTH/profiles/workers if-match="1-BWSYNTH" type=`+ctype+` body={"fragments":["frv_BWSYNTH-proposed","frv_BWSYNTH-head","frv_BWSYNTH-given"]}`)
	step("profile set: a fragment the draft removes", []string{"profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "gone"}, 1, "",
		"draft drf_BWSYNTH removes the fragment 'gone', so a profile cannot pin it; nothing was sent")
	step("profile set: no such fragment", []string{"profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "absent"}, 1, "",
		"cluster cl_BWSYNTH has no fragment 'absent' with a revision")
	// An entry that cannot be read may be the draft's proposal for the fragment named, so no pin is chosen.
	f.mu.Lock()
	f.unreadable = true
	f.mu.Unlock()
	step("profile set: a draft entry that cannot be read", []string{"profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", "worker-sysctls"}, 1, "",
		"GET /api/v1/drafts/drf_BWSYNTH: an entry cannot be read")
	f.mu.Lock()
	f.unreadable = false
	f.mu.Unlock()
	step("assignment set", []string{"assignment", "set", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--profiles", "workers",
		"--fragment", "cluster=cluster-base", "--fragment", "role=a,b", "--fragment", "role=c"}, 0, "Assignment saved (draft revision 3).", "",
		`PUT /drafts/drf_BWSYNTH/assignments/mch_BWSYNTH if-match="2-BWSYNTH" type=`+ctype+
			` body={"fragments":{"cluster":["cluster-base"],"role":["a","b","c"]},"profiles":["workers"]}`)
	step("assignment set: an unknown layer", []string{"assignment", "set", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--fragment", "rack=a"}, 2, "",
		"the layer one of global, site, cluster, role, workload and override")
	step("profile rm: a DELETE with no body", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 0,
		"Profile workers removed from draft drf_BWSYNTH (draft revision 4).", "",
		`DELETE /drafts/drf_BWSYNTH/profiles/workers if-match="3-BWSYNTH" type= body=`)

	// ETAG-01: another author writes; the next write is refused once and not resent.
	f.mu.Lock()
	f.rev = 9
	f.mu.Unlock()
	step("a write after another author's", []string{"assignment", "rm", "mch_BWSYNTH", "--draft", "drf_BWSYNTH"}, 1, "",
		"The draft changed since you last read it (you saw revision 4, it is now revision 9). Nothing was saved. Review the change before writing again:\n  bronzewardctl --context 'lab' draft show 'drf_BWSYNTH'",
		`DELETE /drafts/drf_BWSYNTH/assignments/mch_BWSYNTH if-match="4-BWSYNTH" type= body=`)
	step("the same write again: the kept ETag was not refetched", []string{"assignment", "rm", "mch_BWSYNTH", "--draft", "drf_BWSYNTH"}, 1, "",
		"you saw revision 4, it is now revision 9",
		`DELETE /drafts/drf_BWSYNTH/assignments/mch_BWSYNTH if-match="4-BWSYNTH" type= body=`)
	step("--if-match gives the ETag", []string{"assignment", "rm", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", `--if-match="9-BWSYNTH"`}, 0,
		"Assignment mch_BWSYNTH removed from draft drf_BWSYNTH (draft revision 10).", "",
		`DELETE /drafts/drf_BWSYNTH/assignments/mch_BWSYNTH if-match="9-BWSYNTH" type= body=`)
	// The draft is read at a new epoch, and the GET /server that calls for shows a newer one still:
	// the ETag belongs to the older epoch, so none is kept (§8.5 item 1).
	f.mu.Lock()
	f.rev, f.epoch, f.serverEpoch = 12, "ep_BWSYNTH-2", "ep_BWSYNTH-3"
	f.mu.Unlock()
	step("draft show at an epoch the server read passed keeps none", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	step("so the write is refused before sending", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "", "Read the draft first")
	f.mu.Lock()
	f.epoch, f.serverEpoch = "", ""
	f.mu.Unlock()
	step("draft show keeps the ETag", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	step("discard sends {} with the shown ETag", []string{"draft", "discard", "drf_BWSYNTH"}, 0, "Draft drf_BWSYNTH discarded (revision 13).", "",
		`POST /drafts/drf_BWSYNTH/discard if-match="12-BWSYNTH" type=`+ctype+` body={}`)
	step("discard forgot the ETag", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "", "Read the draft first")
	step("draft show of a closed draft keeps none", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	step("so a write still needs a read", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "", "Read the draft first")
}

// TestProfileSetRefusesEpochBack: the draft, and for a listed name the fragment list, answer
// ep_BWSYNTH-1. The write's own principal read then shows ep_BWSYNTH-2, and its GET /server shows
// ep_BWSYNTH-1 again, so the last epoch equals the epoch of the pins. A change that a later answer
// set back is still a change (client-contracts.md section 8.6 item 3): nothing is sent. A control
// moves the epoch before the draft is read, and that send goes out.
func TestProfileSetRefusesEpochBack(t *testing.T) {
	const want = "the epoch changed after the request was built from reads of 'ep_BWSYNTH-1'"
	for _, c := range []struct {
		name     string
		fragment string // the name to pin
		move     func(*draftFake)
		refused  bool
	}{
		{"the draft proposes the pin", "worker-labels", func(f *draftFake) { f.identityEpoch = "ep_BWSYNTH-2" }, true},
		{"the pin is read from the fragment list", "worker-sysctls", func(f *draftFake) { f.identityEpoch = "ep_BWSYNTH-2" }, true},
		// Every answer of the write shows ep_BWSYNTH-2, so the move came before the draft was read.
		{"control: the move comes before the draft read", "worker-labels", func(f *draftFake) { f.epoch = "ep_BWSYNTH-2" }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &draftFake{rev: 1, state: "open"}
			srv := httptest.NewServer(f)
			defer srv.Close()
			automationContext(t, srv.URL)
			exec := func(args ...string) (int, string, string) {
				t.Helper()
				var o, e bytes.Buffer
				code := run(args, &o, &e)
				return code, o.String(), e.String()
			}
			if code, _, e := exec("draft", "show", "drf_BWSYNTH"); code != 0 {
				t.Fatalf("draft show: exit %d, stderr %q", code, e)
			}
			f.took()
			f.mu.Lock()
			c.move(f)
			f.mu.Unlock()
			// --if-match gives the write an ETag that a crossing did not drop, so only the epoch check can hold it back.
			code, out, errText := exec("profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", c.fragment, `--if-match="1-BWSYNTH"`)
			w := f.took()
			if c.refused {
				if code != 1 || out != "" || !strings.Contains(errText, want) || !strings.Contains(errText, "Nothing was sent") {
					t.Errorf("exit %d, stdout %q, stderr %q; want 1, nothing and %q", code, out, errText, want)
				}
				if len(w) != 0 {
					t.Errorf("sent %q; want nothing", w)
				}
				return
			}
			if code != 0 || len(w) != 1 {
				t.Errorf("exit %d, stderr %q, writes %q; want 0 and one PUT", code, errText, w)
			}
		})
	}
}

// TestProfileSetRefusesMixedPins: the pins of profile set come from the draft and, for a name the
// draft does not propose, from GET /fragments. The write then reads GET /server before it sends.
// When the epoch moves after the draft was read, at any of these reads, the pins are of an earlier
// snapshot: nothing is sent, and the refusal says so (client-contracts.md section 8.5).
func TestProfileSetRefusesMixedPins(t *testing.T) {
	const listWant = "the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2' while the pins were read, so nothing was sent; " +
		"read the draft again with: bronzewardctl draft show 'drf_BWSYNTH'"
	const sendWant = "not sent: the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2' after the request was built from reads of the earlier one. Nothing was sent. " +
		"Read again with: bronzewardctl --context 'lab' draft show 'drf_BWSYNTH', then run this command again"
	for _, c := range []struct {
		name     string
		fragment string // the name to pin
		move     func(*draftFake)
		want     string
	}{
		{"the fragment list shows the new epoch", "worker-sysctls", func(f *draftFake) { f.fragmentsEpoch, f.serverEpoch = "ep_BWSYNTH-2", "ep_BWSYNTH-2" }, listWant},
		// The write's own server read answers the first epoch again, so only the list answer itself shows the change.
		{"the fragment list shows it and the server read sets it back", "worker-sysctls", func(f *draftFake) { f.fragmentsEpoch = "ep_BWSYNTH-2" }, listWant},
		{"only the write's server read shows it", "worker-sysctls", func(f *draftFake) { f.serverEpoch = "ep_BWSYNTH-2" }, sendWant},
		{"the draft proposes the pin, so no list is read", "worker-labels", func(f *draftFake) { f.serverEpoch = "ep_BWSYNTH-2" }, sendWant},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &draftFake{rev: 1, state: "open"}
			srv := httptest.NewServer(f)
			defer srv.Close()
			automationContext(t, srv.URL)
			exec := func(args ...string) (int, string, string) {
				t.Helper()
				var o, e bytes.Buffer
				code := run(args, &o, &e)
				return code, o.String(), e.String()
			}
			if code, _, e := exec("draft", "show", "drf_BWSYNTH"); code != 0 {
				t.Fatalf("draft show: exit %d, stderr %q", code, e)
			}
			f.took()
			f.mu.Lock()
			c.move(f)
			f.mu.Unlock()
			// --if-match gives the write an ETag that the crossing did not drop, so only this check holds it back.
			code, out, errText := exec("profile", "set", "workers", "--draft", "drf_BWSYNTH", "--fragments", c.fragment, `--if-match="1-BWSYNTH"`)
			if code != 1 || out != "" || !strings.Contains(errText, c.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 1, nothing and %q", code, out, errText, c.want)
			}
			if w := f.took(); len(w) != 0 {
				t.Errorf("sent %q; want nothing", w)
			}
		})
	}
}

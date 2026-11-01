package main

import (
	"bytes"
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
}

func (f *draftFake) etag() string { return fmt.Sprintf(`"%d-BWSYNTH"`, f.rev) }

func (f *draftFake) draft() string {
	return fmt.Sprintf(`{"id":"drf_BWSYNTH","cluster":"cl_BWSYNTH","title":"BWSYNTH","state":%q,"revision":%d,"entries":[`+
		`{"kind":"fragment","name":"worker-labels","base":1,"head":"frv_BWSYNTH-base","revision":"frv_BWSYNTH-proposed"},`+
		`{"kind":"fragment","name":"gone","base":1,"head":"frv_BWSYNTH-gone","revision":null}]}`, f.state, f.rev)
}

func (f *draftFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
	w.Header().Set("Content-Type", "application/json")
	route := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.Method != http.MethodGet {
		body, _ := io.ReadAll(r.Body)
		f.writes = append(f.writes, fmt.Sprintf("%s if-match=%s type=%s body=%s", route, r.Header.Get("If-Match"), r.Header.Get("Content-Type"), body))
	}
	switch route {
	case "GET /server":
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
	case "GET /identity":
		fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
	case "GET /fragments":
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
	f.writes = nil
	return w
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
	f.mu.Lock()
	f.rev = 12
	f.mu.Unlock()
	step("draft show keeps the ETag", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	step("discard sends {} with the shown ETag", []string{"draft", "discard", "drf_BWSYNTH"}, 0, "Draft drf_BWSYNTH discarded (revision 13).", "",
		`POST /drafts/drf_BWSYNTH/discard if-match="12-BWSYNTH" type=`+ctype+` body={}`)
	step("discard forgot the ETag", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "", "Read the draft first")
	step("draft show of a closed draft keeps none", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	step("so a write still needs a read", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "", "Read the draft first")
}

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The required check of client-contracts.md §6 item 8: every string member of every response type
// holds ESC, a tab, a line feed, a carriage return, DEL and a C1 character, and each command that
// shows it runs in -o table. A run with plain values of the same shape is the baseline.
const (
	tail    = "\x1b\t\n\r\x7f\u0085\u202e\u2066"
	tailEsc = `\x1b\x09\x0a\x0d\x7f\x85\u202e\u2066`
	// inJSON is the same tail inside JSON text that the CLI encodes itself: the JSON encoder
	// writes the C0 characters as escapes and leaves DEL, the C1 range and the bidi characters raw,
	// so the rule escapes those.
	inJSON = `\u001b\t\n\r\x7f\x85\u202e\u2066`
	// bidi holds the characters that change the order in which a terminal draws text.
	bidi = "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"
)

// filler builds the server's answers. A marker names each string; want maps it to the form in which
// the output must show it.
type filler struct {
	control bool
	yes     bool   // every boolean member is true
	omit    string // the Type.Member that the answers leave out
	n       int
	want    map[string]string
	docs    map[string][]string // the escaped lines of each document and diff, by its marker
	src     map[string]string   // the response type and member that each marker fills
	owner   string
	member  string
}

func newFiller(control bool) *filler {
	return &filler{control: control, want: map[string]string{}, docs: map[string][]string{}, src: map[string]string{}}
}

func (f *filler) mark() string {
	f.n++
	m := fmt.Sprintf("zQ%dz", f.n)
	f.src[m] = f.owner + "." + f.member
	return m
}

func (f *filler) text() string {
	m := f.mark()
	if !f.control {
		return m
	}
	f.want[m] = m + tailEsc
	return m + tail
}

// doc is a document: its own line feeds split it, so the tail holds none.
func (f *filler) doc() string {
	m := f.mark()
	if !f.control {
		return m + " a b\n" + m + " c\n"
	}
	f.docs[m] = []string{m + ` a\x1b\x09\x0d\x7f\x85\u202e b`, m + " c"}
	return m + " a\x1b\t\r\x7f\u0085\u202e b\n" + m + " c\n"
}

// raw is a JSON object as the server writes it: DEL, a C1 character and a byte that is not UTF-8
// stay raw in it, so the CLI sees them when it encodes the member again.
func (f *filler) raw() json.RawMessage {
	m := f.mark()
	if !f.control {
		return json.RawMessage(`{"k":"` + m + `"}`)
	}
	f.want[m] = m + inJSON + `\xff`
	return json.RawMessage(`{"k":"` + m + `\u001b\t\n\r` + "\x7f\u0085\u202e\u2066\xff" + `"}`)
}

// notShown is every response member, as Type.Member, that no command of the sweep shows.
const notShown = "AccessVersionBody.Created AccessVersionBody.Path ActItem.Epoch ActItem.IdempotencyKey ActItem.RequestId " +
	"AttemptAccounting.Basis AttemptBody.Observation AttemptResponse.Class " +
	"Embedded.Format Embedded.Path EndpointChangeBody.Act EndpointChangeBody.ChangedBy EndpointChangeBody.Epoch " +
	"EndpointChangeBody.Role Hardware.Serial IdentityBody.Issuer IdentityBody.Subject " +
	"ObservationBody.AssignmentEvidence ObservationBody.Controller ObservationBody.Health ObservationBody.Machine " +
	"ObservationBody.Operation ObservationBody.Plan ObservationBody.ResourceVersion ObservationBody.RunningVersion " +
	"ObservationBody.SmbiosUuid ObservationBody.TalosClusterId ObservationBody.TalosNodeId " +
	"ObservedBody.Observation OpenDriftBody.LastObservation OpenDriftBody.LateLanding " +
	"RecoveryEntryRef.EnteredBy RecoveryEntryRef.Reason RecoveryExitRef.Act RecoveryExitRef.ExitedBy " +
	"Reference.Encoding ReleaseMachineBody.Review RendererBody.MachineryChecksum " +
	"RestoredBackup.Backup RestoredBackup.Family ServerBody.Features ServerBody.StartMode TimelineEntry.Epoch UnreadValue.Code"

// markersShown is, for each command of the sweep with plain booleans, how many marked values it
// shows. The sweep credits a member to any command that shows it, so a command that stops showing
// a member another command still shows (or shows a placeholder for it) changes this number.
var markersShown = map[string]int{
	"status": 5, "version": 1, "whoami": 5, "context list": 3,
	"cluster list": 6, "cluster show c1": 5,
	"machine list": 7, "machine show m1": 11, "machine timeline m1": 3, "machine observations m1": 6,
	"fragment show f1": 6, "fragment show frv_1": 8, "fragment history f1": 5,
	"profile show p1": 5, "profile show prv_1": 5, "profile history p1": 5,
	"assignment show a1": 4, "assignment show asr_1": 6, "assignment history a1": 6,
	"draft list": 5, "draft show d1": 6, "release list": 6, "release show r1": 15,
	"plan list": 6, "plan show pl1": 27, "operation list": 8, "operation show o1": 15, "act list": 8,
	"cluster add --name n --endpoint https://e --contract c --talos-cluster-id t":    5,
	"machine add --cluster c1 --talos-node-id n --platform metal --talos-endpoint e": 4,
	"machine set-endpoint m1 --talos-endpoint e":                                     3,
	"draft create --cluster c1 --title t":                                            2,
}

var timeType = reflect.TypeFor[time.Time]()

// tree is a value of type t as JSON-ready data, with every string and every map key a marked one.
func (f *filler) tree(t reflect.Type, field string) any {
	switch {
	case t == timeType:
		return "2026-10-26T10:00:00Z"
	case t.Kind() == reflect.String:
		if field == "Document" || field == "Unified" {
			return f.doc()
		}
		return f.text()
	case t.Kind() == reflect.Int || t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		return 2
	case t.Kind() == reflect.Bool:
		return f.yes
	case t.Kind() == reflect.Pointer:
		return f.tree(t.Elem(), field)
	case t.Kind() == reflect.Slice:
		return []any{f.tree(t.Elem(), field)}
	case t.Kind() == reflect.Map:
		if field == "Fragments" {
			// The CLI shows the fragments of a layer it knows, so the key is one of them.
			return map[string]any{"global": f.tree(t.Elem(), field)}
		}
		return map[string]any{f.text(): f.tree(t.Elem(), field)}
	case t.Kind() == reflect.Interface:
		m := f.mark()
		if f.control {
			f.want[m] = m + inJSON
			return map[string]any{"k": m + tail}
		}
		return map[string]any{"k": m}
	case t.Kind() == reflect.Struct && t.NumField() == 1 && t.Field(0).Name == "union":
		if t.Name() == "DraftBody_Entries_Item" {
			return map[string]any{"kind": "fragment", "name": f.text(), "base": 1, "head": f.text(), "revision": f.text()}
		}
		return f.raw()
	case t.Kind() == reflect.Struct:
		out := map[string]any{}
		for sf := range t.Fields() {
			if name, _, _ := strings.Cut(sf.Tag.Get("json"), ","); sf.IsExported() && name != "" && name != "-" && t.Name()+"."+sf.Name != f.omit {
				f.owner, f.member = t.Name(), sf.Name
				out[name] = f.tree(sf.Type, sf.Name)
			}
		}
		return out
	}
	panic("no tree for " + t.String())
}

// answer is the JSON of one value of v's type.
func (f *filler) answer(v any) string {
	b, err := json.Marshal(f.tree(reflect.TypeOf(v), ""))
	if err != nil {
		panic(err)
	}
	return string(b)
}

// page is a list answer of one item and a cursor; last is the same without the cursor, which a
// read of every page gets for its second request.
func (f *filler) page(v any) (body, last string) {
	items := `{"items":[` + f.answer(v) + `]`
	f.owner, f.member = "Page", "next"
	next, _ := json.Marshal(f.text())
	return items + `,"next":` + string(next) + `}`, items + `}`
}

type route struct {
	re         *regexp.Regexp
	body, last string
}

// routes are the server's answers, built in a fixed order so that the plain run has the same shape.
func (f *filler) routes() []route {
	var rs []route
	add := func(re, body, last string) { rs = append(rs, route{regexp.MustCompile("^" + re + "$"), body, last}) }
	one := func(re string, v any) { b := f.answer(v); add(re, b, b) }
	list := func(re string, v any) { b, last := f.page(v); add(re, b, last) }
	one(`GET /server`, apitypes.ServerBody{})
	one(`GET /recovery`, apitypes.RecoveryBody{})
	one(`GET /identity`, apitypes.IdentityBody{})
	list(`GET /clusters`, apitypes.ClusterBody{})
	one(`GET /clusters/[^/]+`, apitypes.ClusterBody{})
	list(`GET /machines`, apitypes.MachineBody{})
	one(`GET /machines/[^/]+`, apitypes.MachineBody{})
	list(`GET /machines/[^/]+/timeline`, apitypes.TimelineEntry{})
	list(`GET /machines/[^/]+/observations`, apitypes.ObservationBody{})
	list(`GET /fragments`, apitypes.FragmentBody{})
	one(`GET /fragments/[^/]+`, apitypes.FragmentBody{})
	list(`GET /fragments/[^/]+/revisions`, apitypes.FragmentRevisionBody{})
	one(`GET /fragment-revisions/[^/]+`, apitypes.FragmentRevisionBody{})
	list(`GET /profiles/[^/]+/revisions`, apitypes.ProfileRevisionBody{})
	one(`GET /profiles/[^/]+`, apitypes.ProfileBody{})
	one(`GET /profile-revisions/[^/]+`, apitypes.ProfileRevisionBody{})
	list(`GET /assignments/[^/]+/revisions`, apitypes.AssignmentRevisionBody{})
	one(`GET /assignments/[^/]+`, apitypes.AssignmentBody{})
	one(`GET /assignment-revisions/[^/]+`, apitypes.AssignmentRevisionBody{})
	list(`GET /drafts`, apitypes.DraftBody{})
	one(`GET /drafts/[^/]+`, apitypes.DraftBody{})
	list(`GET /releases`, apitypes.ReleaseBody{})
	one(`GET /releases/[^/]+`, apitypes.ReleaseBody{})
	list(`GET /plans`, apitypes.PlanBody{})
	one(`GET /plans/[^/]+`, apitypes.PlanBody{})
	list(`GET /operations`, apitypes.OperationBody{})
	one(`GET /operations/[^/]+`, apitypes.OperationBody{})
	list(`GET /acts`, apitypes.ActItem{})
	one(`POST /clusters`, apitypes.ClusterBody{})
	one(`POST /machines`, apitypes.MachineBody{})
	one(`POST /machines/[^/]+/talos-endpoints`, apitypes.EndpointChangeBody{})
	one(`POST /drafts`, apitypes.DraftBody{})
	return rs
}

// commands are the commands that show a response, one per source of remote text.
var commands = [][]string{
	{"status"}, {"version"}, {"whoami"}, {"context", "list"},
	{"cluster", "list"}, {"cluster", "show", "c1"},
	{"machine", "list"}, {"machine", "show", "m1"}, {"machine", "timeline", "m1"}, {"machine", "observations", "m1"},
	{"fragment", "show", "f1"}, {"fragment", "show", "frv_1"}, {"fragment", "history", "f1"},
	{"profile", "show", "p1"}, {"profile", "show", "prv_1"}, {"profile", "history", "p1"},
	{"assignment", "show", "a1"}, {"assignment", "show", "asr_1"}, {"assignment", "history", "a1"},
	{"draft", "list"}, {"draft", "show", "d1"}, {"release", "list"}, {"release", "show", "r1"},
	{"plan", "list"}, {"plan", "show", "pl1"}, {"operation", "list"}, {"operation", "show", "o1"}, {"act", "list"},
	{"cluster", "add", "--name", "n", "--endpoint", "https://e", "--contract", "c", "--talos-cluster-id", "t"},
	{"machine", "add", "--cluster", "c1", "--talos-node-id", "n", "--platform", "metal", "--talos-endpoint", "e"},
	{"machine", "set-endpoint", "m1", "--talos-endpoint", "e"},
	{"draft", "create", "--cluster", "c1", "--title", "t"},
}

// sweep runs commands against a server that answers with f's routes, in the given -o format.
func sweep(t *testing.T, f *filler, output string) (outs, errs []string) {
	t.Helper()
	rs := f.routes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("ETag", `"2-x"`)
		w.Header().Set("Content-Type", "application/json")
		for _, rt := range rs {
			if rt.re.MatchString(key) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusCreated)
				}
				body := rt.body
				if r.URL.Query().Get("cursor") != "" {
					body = rt.last
				}
				fmt.Fprint(w, body)
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	// The contexts file is a source too: a name, a server and a credential kind that it holds.
	f.owner, f.member = "Context", "contexts file"
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Contexts = append(c.Contexts, client.Context{Name: f.text(), Server: f.text(), Credential: f.text()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range commands {
		var o, e bytes.Buffer
		if got := run(append([]string{"-o", output}, c...), &o, &e); got != 0 {
			t.Errorf("%v exited %d: %s", c, got, e.String())
		}
		outs, errs = append(outs, o.String()), append(errs, e.String())
	}
	return outs, errs
}

// TestRemoteTextIsEscaped: in -o table, no output holds a raw control byte or invalid UTF-8, every
// marked value shows in its escaped form, and the lines and columns are those of a run with plain
// values (client-contracts.md §6 item 8).
func TestRemoteTextIsEscaped(t *testing.T) {
	seen := map[string]bool{}
	// The second run sets every boolean member, which shows the recovery-mode members.
	// The third run leaves out a machine's open drift, which shows the release observed instead.
	for _, run := range []struct {
		yes  bool
		omit string
	}{{false, ""}, {true, ""}, {false, "MachineBody.OpenDrift"}} {
		t.Run(fmt.Sprintf("booleans=%v omit=%q", run.yes, run.omit), func(t *testing.T) { checkSweep(t, run.yes, run.omit, seen) })
	}
	// A member that no command shows proves nothing, so each is named: the list below is every
	// member of a response type that the commands of this test never show.
	var hidden []string
	for s, shown := range seen {
		if !shown {
			hidden = append(hidden, s)
		}
	}
	slices.Sort(hidden)
	if got := strings.Join(hidden, " "); got != notShown {
		t.Errorf("members that no command shows:\n%s\nwant:\n%s", got, notShown)
	}
}

// checkSweep runs the commands with marked values, and again with plain ones, and checks both.
// It records in seen, by Type.Member, whether any command showed the member.
func checkSweep(t *testing.T, yes bool, omit string, seen map[string]bool) {
	bad, plain := newFiller(true), newFiller(false)
	bad.yes, plain.yes = yes, yes
	bad.omit, plain.omit = omit, omit
	badOut, badErr := sweep(t, bad, "table")
	plainOut, plainErr := sweep(t, plain, "table")
	for i, c := range commands {
		name := strings.Join(c, " ")
		// Both runs number their markers alike, so a command must show each marker the plain run
		// shows, as often, and in its escaped form: a value replaced by a placeholder fails here
		// even when another command still shows the same member.
		withPlain, withBad := plainOut[i]+"\n"+plainErr[i], badOut[i]+"\n"+badErr[i]
		shownMarkers := 0
		for m := range bad.src {
			if strings.Contains(withBad, m) {
				shownMarkers++
			}
		}
		if want, ok := markersShown[name]; !yes && omit == "" && (!ok || shownMarkers != want) {
			t.Errorf("%s shows %d marked values, want %d: a value is lost or a placeholder replaces it", name, shownMarkers, want)
		}
		for m, want := range bad.want {
			n := strings.Count(withPlain, m)
			if got, esc := strings.Count(withBad, m), strings.Count(withBad, want); got != n || esc != n {
				t.Errorf("%s: %s shows %d times, %d in its escaped form %q; the plain run shows it %d times", name, m, got, esc, want, n)
			}
		}
		for _, text := range []string{badOut[i], badErr[i]} {
			if !utf8.ValidString(text) {
				t.Errorf("%s: the output holds invalid UTF-8: %q", name, text)
			}
			for _, r := range text {
				if r != '\n' && unicode.IsControl(r) || strings.ContainsRune(bidi, r) {
					t.Errorf("%s: the output holds the raw control character %U: %q", name, r, text)
					break
				}
			}
		}
		got, plain := strings.Split(badOut[i], "\n"), strings.Split(plainOut[i], "\n")
		if len(got) != len(plain) {
			t.Errorf("%s: %d lines, %d with plain values:\n%s", name, len(got), len(plain), badOut[i])
			continue
		}
		for j := range got {
			if len(strings.Fields(got[j])) != len(strings.Fields(plain[j])) {
				t.Errorf("%s: line %d has other columns than with plain values:\n%q\n%q", name, j, got[j], plain[j])
			}
		}
	}
	all := strings.Join(append(badOut, badErr...), "\n")
	for m, want := range bad.want {
		n, w := strings.Count(all, m), strings.Count(all, want)
		if n != w {
			t.Errorf("%s shows %d times and in its escaped form %q %d times", m, n, want, w)
		}
		seen[bad.src[m]] = seen[bad.src[m]] || n > 0
	}
	shownDocs := 0
	for m, ls := range bad.docs {
		if !strings.Contains(all, m) {
			continue // a history row shows no document
		}
		shownDocs++
		for _, l := range ls {
			if !strings.Contains(all, "\n  "+l+"\n") && !strings.Contains(all, "\n    "+l+"\n") {
				t.Errorf("the document line %q is not shown whole and escaped", l)
			}
		}
	}
	// With every boolean true a release or plan shows no diff, so that run shows the document alone.
	if want := map[bool]int{false: 2, true: 1}[yes]; shownDocs != want {
		t.Errorf("%d documents and diffs shown, want %d", shownDocs, want)
	}
	if !strings.Contains(all, "start mode unknown") {
		t.Errorf("the start mode the server sent is not shown as unknown")
	}
	// -o json keeps the server's members but writes DEL, the C1 range and the bidirectional
	// controls as \uNNNN, which the JSON encoder leaves raw.
	jsonOut, _ := sweep(t, newFiller(true), "json")
	for i, o := range jsonOut {
		if !json.Valid([]byte(o)) {
			t.Errorf("%s: -o json is not valid JSON: %q", strings.Join(commands[i], " "), o)
		}
		for _, r := range o {
			if r == 0x7f || r >= 0x80 && r <= 0x9f || strings.ContainsRune(bidi, r) || r != '\n' && unicode.IsControl(r) {
				t.Errorf("%s: -o json holds the raw character %U", strings.Join(commands[i], " "), r)
				break
			}
		}
	}
}

// TestRemoteTextInOwnErrors: an error that the CLI words itself from a remote value shows it
// escaped (client-contracts.md §6 item 8), here a draft and a cluster ID in profile set's refusals.
func TestRemoteTextInOwnErrors(t *testing.T) {
	id, cluster := "d1"+tail, "c1"+tail
	draft, _ := json.Marshal(map[string]any{"id": id, "cluster": cluster, "title": "t", "state": "open", "revision": 2,
		"entries": []any{map[string]any{"kind": "fragment", "name": "gone", "base": 1, "head": "frv_h", "revision": nil}}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/server":
			fmt.Fprint(w, `{"version":"v","apiRevision":2,"startMode":"normal"}`)
		case "/api/v1/fragments":
			fmt.Fprint(w, `{"items":[]}`)
		default:
			w.Write(draft)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	for fragment, want := range map[string]string{
		"gone":  `draft d1\x1b\x09\x0a\x0d\x7f\x85\u202e\u2066 removes the fragment`,
		"other": `cluster c1\x1b\x09\x0a\x0d\x7f\x85\u202e\u2066 has no fragment`,
	} {
		var o, e bytes.Buffer
		run([]string{"profile", "set", "p", "--draft", "d1", "--fragments", fragment}, &o, &e)
		if !strings.Contains(e.String(), want) {
			t.Errorf("--fragments %s: the error %q lacks %q", fragment, e.String(), want)
		}
		for _, r := range e.String() {
			if r != '\n' && unicode.IsControl(r) {
				t.Errorf("--fragments %s: the error holds the raw control character %U", fragment, r)
				break
			}
		}
	}
}

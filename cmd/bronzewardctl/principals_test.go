package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/id"
)

func strp(s string) *string { return &s }

// TestPrincipalText checks client contracts A7's fallbacks on the item GET /principals answers: a
// human by its display name, or by subject and issuer when it has none; a service by its name; the
// full identifier, which the CLI prints today, for an identifier answered unknown, one not read, or
// an item that names nobody. The web client keeps the same text for the same item (principals.ts).
func TestPrincipalText(t *testing.T) {
	const who = "idn_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	human := func(display *string, subject, issuer *string) *apitypes.PrincipalBody {
		return &apitypes.PrincipalBody{Id: who, Kind: apitypes.PrincipalBodyKind("human"), Display: display, Subject: subject, Issuer: issuer}
	}
	for _, c := range []struct {
		name string
		item *apitypes.PrincipalBody
		want string
	}{
		{"display", human(strp("Ada Lovelace"), strp("u-1"), strp("https://idp.example")), "Ada Lovelace"},
		{"no display", human(nil, strp("u-1"), strp("https://idp.example")), "u-1 at https://idp.example"},
		{"empty display", human(strp(""), strp("u-1"), strp("https://idp.example")), "u-1 at https://idp.example"},
		{"human naming nobody", human(nil, nil, nil), who},
		{"service", &apitypes.PrincipalBody{Id: who, Kind: apitypes.PrincipalBodyKind("service"), Service: strp("ci-bot")}, "ci-bot"},
		{"service with no name", &apitypes.PrincipalBody{Id: who, Kind: apitypes.PrincipalBodyKind("service")}, who},
		{"unknown", &apitypes.PrincipalBody{Id: who, Kind: apitypes.PrincipalBodyKind("unknown")}, who},
		{"not read", nil, who},
	} {
		if got := principalText(c.item, who); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// principalFake serves acts, releases and GET /principals. Every principal read is recorded; the
// principals answered are idn_aa..., idn_ab..., ... in the order of pid, by kind of i mod 4:
// a display name, a subject and issuer, a service, unknown.
type principalFake struct {
	mu       sync.Mutex
	acts     []string // each act's principal
	calls    [][]string
	failRead bool
	display  func(i int) string // the display name of the human with a name, "Name <i>" when nil
	timeline string             // the answer of GET /machines/mch_a/timeline
	recovery string             // the answer of GET /recovery
	moved    bool               // GET /principals answers in a new epoch
	flip     bool               // the first GET /principals answers in a new epoch and the next in the old one
}

func pid(i int) string {
	return "idn_" + string([]byte{'a' + byte(i/26), 'a' + byte(i%26)}) + strings.Repeat("a", 24)
}

func (f *principalFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch path {
	case "/server":
		if f.moved && len(f.calls) > 0 {
			w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		}
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":10,"startMode":"normal"}`)
	case "/acts":
		var items []string
		for i, p := range f.acts {
			items = append(items, fmt.Sprintf(`{"id":"act_%d","at":"2026-11-20T04:00:00Z","action":"x","epoch":"ep_BWSYNTH-1","principal":%q,"principalKind":"human","role":"author","via":"token","subjects":[]}`, i, p))
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
	case "/recovery":
		fmt.Fprint(w, f.recovery)
	case "/machines/mch_a/timeline":
		fmt.Fprint(w, f.timeline)
	case "/releases/rel_BWSYNTH":
		fmt.Fprintf(w, `{"id":"rel_BWSYNTH","cluster":"cl_BWSYNTH","draft":"drf_BWSYNTH","draftRevision":1,"operation":"apply-config","publishedAt":"2026-11-20T04:00:00Z",`+
			`"publishedBy":{"principal":%q,"role":"publisher"},"machines":[],"sources":[],"renderer":{}}`, pid(1))
	case "/principals":
		ids := r.URL.Query()["id"]
		f.calls = append(f.calls, ids)
		if f.moved || f.flip && len(f.calls) == 1 {
			w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		}
		if f.failRead {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:dependency-unavailable","title":"Service Unavailable","status":503,"detail":"BWSYNTH busy"}`)
			return
		}
		var items []string
		for _, p := range ids {
			items = append(items, f.item(p))
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *principalFake) item(p string) string {
	i := int(p[4]-'a')*26 + int(p[5]-'a')
	switch i % 4 {
	case 0:
		d := fmt.Sprintf("Name %d", i)
		if f.display != nil {
			d = f.display(i)
		}
		b, _ := json.Marshal(d)
		return fmt.Sprintf(`{"id":%q,"kind":"human","issuer":"https://idp.example","subject":"sub-%d","display":%s,"service":null}`, p, i, b)
	case 1:
		return fmt.Sprintf(`{"id":%q,"kind":"human","issuer":"https://idp.example","subject":"sub-%d","display":null,"service":null}`, p, i)
	case 2:
		return fmt.Sprintf(`{"id":%q,"kind":"service","issuer":null,"subject":null,"display":null,"service":"svc-%d"}`, p, i)
	}
	return fmt.Sprintf(`{"id":%q,"kind":"unknown","issuer":null,"subject":null,"display":null,"service":null}`, p)
}

func (f *principalFake) read() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func principalServer(t *testing.T, acts []string) *principalFake {
	f := &principalFake{acts: acts}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	return f
}

// TestPrincipalNamesInList runs act list over 250 distinct principals, repeated, and one that is no
// principal identifier, and checks A7's request rule (at most 100 identifiers a call, each given
// once, an identifier the route would refuse never sent) and each fallback in the table.
func TestPrincipalNamesInList(t *testing.T) {
	var acts []string
	for i := 0; i < 250; i++ {
		acts = append(acts, pid(i))
	}
	acts = append(acts, pid(0), pid(1), "h-publisher")
	f := principalServer(t, acts)
	for i := 0; i < 250; i++ {
		if err := id.MustHave(pid(i), id.Principal); err != nil {
			t.Fatalf("the fixture's identifier %d is not a principal identifier: %v", i, err)
		}
	}
	code, out, errOut := runCtl(t, "act", "list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	seen := map[string]bool{}
	var calls int
	for _, ids := range f.read() {
		calls++
		if len(ids) > 100 {
			t.Errorf("a call with %d identifiers", len(ids))
		}
		for _, s := range ids {
			if seen[s] {
				t.Errorf("%s asked twice", s)
			}
			if s == "h-publisher" {
				t.Errorf("an identifier the route refuses was sent")
			}
			seen[s] = true
		}
	}
	if len(seen) != 250 || calls != 3 {
		t.Errorf("%d identifiers in %d calls, want 250 in 3", len(seen), calls)
	}
	for _, want := range []string{"Name 0 (human)", "sub-1 at https://idp.example (human)", "svc-2 (human)", pid(3) + " (human)", "h-publisher (human)"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, pid(0)) || strings.Contains(out, pid(1)) {
		t.Errorf("a resolved identifier is still printed:\n%s", out)
	}
}

// TestPrincipalNamesJSONAndYAML: -o json and -o yaml make no read of GET /principals and print what
// they printed before, with the identifiers; a table run on the same fixture does read it (the
// control that makes the zero mean something).
func TestPrincipalNamesJSONAndYAML(t *testing.T) {
	f := principalServer(t, []string{pid(0), pid(1)})
	var outs []string
	for _, o := range []string{"json", "yaml"} {
		code, out, errOut := runCtl(t, "act", "list", "-o", o)
		if code != 0 {
			t.Fatalf("-o %s: exit %d: %s", o, code, errOut)
		}
		if !strings.Contains(out, pid(0)) || !strings.Contains(out, pid(1)) || strings.Contains(out, "Name 0") {
			t.Errorf("-o %s:\n%s", o, out)
		}
		outs = append(outs, out)
	}
	if n := len(f.read()); n != 0 {
		t.Errorf("%d principal reads for -o json and -o yaml", n)
	}
	f.failRead = true // the same output whether or not the route answers
	if _, out, _ := runCtl(t, "act", "list", "-o", "json"); out != outs[0] {
		t.Errorf("-o json changed:\n%s\nwas\n%s", out, outs[0])
	}
	f.failRead = false
	if code, _, _ := runCtl(t, "act", "list"); code != 0 || len(f.read()) == 0 {
		t.Errorf("the table run read no principals (exit %d): the zero above proves nothing", code)
	}
}

// TestPrincipalReadFails: a failed read of the names is not a failed command; the full identifier
// is printed, and nothing of the failure reaches the output.
func TestPrincipalReadFails(t *testing.T) {
	f := principalServer(t, []string{pid(0)})
	f.failRead = true
	code, out, errOut := runCtl(t, "act", "list")
	if code != 0 || !strings.Contains(out, pid(0)+" (human)") || strings.Contains(out+errOut, "BWSYNTH busy") {
		t.Errorf("exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

// TestPrincipalNamesAreEscaped: a display name is remote text (client contracts §6 item 8), so a
// terminal sequence, a line break and a bidirectional control in it are shown escaped.
func TestPrincipalNamesAreEscaped(t *testing.T) {
	f := principalServer(t, []string{pid(0)})
	f.display = func(int) string { return "\x1b[31mred\x1b[0m\nnext\u202eevil" }
	code, out, _ := runCtl(t, "act", "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.ContainsAny(out, "\x1b\u202e") || strings.Count(out, "\n") != 2 {
		t.Errorf("raw control text in\n%q", out)
	}
	for _, want := range []string{`\x1b[31mred`, `\x0anext`, `\u202eevil`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

// TestPrincipalNamesInShow: a detail output resolves the one principal it shows, once.
func TestPrincipalNamesInShow(t *testing.T) {
	f := principalServer(t, nil)
	code, out, errOut := runCtl(t, "release", "show", "rel_BWSYNTH")
	if code != 0 || !strings.Contains(out, "published by sub-1 at https://idp.example (publisher)") {
		t.Errorf("exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	if c := f.read(); len(c) != 1 || len(c[0]) != 1 || c[0][0] != pid(1) {
		t.Errorf("reads %v", c)
	}
}

// TestPrincipalNamesInPlans: plan show names the plan's creator and plan cancel names who
// recorded the cancellation, by display name, with the plan's own reads unchanged; -o json shows the
// identifier.
func TestPrincipalNamesInPlans(t *testing.T) {
	f := planServer(t)
	who := pid(5)
	f.set(func(f *planFake) {
		f.raw = map[string]string{"/principals": fmt.Sprintf(`{"items":[{"id":%q,"kind":"human","issuer":"https://idp.example","subject":"sub-5","display":"Ada","service":null}]}`, who)}
		f.post = func(w http.ResponseWriter, _ string) {
			f.state = "cancelled"
			fmt.Fprintf(w, `{"plan":"pln_BWSYNTH","machine":"mch_a","cancelledBy":%q,"role":"publisher","reason":"x","act":"act_BWSYNTH","epoch":"ep_BWSYNTH-2","at":"2026-11-01T06:10:00Z"}`, who)
		}
	})
	plan := strings.Replace(f.plan(), `"principal":"svc_BWSYNTH"`, `"principal":"`+who+`"`, 1)
	f.set(func(f *planFake) { f.raw["/plans/pln_BWSYNTH"] = plan })
	code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
	if code != 0 || !strings.Contains(out, "Ada (publisher)") || strings.Contains(out, who) {
		t.Errorf("plan show: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	code, out, errOut = runCtl(t, "plan", "show", "pln_BWSYNTH", "-o", "json")
	if code != 0 || !strings.Contains(out, who) || strings.Contains(out, "Ada") {
		t.Errorf("plan show -o json: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	code, out, errOut = runCtl(t, "plan", "cancel", "pln_BWSYNTH", "--reason", "x")
	if code != 0 || !strings.Contains(out, "recorded by Ada (publisher), act act_BWSYNTH.") {
		t.Errorf("plan cancel: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

// TestPrincipalNamesInTimeline: a machine timeline names the principals its plan, approval and
// ownership entries hold, from one read for the page.
func TestPrincipalNamesInTimeline(t *testing.T) {
	f := principalServer(t, nil)
	f.timeline = fmt.Sprintf(`{"items":[`+
		`{"revision":1,"at":"2026-11-20T04:00:00Z","kind":"plan","entry":{"plan":"pln_a","operation":"apply-config","release":"rel_a","principal":%q,"role":"publisher"}},`+
		`{"revision":2,"at":"2026-11-20T04:01:00Z","kind":"approval","entry":{"approval":"apr_a","plan":"pln_a","principal":%q,"role":"approver","selfApproval":{"marked":false,"reasons":[]},"unseenOverwrite":false}},`+
		`{"revision":3,"at":"2026-11-20T04:02:00Z","kind":"accounting","entry":{"operation":"op_a","attempt":"att_a","decidedBy":%q,"role":"recovery-admin"}},`+
		`{"revision":4,"at":"2026-11-20T04:03:00Z","kind":"freeze","entry":{"principal":%q,"role":"approver","reason":"r"}},`+
		`{"revision":5,"at":"2026-11-20T04:04:00Z","kind":"scope-state","entry":{"from":"normal","to":"pre-restore-unaccounted","cause":"recovery-entry","enteredBy":%q}}]}`, pid(1), pid(0), pid(1), pid(0), pid(1))
	code, out, errOut := runCtl(t, "machine", "timeline", "mch_a")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"by sub-1 at https://idp.example (publisher)", "by Name 0 (approver)", "decided by sub-1 at https://idp.example",
		`"principal":"Name 0"`, `"enteredBy":"sub-1 at https://idp.example"`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if c := f.read(); len(c) != 1 || len(c[0]) != 2 {
		t.Errorf("reads %v, want one read of two identifiers", c)
	}
}

// TestPrincipalNamesInRecoveryStatus: the names are read before the epoch check, so a restore that
// the names read sees stops the command as a restore seen by any other read would.
func TestPrincipalNamesInRecoveryStatus(t *testing.T) {
	f := principalServer(t, nil)
	f.recovery = fmt.Sprintf(`{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"entry":{"enteredBy":%q,"enteredAt":"2026-11-01T08:40:00Z",`+
		`"stoppedAt":"2026-11-01T08:31:00Z","reason":"drill","restored":[]},"exit":null,"scopes":[]}`, pid(0))
	code, out, errOut := runCtl(t, "recovery", "status")
	if code != 0 || !strings.Contains(out, "by Name 0: drill") {
		t.Errorf("exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
	f.moved = true
	code, out, errOut = runCtl(t, "recovery", "status")
	if code == 0 || out != "" || !strings.Contains(errOut, "the epoch changed from") {
		t.Errorf("a restore seen by the names read: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}

	// The entry's and the exit's names are two reads: the first sees a new epoch and the second the old
	// one again, which leaves the epoch equal and the moves counted.
	f = principalServer(t, nil)
	f.recovery = fmt.Sprintf(`{"epoch":"ep_BWSYNTH-1","recoveryMode":false,"entry":{"enteredBy":%q,"enteredAt":"2026-11-01T08:40:00Z",`+
		`"stoppedAt":"2026-11-01T08:31:00Z","reason":"drill","restored":[]},"exit":{"exitedBy":%q,"at":"2026-11-01T09:00:00Z","act":"act_x"},"scopes":[]}`, pid(0), pid(4))
	f.flip = true
	code, out, errOut = runCtl(t, "recovery", "status")
	if code == 0 || out != "" || !strings.Contains(errOut, "the epoch changed from 'ep_BWSYNTH-1' and changed back") {
		t.Errorf("a move and a move back: exit %d, out\n%s\nerr\n%s", code, out, errOut)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

// TestEntryRepeatFlags: the recovery entry's repeat takes a key and a principal, never an epoch or
// If-Match (client-contracts.md §8.4 item 4).
func TestEntryRepeatFlags(t *testing.T) {
	key := "BWSYNTH-aaaaaaaaaaaaaaa"
	g := func(v string) given { return given{v, true} }
	for _, c := range []struct {
		name string
		f    repeatFlags
		want string
	}{
		{"none: a first send", repeatFlags{}, "first"},
		{"key and principal", repeatFlags{key: g(key), principal: g("iss|sub")}, ""},
		{"an epoch", repeatFlags{g(key), g("ep_1"), g("iss|sub"), given{}}, "its repeat takes only --idempotency-key and --expect-principal"},
		{"if-match", repeatFlags{key: g(key), principal: g("iss|sub"), ifMatch: g(`"e"`)}, "its repeat takes only"},
		{"no principal", repeatFlags{key: g(key)}, "a repeat needs --idempotency-key and --expect-principal"},
		{"a key not as printed", repeatFlags{key: g("short"), principal: g("iss|sub")}, "give the key as printed"},
		{"an empty principal", repeatFlags{key: g(key), principal: g("")}, "was given an empty value"},
	} {
		r, err := c.f.entryRepeat()
		var u usageError
		switch {
		case c.want == "first" && (r != nil || err != nil):
			t.Errorf("%s: %v, %v; want a first send", c.name, r, err)
		case c.want == "" && (err != nil || r == nil || r.Key != key || r.Epoch != "" || r.Principal != "iss|sub"):
			t.Errorf("%s: %v, %v; want the repeat without an epoch", c.name, r, err)
		case c.want != "" && c.want != "first" && (!errors.As(err, &u) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v; want a usage error saying %q", c.name, err, c.want)
		}
	}
}

func TestRestoredFlag(t *testing.T) {
	var r restoredFlag
	if err := r.Set("family=database,backup=BWSYNTH-pg,taken-at=2026-11-01T08:30:00Z"); err != nil || len(r) != 1 || r[0].Backup != "BWSYNTH-pg" {
		t.Fatalf("%v, %v", err, r)
	}
	for _, bad := range []string{"family=database,backup=BWSYNTH-pg", "family=database,backup=b,taken-at=yesterday", "family=database,backup=b,taken-at=2026-11-01T08:30:00Z,x=y"} {
		if err := r.Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestRestoredFlagErrorUsesTheContractEncoding: a rejected component is shown as client-contracts
// section 6 item 8 writes it: a tab as \x09, a backslash unchanged, a quote as \x22.
func TestRestoredFlagErrorUsesTheContractEncoding(t *testing.T) {
	recoveryServer(t, nil, "")
	var out, errb bytes.Buffer
	code := run([]string{"recovery", "enter", "--restored", "family=database,x\ty\\z\"", "--stopped-at", "2026-11-01T08:31:00Z", "--reason", "r"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), `"x\x09y\z\x22": give family=<f>,backup=<id>,taken-at=<time>`) {
		t.Errorf("exit %d; want 2 and the contract's encoding of the component:\n%s", code, errb.String())
	}
	if strings.ContainsAny(errb.String(), "\t") || strings.Contains(errb.String(), `\t`) || strings.Contains(errb.String(), `\\`) {
		t.Errorf("a Go-quoted form reached the terminal:\n%q", errb.String())
	}
}

// recoveryEpochHeader is the epoch every answer of recoveryServer carries; a test sets it with
// setEpochHeader, before it starts the server.
var recoveryEpochHeader = "ep_BWSYNTH-1"

func setEpochHeader(t *testing.T, epoch string) {
	t.Cleanup(func() { recoveryEpochHeader = "ep_BWSYNTH-1" })
	recoveryEpochHeader = epoch
}

// recoveryEpochByPath names the epoch that every answer carries from the first read of a path on, and
// recoveryRevision is the API revision GET /server shows; a test sets them with setServerFacts.
var (
	recoveryEpochByPath map[string]string
	recoveryRevision    = 3
	// recoveryRevisionMoved, when not 0, is the revision GET /server shows once the epoch moved.
	recoveryRevisionMoved = 0
	// recoveryRefreshEpoch, when not empty, is the epoch of the first GET /server read after the epoch
	// moved: the epoch moves a second time before the client refreshes its server facts.
	recoveryRefreshEpoch = ""
)

func setServerFacts(t *testing.T, revision int, epochByPath map[string]string) {
	t.Cleanup(func() {
		recoveryEpochByPath, recoveryRevision, recoveryRevisionMoved, recoveryRefreshEpoch = nil, 3, 0, ""
	})
	recoveryEpochByPath, recoveryRevision = epochByPath, revision
}

// recoveryPosted holds the body of every POST recoveryServer saw, in order; a test that reads it
// resets it before the server starts. The handler runs in the server's goroutines, so every access
// goes through recoveryPostedMu: posted and resetPosted for a test, the handler's own append.
var (
	recoveryPosted   []string
	recoveryPostedMu sync.Mutex
)

func posted() []string {
	recoveryPostedMu.Lock()
	defer recoveryPostedMu.Unlock()
	return slices.Clone(recoveryPosted)
}

func resetPosted() {
	recoveryPostedMu.Lock()
	defer recoveryPostedMu.Unlock()
	recoveryPosted = nil
}

// recoveryServer is a fake server for the recovery commands, answering reads from answers by
// method and path, and every POST 201 with post's body. It lists the requests it saw.
func recoveryServer(t *testing.T, answers map[string]string, post string) func() []string {
	var mu sync.Mutex
	var seen []string
	var moved string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			recoveryPostedMu.Lock()
			recoveryPosted = append(recoveryPosted, string(b))
			recoveryPostedMu.Unlock()
		}
		mu.Unlock()
		// Once a path in recoveryEpochByPath is read, the epoch moved: every later answer carries it.
		mu.Lock()
		// A key may name one page: the path with "?cursor=<cursor>".
		for _, k := range []string{r.URL.Path, r.URL.Path + "?cursor=" + r.URL.Query().Get("cursor")} {
			if ep, ok := recoveryEpochByPath[k]; ok {
				moved = ep
			}
		}
		if r.URL.Path == "/api/v1/server" && moved != "" && recoveryRefreshEpoch != "" {
			moved = recoveryRefreshEpoch
		}
		epoch := recoveryEpochHeader
		if moved != "" {
			epoch = moved
		}
		revision := recoveryRevision
		if moved != "" && recoveryRevisionMoved != 0 {
			revision = recoveryRevisionMoved
		}
		mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", epoch)
		w.Header().Set("Content-Type", "application/json")
		key := r.Method + " " + r.URL.Path
		if cur := r.URL.Query().Get("cursor"); cur != "" {
			key += "?cursor=" + cur
		}
		// An answer keyed with "?order=newest" is sent only to a request that asks for that order.
		if o := r.URL.Query().Get("order"); o != "" {
			if _, ok := answers[key+"?order="+o]; ok {
				key += "?order=" + o
			}
		}
		switch a, ok := answers[key]; {
		case r.URL.Path == "/api/v1/server":
			fmt.Fprintf(w, `{"version":"BWSYNTH","apiRevision":%d,"startMode":"recovery","settleFloorSeconds":30,"maxTransportDeadlineSeconds":120}`, revision)
		case r.URL.Path == "/api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case ok:
			// An answer that starts with a three-digit status and a space is sent with that status;
			// a 3xx has a Location, an error is a problem document.
			if code, body, found := strings.Cut(a, " "); found && len(code) == 3 && code[0] >= '3' && code[0] <= '5' {
				status := int(code[0]-'0')*100 + int(code[1]-'0')*10 + int(code[2]-'0')
				if status < 400 {
					w.Header().Set("Location", "/elsewhere")
				} else {
					w.Header().Set("Content-Type", "application/problem+json")
				}
				w.WriteHeader(status)
				a = body
			}
			fmt.Fprint(w, a)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, post)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	tok := filepath.Join(home, "token")
	if err := os.WriteFile(tok, []byte("bwt_BWSYNTH.BWSYNTH-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Current, c.Contexts = "lab", []client.Context{{Name: "lab", Server: srv.URL, Credential: "automation", Automation: &client.Automation{TokenFile: tok}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// withStdin runs do with standard input reading s from a file.
func withStdin(t *testing.T, s string, do func()) {
	f, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err == nil {
		_, err = f.WriteString(s)
	}
	if err == nil {
		_, err = f.Seek(0, 0)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := stdin
	stdin = f
	defer func() { stdin = old }()
	do()
}

// TestTypedConfirmations: recovery enter and identity revoke send only when the confirmation is
// typed back; --no-input, an empty answer and a wrong one exit 2 and send nothing.
func TestTypedConfirmations(t *testing.T) {
	enter := []string{"recovery", "enter", "--restored", "family=database,backup=BWSYNTH-pg,taken-at=2026-11-01T08:30:00Z",
		"--stopped-at", "2026-11-01T08:31:00Z", "--reason", "BWSYNTH drill"}
	revoke := []string{"identity", "revoke", "--identity", "idn_BWSYNTH", "--reason", "BWSYNTH left"}
	for _, c := range []struct {
		args          []string
		typed, effect string
		route         string
	}{
		{enter, "enter", "It marks every machine's scope pre-restore-unaccounted.", "POST /api/v1/recovery/entries"},
		{revoke, "idn_BWSYNTH", "It adds no auth.deniedSubjects entry and reissues no token", "POST /api/v1/identity-revocations"},
	} {
		seen := recoveryServer(t, nil, `{"act":"act_BWSYNTH","identity":"idn_BWSYNTH","deniedSubjectsListed":true}`)
		var out, errb bytes.Buffer
		if code := run(append(slices.Clone(c.args), "--no-input"), &out, &errb); code != 2 || len(seen()) != 0 {
			t.Errorf("%s --no-input: exit %d, sent %v; want 2 and nothing\n%s", c.args[0], code, seen(), errb.String())
		}
		for _, typed := range []string{"", "\n", "BWSYNTH-wrong\n", c.typed + "x\n"} {
			errb.Reset()
			withStdin(t, typed, func() {
				if code := run(c.args, &out, &errb); code != 2 || slices.Contains(seen(), c.route) || out.Len() != 0 {
					t.Errorf("%s typed %q: exit %d, sent %v; want 2 and no write\n%s", c.args[0], typed, code, seen(), errb.String())
				}
			})
		}
		if !strings.Contains(errb.String(), c.effect) || !strings.Contains(errb.String(), "nothing was sent") {
			t.Errorf("%s: the effects or the refusal were not shown:\n%s", c.args[0], errb.String())
		}
		withStdin(t, c.typed+"\n", func() {
			if code := run(c.args, &out, &errb); code != 0 || !slices.Contains(seen(), c.route) {
				t.Errorf("%s typed back: exit %d, sent %v\n%s", c.args[0], code, seen(), errb.String())
			}
		})
	}
}

// TestRecoveryStatus: T31's rows, one per scope in recovery, each with its next step from the reads
// it needs.
func TestRecoveryStatus(t *testing.T) {
	recoveryServer(t, map[string]string{
		"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"entry":{"enteredBy":"prn_BWSYNTH","enteredAt":"2026-11-01T08:40:00Z",` +
			`"stoppedAt":"2026-11-01T08:31:00Z","reason":"BWSYNTH drill","restored":[{"family":"database","backup":"BWSYNTH-pg","takenAt":"2026-11-01T08:30:00Z"}]},` +
			`"exit":null,"scopes":[{"machine":"mch_a","state":"pre-restore-unaccounted"},{"machine":"mch_b","state":"unresolved"},` +
			`{"machine":"mch_c","state":"blocked"},{"machine":"mch_d","state":"ready"},{"machine":"mch_e","state":"released"},{"machine":"mch_f","state":"normal"}]}`,
		"GET /api/v1/operations":              `{"items":[{"id":"op_b","kind":"apply-config","state":"unresolved"}]}`,
		"GET /api/v1/operations/op_b":         `{"id":"op_b","kind":"apply-config","state":"unresolved","attempts":[{"number":1}]}`,
		"GET /api/v1/machines/mch_c/timeline": `{"items":[{"kind":"scope-state","revision":9,"entry":{"to":"blocked","cause":"restoration-mark","missing":["provider","talos-access"]}}]}`,
	}, "")
	var out, errb bytes.Buffer
	if code := run([]string{"recovery", "status"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	flat := strings.Join(strings.Fields(out.String()), " ") // the table's column widths aside
	for _, want := range []string{"Recovery mode on", "Start mode recovery", "BWSYNTH drill", "database backup BWSYNTH-pg taken at 2026-11-01 08:30:00 UTC",
		"mch_a pre-restore-unaccounted account the scope: recovery scope-account 'mch_a'",
		"mch_b unresolved operation op_b holds it, with an attempt: classified after the normal restart, then recovery mark 'mch_b'",
		"mch_c blocked missing provider, talos-access: then recovery mark 'mch_c' again, or a new release for a lost key version",
		"mch_d ready release: recovery release 'mch_d'", "mch_e released none"} {
		if !strings.Contains(flat, want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "mch_f") || strings.Contains(out.String(), "Every scope is released") {
		t.Errorf("a normal scope was listed, or the exit offered with scopes not released:\n%s", out.String())
	}
}

// TestRecoveryStatusUnresolvedWithNoHolder: a restored operation that was cancelled or classified
// no longer holds its scope, but the scope stays unresolved until it is marked again. An empty
// lookup advises the mark; a failed lookup keeps the command that shows the operations.
func TestRecoveryStatusUnresolvedWithNoHolder(t *testing.T) {
	for _, tc := range []struct{ name, list, want string }{
		{"empty lookup", `{"items":[]}`, "mch_b unresolved no unresolved operation holds it now: recovery mark 'mch_b'"},
		{"failed lookup", `404 {"type":"about:blank","title":"x","status":404}`, "mch_b unresolved an operation holds it: operation list --machine 'mch_b'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recoveryServer(t, map[string]string{
				"GET /api/v1/recovery":   `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_b","state":"unresolved"}]}`,
				"GET /api/v1/operations": tc.list,
			}, "")
			var out, errb bytes.Buffer
			if code := run([]string{"recovery", "status"}, &out, &errb); code != 0 {
				t.Fatalf("exit %d\n%s", code, errb.String())
			}
			if flat := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(flat, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out.String())
			}
		})
	}
}

// TestRecoveryStatusReadsTheNewestMark: the timeline holds an old and a new restoration mark; the
// server answers oldest first unless the request says order=newest, so dropping the order would
// report the keys of the old mark.
func TestRecoveryStatusReadsTheNewestMark(t *testing.T) {
	recoveryServer(t, map[string]string{
		"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_c","state":"blocked"}]}`,
		"GET /api/v1/machines/mch_c/timeline": `{"items":[{"kind":"scope-state","revision":3,"entry":{"to":"blocked","cause":"restoration-mark","missing":["old-key"]}},` +
			`{"kind":"scope-state","revision":9,"entry":{"to":"blocked","cause":"restoration-mark","missing":["new-key"]}}]}`,
		"GET /api/v1/machines/mch_c/timeline?order=newest": `{"items":[{"kind":"scope-state","revision":9,"entry":{"to":"blocked","cause":"restoration-mark","missing":["new-key"]}},` +
			`{"kind":"scope-state","revision":3,"entry":{"to":"blocked","cause":"restoration-mark","missing":["old-key"]}}]}`,
	}, "")
	var out, errb bytes.Buffer
	if code := run([]string{"recovery", "status"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "missing new-key") || strings.Contains(out.String(), "old-key") {
		t.Errorf("status did not name the newest mark's keys alone:\n%s", out.String())
	}
}

// TestRecoveryStatusFollowsTheTimeline: a blocked scope's latest restoration mark can lie on a later
// page of the timeline, behind newer observations, and status still names what it misses. Only the
// end of the history, with no mark found, says the scope was not marked.
func TestRecoveryStatusFollowsTheTimeline(t *testing.T) {
	scopes := `"exit":null,"scopes":[{"machine":"mch_c","state":"blocked"}]}`
	for _, c := range []struct{ name, page2, want string }{
		{"mark on page 2", `{"items":[{"kind":"scope-state","entry":{"to":"blocked","cause":"restoration-mark","missing":["provider"]}}]}`,
			"missing provider: then recovery mark 'mch_c' again"},
		{"no mark in the history", `{"items":[{"kind":"observation","entry":{}}]}`, "not marked since it was blocked: recovery mark 'mch_c'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			recoveryServer(t, map[string]string{
				"GET /api/v1/recovery":                          `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,` + scopes,
				"GET /api/v1/machines/mch_c/timeline":           `{"items":[{"kind":"observation","entry":{}}],"next":"p2"}`,
				"GET /api/v1/machines/mch_c/timeline?cursor=p2": c.page2,
			}, "")
			var out, errb bytes.Buffer
			if code := run([]string{"recovery", "status"}, &out, &errb); code != 0 {
				t.Fatalf("exit %d\n%s", code, errb.String())
			}
			if flat := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(flat, c.want) {
				t.Errorf("output lacks %q:\n%s", c.want, out.String())
			}
		})
	}
}

// TestRecoveryStatusStructuredSkipsNextSteps: -o json and -o yaml print the answer as it came, so
// they read no operation or timeline for the next steps.
func TestRecoveryStatusStructuredSkipsNextSteps(t *testing.T) {
	for _, o := range []string{"json", "yaml"} {
		seen := recoveryServer(t, map[string]string{
			"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_b","state":"unresolved"},{"machine":"mch_c","state":"blocked"}]}`,
		}, "")
		var out, errb bytes.Buffer
		if code := run([]string{"-o", o, "recovery", "status"}, &out, &errb); code != 0 {
			t.Fatalf("-o %s: exit %d\n%s", o, code, errb.String())
		}
		for _, r := range seen() {
			if strings.Contains(r, "/operations") || strings.Contains(r, "/timeline") {
				t.Errorf("-o %s read %s for a next step", o, r)
			}
		}
	}
}

// TestRecoveryStatusRefusesAStaleSnapshot: when a next-step read shows a new epoch, the table shows
// nothing and says to read again; when the epoch holds, the same status prints its table.
func TestRecoveryStatusRefusesAStaleSnapshot(t *testing.T) {
	answers := map[string]string{
		"GET /api/v1/recovery":                `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_c","state":"blocked"}]}`,
		"GET /api/v1/machines/mch_c/timeline": `{"items":[{"kind":"scope-state","revision":9,"entry":{"to":"blocked","cause":"restoration-mark","missing":["provider"]}}]}`,
	}
	t.Run("the epoch moves during the timeline read", func(t *testing.T) {
		setServerFacts(t, 3, map[string]string{"/api/v1/machines/mch_c/timeline": "ep_BWSYNTH-2"})
		recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		rc := run([]string{"recovery", "status"}, &out, &errb)
		if rc == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2'") {
			t.Errorf("rc %d, out %q, err %q; want a refusal that shows no table", rc, out.String(), errb.String())
		}
	})
	t.Run("the epoch holds", func(t *testing.T) {
		recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		if rc := run([]string{"recovery", "status"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), "missing provider") {
			t.Errorf("rc %d, out %q, err %q; want the table", rc, out.String(), errb.String())
		}
	})
}

// TestMachineShowRefusesAMixedSnapshot: the table of machine show derives its status sentences from
// the machine read and from every page of its observations. When an epoch change falls between those
// reads, the machine and the observations come from different snapshots, so the table is not shown
// (section 8.5). An epoch change that comes with the machine read itself leaves both reads in one epoch.
// The -o json and -o yaml forms print the machine read alone and read no observations.
func TestMachineShowRefusesAMixedSnapshot(t *testing.T) {
	answers := map[string]string{
		"GET /api/v1/machines/mch_a": `{"id":"mch_a","cluster":"c1","platform":"metal","talosEndpoint":"10.0.0.1","hardware":{"talosNodeId":"n1"},` +
			`"scopeState":"normal","frozen":false,"pendingConvergence":false,"applied":null,"desired":null,"observed":null,"openDrift":null}`,
		"GET /api/v1/machines/mch_a/observations":           `{"items":[],"next":"p2"}`,
		"GET /api/v1/machines/mch_a/observations?cursor=p2": `{"items":[]}`,
	}
	for name, moves := range map[string]string{
		"the first observation page":  "/api/v1/machines/mch_a/observations",
		"the second observation page": "/api/v1/machines/mch_a/observations?cursor=p2",
	} {
		t.Run("the epoch moves with "+name, func(t *testing.T) {
			setServerFacts(t, 3, map[string]string{moves: "ep_BWSYNTH-2"})
			recoveryServer(t, answers, "")
			var out, errb bytes.Buffer
			rc := run([]string{"machine", "show", "mch_a"}, &out, &errb)
			if rc == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2'") ||
				!strings.Contains(errb.String(), "Read it again with: machine show 'mch_a'") {
				t.Errorf("rc %d, out %q, err %q; want a refusal that shows no table", rc, out.String(), errb.String())
			}
		})
	}
	t.Run("the epoch moves with the machine read", func(t *testing.T) {
		setServerFacts(t, 3, map[string]string{"/api/v1/machines/mch_a": "ep_BWSYNTH-2"})
		recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		if rc := run([]string{"machine", "show", "mch_a"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), "mch_a") {
			t.Errorf("rc %d, out %q, err %q; want the table", rc, out.String(), errb.String())
		}
	})
	t.Run("the epoch moves again before the server refresh", func(t *testing.T) {
		// The machine answer shows epoch 2; the refresh of GET /server, and then the observations, show epoch 3.
		setServerFacts(t, 3, map[string]string{"/api/v1/machines/mch_a": "ep_BWSYNTH-2"})
		recoveryRefreshEpoch = "ep_BWSYNTH-3"
		recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		rc := run([]string{"machine", "show", "mch_a"}, &out, &errb)
		if rc == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "the epoch changed from 'ep_BWSYNTH-2' to 'ep_BWSYNTH-3'") {
			t.Errorf("rc %d, out %q, err %q; want a refusal that shows no table", rc, out.String(), errb.String())
		}
	})
	t.Run("the epoch moves with an observation page and the refresh moves it back", func(t *testing.T) {
		// The observations answer epoch 2 and the refresh of GET /server answers epoch 1 again: the
		// epoch of the last answer equals the first, and only the count of moves tells (§8.5, §8.6 item 3).
		setServerFacts(t, 3, map[string]string{"/api/v1/machines/mch_a/observations": "ep_BWSYNTH-2"})
		recoveryRefreshEpoch = "ep_BWSYNTH-1"
		recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		rc := run([]string{"machine", "show", "mch_a"}, &out, &errb)
		if rc == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "the epoch moved and came back to 'ep_BWSYNTH-1'") ||
			!strings.Contains(errb.String(), "Read it again with: machine show 'mch_a'") {
			t.Errorf("rc %d, out %q, err %q; want a refusal that shows no table", rc, out.String(), errb.String())
		}
	})
	t.Run("the epoch holds", func(t *testing.T) {
		recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		if rc := run([]string{"machine", "show", "mch_a"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), "mch_a") {
			t.Errorf("rc %d, out %q, err %q; want the table", rc, out.String(), errb.String())
		}
	})
	t.Run("-o json reads the machine alone", func(t *testing.T) {
		setServerFacts(t, 3, map[string]string{"/api/v1/machines/mch_a/observations": "ep_BWSYNTH-2"})
		seen := recoveryServer(t, answers, "")
		var out, errb bytes.Buffer
		if rc := run([]string{"machine", "show", "mch_a", "-o", "json"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), `"id":"mch_a"`) {
			t.Errorf("rc %d, out %q, err %q; want the machine", rc, out.String(), errb.String())
		}
		for _, r := range seen() {
			if strings.Contains(r, "observations") {
				t.Errorf("-o json read the observations: %v", seen())
			}
		}
	})
}

// TestStatusRefusesAStaleSnapshot: status builds one snapshot from GET /server and three later reads.
// When an epoch change falls between the first read and the last, the server answer, with its
// revision and start mode, is stale, so the command shows nothing in every output form (section 8.5).
func TestStatusRefusesAStaleSnapshot(t *testing.T) {
	answers := map[string]string{
		"GET /api/v1/recovery":   `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[]}`,
		"GET /api/v1/machines":   `{"items":[]}`,
		"GET /api/v1/operations": `{"items":[]}`,
	}
	for _, form := range [][]string{{"status"}, {"status", "-o", "json"}} {
		for _, moves := range []string{"/api/v1/recovery", "/api/v1/machines", "/api/v1/operations"} {
			t.Run(strings.Join(form, " ")+" "+moves, func(t *testing.T) {
				setServerFacts(t, 3, map[string]string{moves: "ep_BWSYNTH-2"})
				recoveryRevisionMoved = 2
				recoveryServer(t, answers, "")
				var out, errb bytes.Buffer
				rc := run(form, &out, &errb)
				if rc == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "the epoch changed from 'ep_BWSYNTH-1' to 'ep_BWSYNTH-2'") ||
					!strings.Contains(errb.String(), "Read it again with: status") {
					t.Errorf("rc %d, out %q, err %q; want a refusal that shows no snapshot", rc, out.String(), errb.String())
				}
			})
			t.Run(strings.Join(form, " ")+" "+moves+" moves and the refresh moves it back", func(t *testing.T) {
				// A later read answers epoch 2 and the refresh of GET /server answers epoch 1 again: the
				// epoch of the last answer equals the first, and only the count of moves tells (§8.5, §8.6 item 3).
				setServerFacts(t, 3, map[string]string{moves: "ep_BWSYNTH-2"})
				recoveryRefreshEpoch = "ep_BWSYNTH-1"
				recoveryServer(t, answers, "")
				var out, errb bytes.Buffer
				rc := run(form, &out, &errb)
				if rc == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "the epoch moved and came back to 'ep_BWSYNTH-1'") ||
					!strings.Contains(errb.String(), "Read it again with: status") {
					t.Errorf("rc %d, out %q, err %q; want a refusal that shows no snapshot", rc, out.String(), errb.String())
				}
			})
		}
		t.Run(strings.Join(form, " ")+" epoch holds", func(t *testing.T) {
			recoveryServer(t, answers, "")
			var out, errb bytes.Buffer
			if rc := run(form, &out, &errb); rc != 0 || !strings.Contains(out.String(), "3") {
				t.Errorf("rc %d, out %q, err %q; want the snapshot", rc, out.String(), errb.String())
			}
		})
	}
}

// TestRecoveryStatusNamesTheRevisionForMissingKeys: a server of API revision 2 refuses order on the
// timeline (client-contracts section 8.6 item 6), so the client does not send it (section 7 item 1)
// and the next step says what the keys need.
func TestRecoveryStatusNamesTheRevisionForMissingKeys(t *testing.T) {
	setServerFacts(t, 2, nil)
	seen := recoveryServer(t, map[string]string{
		"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_c","state":"blocked"}]}`,
	}, "")
	var out, errb bytes.Buffer
	if code := run([]string{"recovery", "status"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	flat := strings.Join(strings.Fields(out.String()), " ")
	if want := "the missing keys need API revision 3; see machine timeline 'mch_c', then recovery mark 'mch_c'"; !strings.Contains(flat, want) {
		t.Errorf("output lacks %q:\n%s", want, out.String())
	}
	for _, r := range seen() {
		if strings.Contains(r, "/timeline") {
			t.Errorf("a revision 2 server was sent %s", r)
		}
	}
}

// TestRecoveryStatusGatesOnTheRefreshedRevision: a next-step read shows a new epoch, the client reads
// GET /server again, and that read shows API revision 2. The next scope then gets no order on its
// timeline (section 7 item 1), though the first read showed revision 3.
func TestRecoveryStatusGatesOnTheRefreshedRevision(t *testing.T) {
	setServerFacts(t, 3, map[string]string{"/api/v1/operations": "ep_BWSYNTH-2"})
	recoveryRevisionMoved = 2
	seen := recoveryServer(t, map[string]string{
		"GET /api/v1/recovery":   `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_b","state":"unresolved"},{"machine":"mch_c","state":"blocked"}]}`,
		"GET /api/v1/operations": `{"items":[]}`,
	}, "")
	var out, errb bytes.Buffer
	run([]string{"recovery", "status"}, &out, &errb)
	reqs := seen()
	if !slices.Contains(reqs, "GET /api/v1/operations") {
		t.Fatalf("no operations read, so the epoch never moved: %v", reqs)
	}
	for _, r := range reqs {
		if strings.Contains(r, "/timeline") {
			t.Errorf("a revision 2 server was sent %s after the refresh", r)
		}
	}
}

// TestRecoveryStatusGatesEachTimelinePage: the first timeline page shows a new epoch, and the re-read
// of GET /server shows API revision 2. The client then sends no second page with an order.
func TestRecoveryStatusGatesEachTimelinePage(t *testing.T) {
	setServerFacts(t, 3, map[string]string{"/api/v1/machines/mch_c/timeline": "ep_BWSYNTH-2"})
	recoveryRevisionMoved = 2
	seen := recoveryServer(t, map[string]string{
		"GET /api/v1/recovery":                `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_c","state":"blocked"}]}`,
		"GET /api/v1/machines/mch_c/timeline": `{"items":[{"kind":"observation","revision":9,"entry":{}}],"next":"cur_BWSYNTH"}`,
	}, "")
	var out, errb bytes.Buffer
	run([]string{"recovery", "status"}, &out, &errb)
	n := 0
	for _, r := range seen() {
		if strings.Contains(r, "/timeline") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d timeline requests; want the first page only, as the refreshed revision is 2", n)
	}
}

// TestRecoveryStatusGatesTheCursorRestart: the second timeline page answers 400 cursor-invalid with a
// new epoch, and the re-read of GET /server shows API revision 2. The client then does not read the
// first page again with an order.
func TestRecoveryStatusGatesTheCursorRestart(t *testing.T) {
	const p = "/api/v1/machines/mch_c/timeline"
	setServerFacts(t, 3, map[string]string{p + "?cursor=cur_BWSYNTH": "ep_BWSYNTH-2"})
	recoveryRevisionMoved = 2
	seen := recoveryServer(t, map[string]string{
		"GET /api/v1/recovery":             `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[{"machine":"mch_c","state":"blocked"}]}`,
		"GET " + p:                         `{"items":[{"kind":"observation","revision":9,"entry":{}}],"next":"cur_BWSYNTH"}`,
		"GET " + p + "?cursor=cur_BWSYNTH": `400 {"type":"urn:bronzeward:problem:cursor-invalid","title":"BWSYNTH","status":400,"instance":"req_BWSYNTH"}`,
	}, "")
	var out, errb bytes.Buffer
	run([]string{"recovery", "status"}, &out, &errb)
	n := 0
	for _, r := range seen() {
		if r == "GET "+p {
			n++
		}
	}
	// The fake records paths only: the first page and the refused cursor's page make two reads, a restart a third.
	if n != 2 {
		t.Errorf("%d timeline reads; want 2, with no restart on a revision 2 server", n)
	}
}

// TestRecoveryTextIsEscaped: every string of a server's answer that the recovery commands print is
// escaped as a value, a line feed included, so none can forge a row or move the cursor
// (client-contracts.md section 6 item 8).
func TestRecoveryTextIsEscaped(t *testing.T) {
	// Every field has its own marker with the whole character set of the rule: ESC, a line feed, a
	// tab, a C1 control, a bidi override and DEL.
	mk := func(n string) string { return n + "\x1b[2J\n\t\u0085\u202e\x7f" + n }
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	// The status epoch must equal the header's, which cannot hold ESC, a line feed or DEL.
	epoch := "EPOCH\u0085‮EPOCH"
	setEpochHeader(t, epoch)
	recoveryServer(t, map[string]string{
		"GET /api/v1/recovery": `{"epoch":` + js(epoch) + `,"recoveryMode":true,"entry":{"enteredBy":` + js(mk("BY")) + `,"enteredAt":"2026-11-01T08:40:00Z","stoppedAt":"2026-11-01T08:31:00Z",` +
			`"reason":` + js(mk("WHY")) + `,"restored":[{"family":` + js(mk("FAMILY")) + `,"backup":` + js(mk("BACKUP")) + `,"takenAt":"2026-11-01T08:30:00Z"}]},"exit":null,` +
			`"scopes":[{"machine":` + js("mch_"+mk("MACH")) + `,"state":"released"},{"machine":"mch_s","state":` + js(mk("STATE")) + `}]}`,
	}, `{"act":`+js(mk("ACT"))+`,"epoch":`+js(mk("NEWEPOCH"))+`,"previousEpoch":`+js(mk("PREV"))+`,"identity":`+js(mk("IDENT"))+`,"note":`+js(mk("NOTE"))+`,"deniedSubjectsListed":true,`+
		`"machines":1,"operations":[`+js(mk("OP"))+`],"scopes":[`+js(mk("SCOPE"))+`]}`)
	for _, c := range []struct {
		args  []string
		typed string
		shows string
		marks []string
	}{
		{[]string{"recovery", "status"}, "", "Epoch", []string{"EPOCH", "BY", "WHY", "FAMILY", "BACKUP", "MACH", "STATE"}},
		{[]string{"recovery", "enter", "--restored", "family=database,backup=pg,taken-at=2026-11-01T08:30:00Z", "--stopped-at", "2026-11-01T08:31:00Z", "--reason", "r"}, "enter\n", "Recovery mode entered", []string{"ACT", "NEWEPOCH", "PREV", "OP"}},
		{[]string{"recovery", "exit"}, "", "Recovery mode left", []string{"ACT", "NEWEPOCH", "SCOPE"}},
		{[]string{"identity", "revoke", "--identity", "svc_x", "--reason", "r"}, "svc_x\n", "Revoked", []string{"IDENT", "ACT", "NOTE"}},
	} {
		args := c.args
		var out, errb bytes.Buffer
		withStdin(t, c.typed, func() { run(args, &out, &errb) })
		if !strings.Contains(out.String(), c.shows) {
			t.Fatalf("%v: the answer was not shown:\n%s\n%s", args, out.String(), errb.String())
		}
		for _, s := range []string{out.String(), errb.String()} {
			for _, r := range "\x1b\t\u0085\u202e\x7f" {
				if strings.ContainsRune(s, r) {
					t.Errorf("%v: hostile %q reached the terminal raw:\n%q", args, r, s)
				}
			}
		}
		// Dropping a field would pass the checks above: each one shows, escaped as its own value.
		for _, m := range c.marks {
			want := client.Printable(mk(m))
			if m == "EPOCH" {
				want = client.Printable(epoch)
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v: %s is not shown as %q:\n%s", args, m, want, out.String())
			}
		}
		// The same command with -o json prints the answer, and no raw control character either.
		var jout, jerr bytes.Buffer
		withStdin(t, c.typed, func() { run(append([]string{"-o", "json"}, args...), &jout, &jerr) })
		if !strings.Contains(jout.String(), `"epoch"`) && !strings.Contains(jout.String(), `"act"`) {
			t.Fatalf("%v -o json: the answer was not shown:\n%s\n%s", args, jout.String(), jerr.String())
		}
		for _, s := range []string{jout.String(), jerr.String()} {
			for _, r := range "\x1b\t\u0085\u202e\x7f" {
				if strings.ContainsRune(s, r) {
					t.Errorf("%v -o json: hostile %q reached the terminal raw:\n%q", args, r, s)
				}
			}
		}
	}
}

// TestRecoveryStatusEscapesTheExitAndTheNextSteps: the exit row, the operation that holds an
// unresolved scope and the keys a blocked scope misses come from reads after the first one; each
// is shown escaped as its own value, a carriage return included.
func TestRecoveryStatusEscapesTheExitAndTheNextSteps(t *testing.T) {
	mk := func(n string) string { return n + "\x1b[2J\r\n\t\u0085\u202e\x7f" + n }
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	opID := "op_" + mk("OPID")
	recoveryServer(t, map[string]string{
		"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"exit":{"at":"2026-11-01T09:00:00Z","exitedBy":` + js(mk("EXITBY")) + `,"act":` + js(mk("EXITACT")) + `},` +
			`"scopes":[{"machine":"mch_b","state":"unresolved"},{"machine":"mch_c","state":"blocked"}]}`,
		"GET /api/v1/operations":                           `{"items":[{"id":` + js(opID) + `,"kind":"apply-config","state":"unresolved"}]}`,
		"GET /api/v1/operations/" + opID:                   `{"id":` + js(opID) + `,"kind":"apply-config","state":"unresolved"}`,
		"GET /api/v1/machines/mch_c/timeline":              `{"items":[{"kind":"scope-state","revision":9,"entry":{"to":"blocked","cause":"restoration-mark","missing":[` + js(mk("KEY")) + `]}}]}`,
		"GET /api/v1/machines/mch_c/timeline?order=newest": `{"items":[{"kind":"scope-state","revision":9,"entry":{"to":"blocked","cause":"restoration-mark","missing":[` + js(mk("KEY")) + `]}}]}`,
	}, "")
	var out, errb bytes.Buffer
	if code := run([]string{"recovery", "status"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	for _, s := range []string{out.String(), errb.String()} {
		for _, r := range "\x1b\r\t\u0085\u202e\x7f" {
			if strings.ContainsRune(s, r) {
				t.Errorf("hostile %q reached the terminal raw:\n%q", r, s)
			}
		}
	}
	for _, m := range []string{"EXITBY", "EXITACT", "KEY"} {
		if want := client.Printable(mk(m)); !strings.Contains(out.String(), want) {
			t.Errorf("%s is not shown as %q:\n%s", m, want, out.String())
		}
	}
	if want := client.Printable(opID); !strings.Contains(out.String(), "operation "+want+" holds it") {
		t.Errorf("the operation is not shown as %q:\n%s", want, out.String())
	}
}

// TestEntryUnknownOutcomePrintsNoEpoch: the entry's outcome-unknown block, read through the real
// read callback, shows no epoch anywhere (REC-04 (a)); the exit's block still shows it.
func TestEntryUnknownOutcomePrintsNoEpoch(t *testing.T) {
	recoveryServer(t, map[string]string{
		"GET /api/v1/recovery":          `{"epoch":"ep_BWSYNTH-9","recoveryMode":true,"scopes":[]}`,
		"POST /api/v1/recovery/entries": `302 {}`,
		"POST /api/v1/recovery/exits":   `302 {}`,
	}, "")
	var out, errb bytes.Buffer
	args := []string{"recovery", "enter", "--restored", "family=database,backup=pg,taken-at=2026-11-01T08:30:00Z", "--stopped-at", "2026-11-01T08:31:00Z", "--reason", "r"}
	var rc int
	withStdin(t, "enter\n", func() { rc = run(args, &out, &errb) })
	if rc != 3 || !strings.Contains(errb.String(), "GET /api/v1/recovery shows recovery mode true") {
		t.Fatalf("rc %d, want 3 with the read shown:\n%s", rc, errb.String())
	}
	if strings.Contains(errb.String(), "ep_BWSYNTH") {
		t.Errorf("the entry's block names an epoch:\n%s", errb.String())
	}
	errb.Reset()
	if rc = run([]string{"recovery", "exit"}, &out, &errb); rc != 3 || !strings.Contains(errb.String(), "shows epoch ep_BWSYNTH-9") {
		t.Errorf("rc %d, the exit's block should show the epoch:\n%s", rc, errb.String())
	}
}

// TestRefusedExitRepeatNamesTheScope: an operator's repeat of the exit, refused for an unreleased
// scope, still names that scope, and keeps the unknown-outcome exit code.
func TestRefusedExitRepeatNamesTheScope(t *testing.T) {
	recoveryServer(t, map[string]string{
		"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":true,"scopes":[]}`,
		"POST /api/v1/recovery/exits": `409 {"type":"urn:bronzeward:problem:conflict","title":"BWSYNTH","status":409,"instance":"req_BWSYNTH",` +
			`"machine":"mch_BWSYNTH","scope":"config"}`,
	}, "")
	var out, errb bytes.Buffer
	rc := run([]string{"recovery", "exit", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa", "--expect-epoch", "ep_BWSYNTH-1", "--expect-principal", "svc_BWSYNTH"}, &out, &errb)
	if rc != 3 || !strings.Contains(errb.String(), "\nThe first scope not released: machine 'mch_BWSYNTH', 'config'") {
		t.Errorf("rc %d, want 3 naming the scope on a line of its own:\n%s", rc, errb.String())
	}
	if strings.Contains(errb.String(), `\x0aThe first scope`) {
		t.Errorf("the scope line was flattened into the error line:\n%s", errb.String())
	}
}

// TestIdentityPromptIsEscaped: the identity the operator is asked to type back is shown escaped, a
// line feed included, so a hostile --identity cannot forge a prompt line; the comparison still
// uses the raw value (client-contracts.md section 6 item 8).
func TestIdentityPromptIsEscaped(t *testing.T) {
	recoveryServer(t, map[string]string{}, "")
	var out, errb bytes.Buffer
	withStdin(t, "\n", func() {
		run([]string{"identity", "revoke", "--identity", "svc_x\nFORGED: type yes", "--reason", "r"}, &out, &errb)
	})
	if strings.Contains(errb.String(), "\nFORGED") {
		t.Errorf("the prompt shows the identity raw:\n%q", errb.String())
	}
	if !strings.Contains(errb.String(), `Type the identity svc_x\x0aFORGED: type yes to send it`) {
		t.Errorf("the prompt lacks the escaped identity:\n%q", errb.String())
	}
}

// TestIdentityConfirmationComparesTheRawValue: for an identity with a tab, the prompt shows \x09,
// but only the raw spelling confirms; the shown spelling sends nothing.
func TestIdentityConfirmationComparesTheRawValue(t *testing.T) {
	args := []string{"identity", "revoke", "--identity", "svc_x\ty", "--reason", "r"}
	for _, c := range []struct {
		typed string
		sent  bool
	}{{"svc_x\ty\n", true}, {`svc_x\x09y` + "\n", false}} {
		seen := recoveryServer(t, nil, `{"act":"act_BWSYNTH","identity":"idn_BWSYNTH","deniedSubjectsListed":true}`)
		var out, errb bytes.Buffer
		withStdin(t, c.typed, func() { run(args, &out, &errb) })
		if got := slices.Contains(seen(), "POST /api/v1/identity-revocations"); got != c.sent {
			t.Errorf("typed %q: sent %t, want %t\n%s", c.typed, got, c.sent, errb.String())
		}
		if !strings.Contains(errb.String(), `Type the identity svc_x\x09y to send it`) {
			t.Errorf("typed %q: the prompt lacks the escaped tab:\n%q", c.typed, errb.String())
		}
	}
}

// TestMachineAddNamesTheClusterFromTheSameEpoch: machine add shows the cluster's name from a second
// read. When the epoch moved after the write's answer, the name is of another snapshot: the
// cluster ID is shown alone, and the write is still reported as done (client-contracts.md section 8.5).
func TestMachineAddNamesTheClusterFromTheSameEpoch(t *testing.T) {
	args := []string{"machine", "add", "--cluster", "cl_c", "--talos-node-id", "n", "--platform", "metal", "--talos-endpoint", "10.0.0.5:50000"}
	for _, c := range []struct {
		name  string
		moved map[string]string
		want  string
	}{
		{"the epoch holds", nil, "Machine mch_new recorded in cluster cl_c (lab-a). Endpoint 10.0.0.5:50000."},
		{"the epoch moves before the cluster read", map[string]string{"/api/v1/clusters/cl_c": "ep_BWSYNTH-2"}, "Machine mch_new recorded in cluster cl_c. Endpoint 10.0.0.5:50000."},
	} {
		setServerFacts(t, 3, c.moved)
		recoveryServer(t, map[string]string{
			"GET /api/v1/recovery":      `{"epoch":"ep_BWSYNTH-1","recoveryMode":false}`,
			"GET /api/v1/clusters/cl_c": `{"id":"cl_c","name":"lab-a"}`,
		}, `{"id":"mch_new","cluster":"cl_c","talosEndpoint":"10.0.0.5:50000"}`)
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		if code != 0 || !strings.Contains(out.String(), c.want+"\n") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 0 and %q", c.name, code, out.String(), errb.String(), c.want)
		}
	}
}

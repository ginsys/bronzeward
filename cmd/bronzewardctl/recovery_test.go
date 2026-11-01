package main

import (
	"bytes"
	"errors"
	"fmt"
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

// recoveryServer is a fake server for the recovery commands, answering reads from answers by
// method and path, and every POST 201 with post's body. It lists the requests it saw.
func recoveryServer(t *testing.T, answers map[string]string, post string) func() []string {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		w.Header().Set("Content-Type", "application/json")
		switch a, ok := answers[r.Method+" "+r.URL.Path]; {
		case r.URL.Path == "/api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"recovery"}`)
		case r.URL.Path == "/api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case ok:
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

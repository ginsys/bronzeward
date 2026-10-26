package clitest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"go.yaml.in/yaml/v3"
)

// record posts body to path as the fixture human h-all and returns the created ID.
func (f *fixture) record(path, key, body string) string {
	f.t.Helper()
	human, err := f.iss.Mint("h-all", "", time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	q, _ := http.NewRequest(http.MethodPost, f.base+"/api/v1"+path, strings.NewReader(body))
	q.Header.Set("Authorization", "Bearer "+human)
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", key)
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(f.do(q, http.StatusCreated), &v); err != nil {
		f.t.Fatalf("POST %s: %v", path, err)
	}
	return v.ID
}

// inventory seeds a cluster with three machines and returns them and the machine IDs in order.
func (f *fixture) inventory() (seeded, []string) {
	f.t.Helper()
	s := f.seed()
	ms := []string{s.machine}
	for i := range 2 {
		ms = append(ms, f.record("/machines", "k-reads-machine-"+string(rune('a'+i))+"-0123456",
			`{"cluster":"`+s.cluster+`","smbiosUuid":"`+string(rune('1'+i))+`b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b","platform":"container","talosEndpoint":"10.55.0.`+string(rune('4'+i))+`"}`))
	}
	slices.Sort(ms)
	return s, ms
}

// listed is the IDs of an {"items": [...]} answer, in order.
func listed(t *testing.T, out string) []string {
	t.Helper()
	var v struct{ Items []struct{ ID string } }
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("%v: %s", err, out)
	}
	var ids []string
	for _, x := range v.Items {
		ids = append(ids, x.ID)
	}
	return ids
}

// The pagination and recovery cases; TestSharedCases seeds the inventory they read.
func init() {
	all := func(t *testing.T, f *fixture) []string {
		r := f.ctl(nil, "", "--context", "trusted", "machine", "list", "-o", "json")
		if ids := listed(t, r.out); len(ids) >= 3 {
			return ids
		}
		t.Fatalf("want three machines or more: %s", r.out)
		return nil
	}
	sharedCases["PAGE-01"] = func(t *testing.T, f *fixture, in *injector) {
		ms := all(t, f)
		r := f.ctl(nil, "", "--context", "trusted", "machine", "list", "--limit", "1")
		f.want(r, 0, ms[0], "Next page: --cursor ")
		if strings.Contains(r.out, ms[1]) {
			t.Errorf("one page of one machine shows more:\n%s", r.out)
		}
		in.set(nil, nil)
		r = f.ctl(nil, "", "--context", "trusted", "machine", "list", "--limit", "1", "--all", "-o", "json")
		f.want(r, 0)
		if got := listed(t, r.out); !slices.Equal(got, ms) {
			t.Errorf("listed %v, want %v", got, ms)
		}
		if n := len(slices.DeleteFunc(in.requests(), func(q string) bool { return q != "GET /api/v1/machines" })); n != len(ms) {
			t.Errorf("%d page reads, want %d: %v", n, len(ms), in.requests())
		}
		f.want(f.ctl(nil, "", "--context", "trusted", "machine", "list", "--all", "--cursor", "x"), 2, "not both")
	}
	// A refused cursor in the same epoch also restarts the list, without the notice; it runs first,
	// while the context still holds the server's epoch.
	sharedCases["PAGE-02"] = func(t *testing.T, f *fixture, in *injector) {
		ms := all(t, f)
		for _, newEpoch := range []bool{false, true} {
			var refused, entered atomic.Bool
			in.set(func(w http.ResponseWriter, r *http.Request) bool {
				if !on("/machines")(r) || r.URL.Query().Get("cursor") == "" || refused.Swap(true) {
					return false
				}
				entered.Store(newEpoch)
				if newEpoch {
					w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-restored")
				}
				problem(w, http.StatusBadRequest, "cursor-invalid", "")
				return true
			}, func(r *http.Response) {
				if entered.Load() {
					r.Header.Set("Bronzeward-Epoch", "ep_BWSYNTH-restored")
				}
			})
			r := f.ctl(nil, "", "--context", "trusted", "machine", "list", "--limit", "1", "--all", "-o", "json")
			f.want(r, 0, "reading the list again from its start")
			if got := listed(t, r.out); !slices.Equal(got, ms) {
				t.Errorf("new epoch %v: listed %v, want %v", newEpoch, got, ms)
			}
			if notice := strings.Contains(r.err, "entered recovery mode or was restored"); notice != newEpoch {
				t.Errorf("new epoch %v: notice shown %v\n%s", newEpoch, notice, r.err)
			}
		}
		// A given cursor the server refuses is discarded too: the first page is shown.
		in.set(nil, nil)
		f.want(f.ctl(nil, "", "--context", "trusted", "machine", "list", "--limit", "1", "--cursor", "BWSYNTH-stale"), 0,
			"reading the list again from its start", ms[0], "Next page: --cursor ")
		// The new epoch's GET /server re-read refuses the server: the list fails, not restarts.
		var refused atomic.Bool
		in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if !on("/machines")(r) || r.URL.Query().Get("cursor") == "" || refused.Swap(true) {
				return false
			}
			w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-other")
			problem(w, http.StatusBadRequest, "cursor-invalid", "")
			return true
		}, func(r *http.Response) {
			if refused.Load() && on("/server")(r.Request) {
				revisionOne(r)
			}
		})
		r := f.ctl(nil, "", "--context", "trusted", "machine", "list", "--limit", "1", "--all", "-o", "json")
		f.want(r, 1, "API revision 1")
		if strings.Contains(r.err, "reading the list again") || strings.Contains(r.out, ms[0]) {
			t.Errorf("the list restarted after the refused server:\n%s%s", r.out, r.err)
		}
		in.set(nil, nil)
	}
	sharedCases["REC-01"] = func(t *testing.T, f *fixture, in *injector) {
		m := all(t, f)[0]
		in.set(nil, func(r *http.Response) {
			r.Header.Set("Bronzeward-Recovery-Mode", "true")
			if on("/recovery")(r.Request) {
				rewrite(r, `"recoveryMode":false`, `"recoveryMode":true`)
				rewrite(r, `{"machine":"`+m+`","state":"normal"}`, `{"machine":"`+m+`","state":"ready"}`)
			}
		})
		f.want(f.ctl(nil, "", "--context", "trusted", "status"), 0, "RECOVERY MODE is on in trusted", "Epoch ", "Scopes: 1: "+m+" ready")
		in.set(refuse("/machines/"+m, http.StatusConflict, "recovery-mode-active", `,"machine":"`+m+`","scope":"pre-restore-unaccounted"`), nil)
		f.want(f.ctl(nil, "", "--context", "trusted", "machine", "show", m), 1, "the scope of machine "+m+" is pre-restore-unaccounted")
		in.set(refuse("/machines/"+m, http.StatusConflict, "recovery-mode-active", ""), nil)
		f.want(f.ctl(nil, "", "--context", "trusted", "machine", "show", m), 1, "a process restriction of a server started for recovery")
	}
}

// TestReads covers context add --from and the OIDC fields, and the cluster, machine and status
// reads. The token may write, so the unchanged acts and the injector's GET-only record show the
// reads send no write.
func TestReads(t *testing.T) {
	f := start(t, "viewer,author,publisher")
	in := newInjector(t, f.base)
	s, machines := f.inventory()

	// The client configuration file names the issuer, audience and both client IDs; the CLI keeps
	// its own.
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/bronzeward-client.json" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"issuer":"https://idp.example.invalid","audience":"bronzeward","cliClientId":"BWSYNTH-cli","webClientId":"BWSYNTH-web"}`))
		return true
	}, nil)
	f.want(f.ctl(nil, "", "context", "add", "signin", "--from", in.URL, "--certificate-authority", in.ca), 0,
		`Context "signin" added: `+in.URL, "credential oidc")
	var cfg client.Config
	b, _ := os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
	if yaml.Unmarshal(b, &cfg); cfg.Find("signin") == nil || cfg.Find("signin").OIDC == nil ||
		!reflect.DeepEqual(*cfg.Find("signin").OIDC, client.OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli", Audience: "bronzeward"}) {
		t.Errorf("--from did not keep the file's issuer, audience and CLI client ID:\n%s", b)
	}
	// The optional scopes, as the test identity provider's file names its project audience scope,
	// are kept after the base scopes; a repeated base scope or a malformed member is refused.
	serve := func(scopes string) {
		in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/bronzeward-client.json" {
				return false
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"issuer":"https://idp.example.invalid","audience":"BWSYNTH-project","cliClientId":"BWSYNTH-cli","webClientId":"BWSYNTH-web","scopes":` + scopes + `}`))
			return true
		}, nil)
	}
	serve(`"urn:zitadel:iam:org:project:id:BWSYNTH-project:aud"`)
	f.want(f.ctl(nil, "", "context", "add", "zitadel", "--from", in.URL, "--certificate-authority", in.ca), 0, `Context "zitadel" added`)
	b, _ = os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
	cfg = client.Config{}
	if yaml.Unmarshal(b, &cfg); cfg.Find("zitadel") == nil || cfg.Find("zitadel").OIDC == nil ||
		!slices.Equal(cfg.Find("zitadel").OIDC.Scopes, []string{"openid", "offline_access", "urn:zitadel:iam:org:project:id:BWSYNTH-project:aud"}) {
		t.Errorf("--from did not keep the file's scopes after the base scopes:\n%s", b)
	}
	for scopes, want := range map[string]string{
		`"openid urn:zitadel:iam:org:project:id:BWSYNTH-project:aud"`: "scopes repeats openid, which is always requested",
		`"offline_access"`: "scopes repeats offline_access, which is always requested",
		`" "`:              "scopes is empty",
		`"a\"b"`:           "scopes holds a character a scope cannot",
		`7`:                "scopes is not a string",
		`null`:             "scopes is not a string",
	} {
		serve(scopes)
		f.want(f.ctl(nil, "", "context", "add", "badscopes", "--from", in.URL, "--certificate-authority", in.ca), 1, "bronzeward-client.json: "+want)
	}
	f.want(f.ctl(nil, "", "--context", "signin", "--no-input", "whoami"), 1, "not signed in", "--no-input forbids")
	f.want(f.ctl(nil, "", "context", "add", "both", "--from", in.URL, "--issuer", "https://idp.example.invalid"), 2, "not both")
	f.want(f.ctl(nil, "", "context", "add", "noclient", "--server", in.URL, "--issuer", "https://idp.example.invalid"), 2, "issuer and client ID")
	// An identity-provider flag is never dropped: alone it asks for the rest, and with another
	// credential it is refused.
	f.want(f.ctl(nil, "", "context", "add", "scopesonly", "--server", in.URL, "--scopes", "openid"), 2, "issuer and client ID")
	f.want(f.ctl(nil, "", "context", "add", "mixed", "--from", in.URL, "--credential", "automation"), 2, `need --credential oidc, not "automation"`)
	f.want(f.ctl(nil, "", "context", "add", "mixed", "--server", in.URL, "--credential", "automation", "--issuer", "https://idp.example.invalid"), 2, "need --credential oidc")
	f.want(f.ctl(nil, "", "context", "add", "plainidp", "--server", in.URL, "--issuer", "http://idp.example.invalid", "--client-id", "BWSYNTH-cli"), 2, "oidc.issuer", "must be https")
	// The configured server and issuer take no query, an empty one included; the provider's own
	// endpoints may (TestSignIn).
	for _, issuer := range []string{"https://idp.example.invalid/?x=1", "https://idp.example.invalid/?"} {
		f.want(f.ctl(nil, "", "context", "add", "queryidp", "--server", in.URL, "--issuer", issuer, "--client-id", "BWSYNTH-cli"), 2, "oidc.issuer", "no query")
	}
	f.want(f.ctl(nil, "", "context", "add", "queryserver", "--server", in.URL+"?x=1", "--automation-token-file", f.tokenFile), 2, "server", "no query")
	// Nor a fragment, an empty one included, which url.Parse drops.
	f.want(f.ctl(nil, "", "context", "add", "hashidp", "--server", in.URL, "--issuer", "https://idp.example.invalid/#", "--client-id", "BWSYNTH-cli"), 2, "oidc.issuer", "fragment")
	f.want(f.ctl(nil, "", "context", "add", "hashserver", "--server", in.URL+"/#", "--automation-token-file", f.tokenFile), 2, "server", "fragment")
	f.want(f.ctl(nil, "", "context", "add", "byhand", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", "https://idp.example.invalid", "--client-id", "BWSYNTH-cli", "--scopes", "openid,groups"), 0, "credential oidc")
	in.set(nil, nil)
	f.want(f.ctl(nil, "", "context", "add", "nofile", "--from", in.URL, "--certificate-authority", in.ca), 1,
		"bronzeward-client.json answered 401 Unauthorized, not the client configuration file")

	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", in.URL, "--certificate-authority", in.ca,
		"--automation-token-file", f.tokenFile), 0, "credential automation")
	f.want(f.ctl(nil, "", "context", "use", "lab"), 0)
	before := f.acts()

	f.want(f.ctl(nil, "", "cluster", "list"), 0, s.cluster, "lab-a", "https://cp.example.test:6443", "v1.13")
	f.want(f.ctl(nil, "", "cluster", "show", s.cluster), 0, s.cluster, "lab-a")
	f.want(f.ctl(nil, "", "cluster", "show", "cl_BWSYNTH-absent"), 1, "404")
	f.want(f.ctl(nil, "", "cluster"), 2, "cluster needs list or show")
	f.want(f.ctl(nil, "", "machine", "list", "--cluster", s.cluster), 0, machines[0], machines[2], "10.55.0.4:50000", "none", "Health is not checked")
	f.want(f.ctl(nil, "", "machine", "show", s.machine), 0,
		"Hardware", "node ID none", "SMBIOS UUID ", "Not applied or adopted by Bronzeward.", "No release selected.", "No configuration read recorded.", "Health: not checked.")
	// Structured output is the machine resource alone: its observations are not read.
	in.set(nil, nil)
	f.want(f.ctl(nil, "", "machine", "show", s.machine, "-o", "json"), 0, `"id":"`+s.machine+`"`)
	if slices.ContainsFunc(in.requests(), func(q string) bool { return strings.HasSuffix(q, "/observations") }) {
		t.Errorf("machine show -o json read the observations: %v", in.requests())
	}
	f.want(f.ctl(nil, "", "status"), 0, "Epoch                  ep_", "Recovery mode          off", "Open drift             none",
		"Unresolved operations  none", "Health                 not checked")
	r := f.ctl(nil, "", "status", "-o", "json")
	var st struct {
		Server, Recovery                      map[string]any
		DriftedMachines, UnresolvedOperations []any
	}
	if f.want(r, 0); json.Unmarshal([]byte(r.out), &st) != nil || st.Server["apiRevision"] == nil || st.Recovery["recoveryMode"] != false ||
		st.DriftedMachines == nil || st.UnresolvedOperations == nil {
		t.Errorf("status -o json: %s", r.out)
	}
	if after := f.acts(); after != before {
		t.Errorf("acts changed around the read commands:\nbefore %s\nafter  %s", before, after)
	}
}

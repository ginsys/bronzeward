package api

import (
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
)

const serverAuth = `listen: 127.0.0.1:8080
database: {dsn: postgres://bronzeward@127.0.0.1:55433/bronzeward}
auth:
  oidc:
    issuer: http://127.0.0.1:5556
    audience: bronzeward
`

// serverConfigs are the two configurations GET /server is checked under: (a) required values
// only, without a provider, so every listed setting is its default (the transport deadline
// default capped by the maximum); (b) every listed value set, with provider and ingestion. Each
// carries the members it must answer besides version, revision and start mode.
var serverConfigs = []struct {
	name, yaml string
	provider   bool
	want       map[string]any
}{
	{"defaults", serverAuth + `execution: {maxTransportDeadline: 30s}
`, false, map[string]any{
		"features": []any{}, "maxTokenLifetimeSeconds": 900.0, "settleFloorSeconds": 30.0,
		"maxTransportDeadlineSeconds": 30.0, "ingestionClaim": nil,
		"planDefaults": map[string]any{"expiresInSeconds": 86400.0, "maxObservationAgeSeconds": 300.0,
			"checkValiditySeconds": 300.0, "transportDeadlineSeconds": 30.0, "verificationDeadlineSeconds": 600.0, "maxAttempts": 3.0},
	}},
	{"every value set", strings.Replace(serverAuth, "audience: bronzeward\n", "audience: bronzeward\n    maxTokenLifetime: 20m\n", 1) +
		`execution:
  settleFloor: 45s
  maxTransportDeadline: 2m
  planDefaults: {expiry: 12h, maxObservationAge: 2m, checkValidity: 3m, transportDeadline: 90s, verificationDeadline: 15m, maxAttempts: 5}
provider:
  address: http://127.0.0.1:58201
  keys: {baseline: bw-baseline, staging: bw-staging, digest: bw-digest, artifact: bw-artifact}
  ingestionTokenFile: /etc/bronzeward/ingestion.token
  compilerTokenFile: /etc/bronzeward/compiler.token
  metadataTokenFile: /etc/bronzeward/metadata.token
  executorTokenFile: /etc/bronzeward/executor.token
ingestion: {instance: a, heartbeat: 500ms, lease: 1500ms, absoluteExpiry: 10m, sweep: 15s}
`, true, map[string]any{
		"features": []any{"execution", "ingestion", "publication"}, "maxTokenLifetimeSeconds": 1200.0, "settleFloorSeconds": 45.0,
		"maxTransportDeadlineSeconds": 120.0, "ingestionClaim": map[string]any{"leaseSeconds": 1.5, "absoluteExpirySeconds": 600.0},
		"planDefaults": map[string]any{"expiresInSeconds": 43200.0, "maxObservationAgeSeconds": 120.0,
			"checkValiditySeconds": 180.0, "transportDeadlineSeconds": 90.0, "verificationDeadlineSeconds": 900.0, "maxAttempts": 5.0},
	}},
}

// readServer is GET /server on a as tok, decoded; it fails unless the answer is a 200 carrying
// the epoch the read held and a non-empty version, which it removes (a test binary may report
// "(devel)").
func readServer(t *testing.T, e *env, a *API, tok string) map[string]any {
	t.Helper()
	rec := e.do(a, call{method: "GET", path: prefix + "/server", token: tok})
	b := decode[map[string]any](t, rec, http.StatusOK)
	if rec.Header().Get("Bronzeward-Epoch") != epoch(t, e.db) || rec.Header().Get("ETag") != "" {
		t.Fatalf("headers %v", rec.Header())
	}
	if v, _ := b["version"].(string); v == "" {
		t.Fatalf("version %v", b["version"])
	}
	delete(b, "version")
	return b
}

// persistence-api.md §9.2 GET /server (#278): every role and an automation token receive exactly
// the listed members, each equal to the loaded configuration, defaults included, and nothing is
// recorded.
func TestServerRead(t *testing.T) {
	t.Parallel()
	for _, c := range serverConfigs {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := config.Load(strings.NewReader(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			e := newEnv(t, options{})
			e.cfg.OIDC.MaxTokenLifetime = cfg.Auth.OIDC.MaxTokenLifetime
			d := deps{exec: cfg.Execution}
			if c.provider {
				d.timers, d.ing, d.pub, d.exe = *cfg.Ingestion, &fakeIngester{}, &publishClients{}, &fakeExecutor{}
			}
			a := e.buildWith(d, options{})
			want := map[string]any{"apiRevision": float64(apiRevision), "startMode": "normal"}
			for k, v := range c.want {
				want[k] = v
			}
			acts := count(t, e.db, "SELECT count(*) FROM act")
			for _, tok := range []string{e.human("h-viewer"), e.human("h-author"), e.human("h-publisher"), e.human("h-approver"),
				e.human("h-recovery"), e.human("h-all"), e.robot} {
				if got := readServer(t, e, a, tok); !reflect.DeepEqual(got, want) {
					t.Fatalf("answered %v; want %v", got, want)
				}
			}
			if n := count(t, e.db, "SELECT count(*) FROM act"); n != acts {
				t.Fatalf("%d acts after reads; %d before", n, acts)
			}
		})
	}
}

// §12.2: a recovery start refuses GET /server, as every request but the entry, until its entry
// commits; afterwards it answers startMode recovery to every role.
func TestServerRecoveryStart(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	var entered atomic.Int32
	r := recoveryStart(t, e, &entered)
	for _, tok := range []string{e.human("h-viewer"), e.human("h-recovery"), e.robot} {
		wantProblem(t, e.do(r, call{method: "GET", path: prefix + "/server", token: tok}), http.StatusConflict, "recovery-mode-active")
	}
	decode[entryBody](t, e.enter(r, "1", entryBodyText), http.StatusCreated)
	for _, h := range []string{"h-viewer", "h-author", "h-publisher", "h-approver", "h-recovery", "h-all"} {
		if b := readServer(t, e, r, e.human(h)); b["startMode"] != "recovery" {
			t.Fatalf("%s: startMode %v after the entry", h, b["startMode"])
		}
	}
	// The automation token was issued in the epoch the entry superseded (§10.2).
	wantProblem(t, e.do(r, call{method: "GET", path: prefix + "/server", token: e.robot}), http.StatusUnauthorized, "unauthenticated")
}

// GET /server's refusals are any read's: a query, an invalid token, no role, a denied subject, a
// revoked identity, an unreadable database.
func TestServerRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	p := prefix + "/server"
	wantProblem(t, e.do(e.api, call{method: "GET", path: p + "?x=1", token: e.human("h-viewer")}), http.StatusBadRequest, "invalid-request")
	wantProblem(t, e.do(e.api, call{method: "GET", path: p, token: "not-a-token"}), http.StatusUnauthorized, "unauthenticated")
	nobody, err := e.iss.Sign(map[string]any{"iss": e.iss.URL, "aud": "bronzeward", "sub": "h-nobody", "jti": issuer.JTI(),
		"iat": time.Now().Unix(), "nbf": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: p, token: nobody}), http.StatusForbidden, "forbidden")
	cfg := e.cfg
	cfg.DeniedSubjects = []config.DeniedSubject{{Iss: e.iss.URL, Sub: "h-viewer"}}
	denied := newAPI(e.db, auth.NewVerifier(cfg, e.db, auth.Discover(cfg.OIDC)), cfg, deps{}, options{logf: e.logf})
	wantProblem(t, e.do(denied, call{method: "GET", path: p, token: e.human("h-viewer")}), http.StatusForbidden, "identity-revoked")
	revocationOf(t, revoke(e, e.human("h-recovery"), "k-revoke-robot-0123456789", `{"identity":"`+e.robotID+`","reason":"left"}`))
	wantProblem(t, e.do(e.api, call{method: "GET", path: p, token: e.robot}), http.StatusForbidden, "identity-revoked")
	viewer := e.human("h-viewer")
	mustExec(t, e.db, "ALTER TABLE installation_state RENAME TO installation_state_away")
	t.Cleanup(func() { mustExec(t, e.db, "ALTER TABLE installation_state_away RENAME TO installation_state") })
	wantProblem(t, e.do(e.api, call{method: "GET", path: p, token: viewer}), http.StatusServiceUnavailable, "dependency-unavailable")
}

// §9.1: the served revision is the last row of the revision table.
func TestAPIRevisionIsSpecified(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/persistence-api.md")
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile("(?s)\nAPI revisions:\n\n\\| Revision \\| Adds \\|\n\\| --- \\| --- \\|\n((?:\\|[^\n]*\n)+)").FindSubmatch(spec)
	if table == nil {
		t.Fatal("persistence-api.md §9.1 has no revision table")
	}
	rows := regexp.MustCompile(`(?m)^\| ([0-9]+) \|`).FindAllSubmatch(table[1], -1)
	for i, r := range rows {
		if n, _ := strconv.Atoi(string(r[1])); n != i+1 {
			t.Fatalf("row %d names revision %s", i+1, r[1])
		}
	}
	if len(rows) != apiRevision {
		t.Fatalf("§9.1's table ends at revision %d; the server serves %d", len(rows), apiRevision)
	}
}

package config

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// authBlock is the smallest valid auth block. Every input below carries it, so that each
// refusal case fails for its own reason and not because auth is missing.
const authBlock = "auth:\n  oidc:\n    issuer: https://idp.example.test/realms/ops\n    audience: bronzeward\n"

func TestLoad(t *testing.T) {
	c, err := Load(strings.NewReader("listen: 127.0.0.1:8443\ndatabase:\n  dsn: postgres://bw@localhost/bw\n" + execBlock + authBlock))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8443" || c.Database.DSN != "postgres://bw@localhost/bw" {
		t.Fatalf("got %+v", c)
	}
	if c.Auth.OIDC.GroupsClaim != "groups" || c.Auth.OIDC.MaxTokenLifetime != 15*time.Minute {
		t.Fatalf("defaults not applied: %+v", c.Auth.OIDC)
	}
}

// Each input is valid but for its one defect, and the refusal must name that defect: an input
// that fails for another reason would pass with the check under test removed.
func TestLoadRefuses(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"unknown field": {"listen: :1\ndatabase: {dsn: x}\nlisten_addr: :2\n" + execBlock + authBlock, "field listen_addr not found"},
		"no listen":     {"database: {dsn: x}\n" + execBlock + authBlock, "listen is required"},
		"no dsn":        {"listen: :1\n" + execBlock + authBlock, "database.dsn is required"},
		"empty":         {"", "EOF"},
		"two documents": {"listen: :1\ndatabase: {dsn: x}\n" + execBlock + authBlock + "---\nlisten: :2\n", "more than one YAML document"},
	} {
		if _, err := Load(strings.NewReader(c.in)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error naming %q", name, err, c.want)
		}
	}
}

// execBlock is the smallest valid execution block (execution-recovery.md §5.2).
const execBlock = "execution: {maxTransportDeadline: 5m}\n"

const base = "listen: :1\ndatabase: {dsn: x}\n" + execBlock

func TestLoadAuth(t *testing.T) {
	c, err := Load(strings.NewReader(base + `auth:
  oidc:
    issuer: https://idp.example.test/realms/ops
    audience: bronzeward
    groupsClaim: roles
    maxTokenLifetime: 10m
  roles:
    viewer: [bw-viewers]
    recovery-admin: [bw-recovery]
  deniedSubjects:
    - {iss: https://idp.example.test/realms/ops, sub: alice}
    - {identity: idn_fgqvcvz3ck7h7234ljgdbzsj6m}
`))
	if err != nil {
		t.Fatal(err)
	}
	a := c.Auth
	if a.OIDC.Issuer != "https://idp.example.test/realms/ops" || a.OIDC.Audience != "bronzeward" ||
		a.OIDC.GroupsClaim != "roles" || a.OIDC.MaxTokenLifetime != 10*time.Minute {
		t.Fatalf("oidc: %+v", a.OIDC)
	}
	if !slices.Equal(a.Roles.Viewer, []string{"bw-viewers"}) || !slices.Equal(a.Roles.RecoveryAdmin, []string{"bw-recovery"}) || a.Roles.Author != nil {
		t.Fatalf("roles: %+v", a.Roles)
	}
	want := []DeniedSubject{{Iss: "https://idp.example.test/realms/ops", Sub: "alice"}, {Identity: "idn_fgqvcvz3ck7h7234ljgdbzsj6m"}}
	if !slices.Equal(a.DeniedSubjects, want) {
		t.Fatalf("deniedSubjects: %+v", a.DeniedSubjects)
	}
}

// Plain http reaches only this host: the fixture issuer. Anything else would let an on-path
// attacker serve the discovery document and signing key.
func TestLoadAcceptsLoopbackHTTPIssuer(t *testing.T) {
	for _, iss := range []string{"http://127.0.0.1:5556", "http://[::1]:5556", "http://localhost:5556"} {
		if _, err := Load(strings.NewReader(base + "auth:\n  oidc:\n    issuer: " + iss + "\n    audience: bronzeward\n")); err != nil {
			t.Errorf("%s: %v", iss, err)
		}
	}
}

func TestLoadRefusesAuth(t *testing.T) {
	oidc := func(extra string) string {
		return base + "auth:\n  oidc:\n    issuer: https://idp.example.test\n    audience: bronzeward\n" + extra
	}
	for name, in := range map[string]string{
		"no auth":            base,
		"no issuer":          base + "auth:\n  oidc:\n    audience: bronzeward\n",
		"relative issuer":    base + "auth:\n  oidc:\n    issuer: idp.example.test\n    audience: bronzeward\n",
		"issuer with query":  base + "auth:\n  oidc:\n    issuer: https://idp.example.test/?x=1\n    audience: bronzeward\n",
		"plain http issuer":  base + "auth:\n  oidc:\n    issuer: http://idp.example.test\n    audience: bronzeward\n",
		"http, loopback-ish": base + "auth:\n  oidc:\n    issuer: http://127.0.0.1.example.test\n    audience: bronzeward\n",
		"no audience":        base + "auth:\n  oidc:\n    issuer: https://idp.example.test\n",
		"unknown oidc field": oidc("    clientSecret: x\n"),
		"integer lifetime":   oidc("    maxTokenLifetime: 900\n"),
		"negative lifetime":  oidc("    maxTokenLifetime: -5m\n"),
		"unknown role":       oidc("  roles:\n    admin: [x]\n"),
		"both forms":         oidc("  deniedSubjects:\n    - {iss: https://idp.example.test, sub: a, identity: idn_fgqvcvz3ck7h7234ljgdbzsj6m}\n"),
		"iss without sub":    oidc("  deniedSubjects:\n    - {iss: https://idp.example.test}\n"),
		"empty entry":        oidc("  deniedSubjects:\n    - {}\n"),
		"not an idn":         oidc("  deniedSubjects:\n    - {identity: tok_fgqvcvz3ck7h7234ljgdbzsj6m}\n"),
	} {
		if _, err := Load(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// plainHTTPHosts is the admin's list of hosts reached over plain http on a network the deployment
// protects (persistence-api.md §10.1): exactly those, by name, and nothing a listed name merely
// begins or ends with.
func TestPlainHTTPHosts(t *testing.T) {
	c, err := Load(strings.NewReader(base + "auth:\n  oidc:\n    issuer: http://dex.auth.svc:5556\n    plainHTTPHosts: [dex.auth.svc]\n    audience: bronzeward\n"))
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]bool{
		"http://dex.auth.svc:5556/keys":   true,
		"http://DEX.auth.svc/keys":        true,
		"https://anything.example/keys":   true,
		"http://127.0.0.1:9/keys":         true,
		"http://dex.auth.svc.evil/keys":   false,
		"http://x.dex.auth.svc/keys":      false,
		"http://other.auth.svc:5556/keys": false,
		"ftp://dex.auth.svc/keys":         false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Auth.OIDC.Transport(u); got != want {
			t.Errorf("%s: %v, want %v", raw, got, want)
		}
	}
}

func TestPlainHTTPHostsRefusals(t *testing.T) {
	for name, hosts := range map[string]string{
		"empty entry":                  `[""]`,
		"with scheme":                  `["http://dex"]`,
		"with port":                    `["dex:5556"]`,
		"with path":                    `["dex/x"]`,
		"with space":                   `["dex auth"]`,
		"empty label":                  `["dex..svc"]`,
		"label starting with a hyphen": `["dex.-svc"]`,
		"label ending with a hyphen":   `["dex-.svc"]`,
		"label over 63 characters":     `["` + strings.Repeat("a", 64) + `.svc"]`,
		"name over 253 characters":     `["` + longName(254) + `"]`,
	} {
		in := base + "auth:\n  oidc:\n    issuer: http://127.0.0.1:5556\n    plainHTTPHosts: " + hosts + "\n    audience: bronzeward\n"
		if _, err := Load(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A name of exactly 253 characters is a DNS name (control for the length refusal).
	in := base + "auth:\n  oidc:\n    issuer: http://127.0.0.1:5556\n    plainHTTPHosts: [" + longName(253) + "]\n    audience: bronzeward\n"
	if _, err := Load(strings.NewReader(in)); err != nil {
		t.Errorf("a 253-character name: %v", err)
	}
	// Unlisted, the PR 3 refusal stands.
	in = base + "auth:\n  oidc:\n    issuer: http://dex.auth.svc:5556\n    plainHTTPHosts: [other.auth.svc]\n    audience: bronzeward\n"
	if _, err := Load(strings.NewReader(in)); err == nil {
		t.Fatal("an unlisted plain-http issuer was accepted")
	}
}

// longName is a host name of n characters made of valid labels of at most 63 characters.
func longName(n int) string {
	var labels []string
	for n > 0 {
		l := min(63, n)
		if n-l == 1 { // a remainder of one character leaves no room for its dot
			l--
		}
		labels = append(labels, strings.Repeat("a", l))
		n -= l + 1
	}
	return strings.Join(labels, ".")
}

// execution-recovery.md §5.2: the settle floor defaults to, and may not go under, 30s; the
// maximum transport deadline has no default.
func TestExecution(t *testing.T) {
	c, err := Load(strings.NewReader(base + authBlock))
	if err != nil {
		t.Fatal(err)
	}
	if c.Execution.SettleFloor != 30*time.Second || c.Execution.MaxTransportDeadline != 5*time.Minute {
		t.Fatalf("%+v", c.Execution)
	}
	noExec := strings.Replace(base, execBlock, "", 1)
	for name, in := range map[string]string{
		"settle floor under 30s": noExec + "execution: {settleFloor: 29s, maxTransportDeadline: 5m}\n" + authBlock,
		"deadline negative":      noExec + "execution: {maxTransportDeadline: -1s}\n" + authBlock,
		"deadline missing":       noExec + "execution: {settleFloor: 30s}\n" + authBlock,
		"no execution block":     noExec + authBlock,
	} {
		if _, err := Load(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// providerBlock is the smallest valid provider block; each refusal below changes one thing.
const providerBlock = `provider:
  address: https://bao.example.test:8200
  keys: {baseline: bw-baseline, staging: bw-staging, digest: bw-digest}
  ingestionTokenFile: /etc/bronzeward/openbao-ingestion.token
`

func TestProviderBlock(t *testing.T) {
	c, err := Load(strings.NewReader(base + authBlock))
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != nil {
		t.Fatalf("an absent block is not nil: %+v", c.Provider)
	}
	c, err = Load(strings.NewReader(base + authBlock + providerBlock + ingestionBlock))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Provider
	if p == nil || p.Address != "https://bao.example.test:8200" || p.Keys.Baseline != "bw-baseline" ||
		p.Keys.Staging != "bw-staging" || p.Keys.Digest != "bw-digest" || p.IngestionTokenFile != "/etc/bronzeward/openbao-ingestion.token" {
		t.Fatalf("%+v", p)
	}
	for name, in := range map[string]string{
		"loopback http":  strings.Replace(providerBlock, "https://bao.example.test:8200", "http://127.0.0.1:58200", 1),
		"listed http":    strings.Replace(providerBlock, "https://bao.example.test:8200", "http://openbao:8200", 1) + "  plainHTTPHosts: [openbao]\n",
		"trailing slash": strings.Replace(providerBlock, "8200", "8200/", 1),
	} {
		if _, err := Load(strings.NewReader(base + authBlock + in + ingestionBlock)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, c := range map[string]struct{ block, want string }{
		"no address":        {strings.Replace(providerBlock, "  address: https://bao.example.test:8200\n", "", 1), "provider.address is required"},
		"unlisted http":     {strings.Replace(providerBlock, "https://bao.example.test:8200", "http://openbao:8200", 1), "neither loopback nor in provider.plainHTTPHosts"},
		"other host listed": {strings.Replace(providerBlock, "https://bao.example.test:8200", "http://openbao:8200", 1) + "  plainHTTPHosts: [issuer]\n", "neither loopback nor in provider.plainHTTPHosts"},
		"bad listed host":   {providerBlock + "  plainHTTPHosts: [\"open bao\"]\n", "not a bare host name"},
		"no scheme":         {strings.Replace(providerBlock, "https://", "", 1), "provider.address"},
		"path":              {strings.Replace(providerBlock, ":8200", ":8200/v1", 1), "provider.address"},
		"query":             {strings.Replace(providerBlock, ":8200", ":8200?x=1", 1), "provider.address"},
		"userinfo":          {strings.Replace(providerBlock, "https://", "https://root:hunter2@", 1), "userinfo"},
		"no baseline key":   {strings.Replace(providerBlock, "baseline: bw-baseline, ", "", 1), "provider.keys.baseline is required"},
		"no staging key":    {strings.Replace(providerBlock, "staging: bw-staging, ", "", 1), "provider.keys.staging is required"},
		"no digest key":     {strings.Replace(providerBlock, ", digest: bw-digest", "", 1), "provider.keys.digest is required"},
		"key with slash":    {strings.Replace(providerBlock, "bw-digest", "a/b", 1), "provider.keys.digest"},
		"key dot-dot":       {strings.Replace(providerBlock, "bw-digest", `".."`, 1), "provider.keys.digest"},
		"key with percent":  {strings.Replace(providerBlock, "bw-digest", `"a%2F"`, 1), "provider.keys.digest"},
		"key too long":      {strings.Replace(providerBlock, "bw-digest", strings.Repeat("k", 228), 1), "provider.keys.digest"},
		"shared key":        {strings.Replace(providerBlock, "bw-digest", "bw-staging", 1), "distinct"},
		"no token file":     {strings.Replace(providerBlock, "  ingestionTokenFile: /etc/bronzeward/openbao-ingestion.token\n", "", 1), "provider.ingestionTokenFile is required"},
		"unknown field":     {providerBlock + "  token: x\n", "field token not found"},
		"unknown key":       {strings.Replace(providerBlock, "digest: bw-digest", "digest: bw-digest, artifact: bw-artifact", 1), "field artifact not found"},
	} {
		_, err := Load(strings.NewReader(base + authBlock + c.block))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error naming %q", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error quotes the password: %v", name, err)
		}
	}
}

// ingestionBlock is the smallest valid ingestion block: the fixture's timers (compilation.md §3.2
// leaves the values open and requires only heartbeat < lease < absolute expiry).
const ingestionBlock = `ingestion:
  instance: a
  heartbeat: 5s
  lease: 15s
  absoluteExpiry: 10m
  sweep: 15s
`

func TestIngestionBlock(t *testing.T) {
	c, err := Load(strings.NewReader(base + authBlock + providerBlock + ingestionBlock))
	if err != nil {
		t.Fatal(err)
	}
	i := c.Ingestion
	if i == nil || i.Instance != "a" || i.Heartbeat != 5*time.Second || i.Lease != 15*time.Second ||
		i.AbsoluteExpiry != 10*time.Minute || i.Sweep != 15*time.Second {
		t.Fatalf("%+v", i)
	}
	for name, c := range map[string]struct{ in, want string }{
		"provider without ingestion": {base + authBlock + providerBlock, "ingestion is required with provider"},
		"ingestion without provider": {base + authBlock + ingestionBlock, "ingestion needs provider"},
		"heartbeat equals lease":     {strings.Replace(ingestionBlock, "heartbeat: 5s", "heartbeat: 15s", 1), "heartbeat must be shorter than ingestion.lease"},
		"lease equals expiry":        {strings.Replace(ingestionBlock, "lease: 15s", "lease: 10m", 1), "lease must be shorter than ingestion.absoluteExpiry"},
		"no heartbeat":               {strings.Replace(ingestionBlock, "  heartbeat: 5s\n", "", 1), "ingestion.heartbeat must be positive"},
		"negative heartbeat":         {strings.Replace(ingestionBlock, "heartbeat: 5s", "heartbeat: -1s", 1), "ingestion.heartbeat must be positive"},
		"no sweep":                   {strings.Replace(ingestionBlock, "  sweep: 15s\n", "", 1), "ingestion.sweep must be positive"},
		// The database keeps microseconds: 1500ns and 1900ns would both store as 1µs.
		"sub-microsecond lease":  {strings.Replace(strings.Replace(ingestionBlock, "heartbeat: 5s", "heartbeat: 1us", 1), "lease: 15s", "lease: 1500ns", 1), "ingestion.lease must be a whole number of microseconds"},
		"sub-microsecond expiry": {strings.Replace(ingestionBlock, "absoluteExpiry: 10m", "absoluteExpiry: 10m500ns", 1), "ingestion.absoluteExpiry must be a whole number of microseconds"},
		"no instance":            {strings.Replace(ingestionBlock, "  instance: a\n", "", 1), "ingestion.instance"},
		"instance with slash":    {strings.Replace(ingestionBlock, "instance: a", "instance: a/b", 1), "ingestion.instance"},
		"instance upper case":    {strings.Replace(ingestionBlock, "instance: a", "instance: A", 1), "ingestion.instance"},
	} {
		in := c.in
		if strings.HasPrefix(in, "ingestion:") {
			in = base + authBlock + providerBlock + in
		}
		if _, err := Load(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error naming %q", name, err, c.want)
		}
	}
}

// TestNoTalosBlock: the Talos credential is per cluster and read from the provider at use
// (persistence-api §3.3), so a deployment-wide talosconfig file is refused, not ignored.
func TestNoTalosBlock(t *testing.T) {
	if _, err := Load(strings.NewReader(base + authBlock + "talos: {talosconfig: /etc/bronzeward/talosconfig}\n")); err == nil || !strings.Contains(err.Error(), "field talos not found") {
		t.Fatalf("a talos block: %v", err)
	}
}

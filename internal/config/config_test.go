package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// authBlock is the smallest valid auth block. Every input below carries it, so that each
// refusal case fails for its own reason and not because auth is missing.
const authBlock = "auth:\n  oidc:\n    issuer: https://idp.example.test/realms/ops\n    audience: bronzeward\n"

func TestLoad(t *testing.T) {
	c, err := Load(strings.NewReader("listen: 127.0.0.1:8443\ndatabase:\n  dsn: postgres://bw@localhost/bw\n" + authBlock))
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

func TestLoadRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"unknown field": "listen: :1\ndatabase: {dsn: x}\nlisten_addr: :2\n" + authBlock,
		"no listen":     "database: {dsn: x}\n" + authBlock,
		"no dsn":        "listen: :1\n" + authBlock,
		"empty":         "",
		"two documents": "listen: :1\ndatabase: {dsn: x}\n" + authBlock + "---\nlisten: :2\n",
	} {
		if _, err := Load(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

const base = "listen: :1\ndatabase: {dsn: x}\n"

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

func TestLoadRefusesAuth(t *testing.T) {
	oidc := func(extra string) string {
		return base + "auth:\n  oidc:\n    issuer: https://idp.example.test\n    audience: bronzeward\n" + extra
	}
	for name, in := range map[string]string{
		"no auth":            base,
		"no issuer":          base + "auth:\n  oidc:\n    audience: bronzeward\n",
		"relative issuer":    base + "auth:\n  oidc:\n    issuer: idp.example.test\n    audience: bronzeward\n",
		"issuer with query":  base + "auth:\n  oidc:\n    issuer: https://idp.example.test/?x=1\n    audience: bronzeward\n",
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

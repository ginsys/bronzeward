package client

import (
	"strings"
	"testing"
)

// TestCheckOIDCNeedsScopesAndAudience pins client-contracts.md §5's table: oidc.scopes and
// oidc.audience are required with oidc. The refusal names every field the context lacks and what to
// add, and an automation context needs neither.
func TestCheckOIDCNeedsScopesAndAudience(t *testing.T) {
	oidc := func(scopes []string, audience string) *Context {
		return &Context{Name: "lab", Server: "https://bronzeward.example.test", Credential: "oidc",
			OIDC: &OIDC{Issuer: "https://idp.example.test", ClientID: "BWSYNTH-cli", Scopes: scopes, Audience: audience}}
	}
	both := []string{"openid", "offline_access"}
	for name, c := range map[string]struct {
		x    *Context
		want []string // every part must appear; none when the context is accepted
	}{
		"complete":     {oidc(both, "https://bronzeward.example.test"), nil},
		"no scopes":    {oidc(nil, "https://bronzeward.example.test"), []string{"oidc.scopes is empty", "scopes: [openid, offline_access]"}},
		"empty scopes": {oidc([]string{}, "https://bronzeward.example.test"), []string{"oidc.scopes is empty"}},
		"no audience":  {oidc(both, ""), []string{"oidc.audience is empty", "auth.oidc.audience"}},
		"neither":      {oidc(nil, ""), []string{"oidc.scopes is empty", "oidc.audience is empty"}},
		"automation":   {&Context{Name: "ci", Server: "https://bronzeward.example.test", Credential: "automation"}, nil},
		"no client ID": {&Context{Name: "lab", Server: "https://bronzeward.example.test", Credential: "oidc", OIDC: &OIDC{Issuer: "https://idp.example.test"}}, []string{"issuer and client ID"}},
		"bad issuer":   {&Context{Name: "lab", Server: "https://bronzeward.example.test", Credential: "oidc", OIDC: &OIDC{Issuer: "http://idp.example.test", ClientID: "BWSYNTH-cli"}}, []string{"oidc.issuer", "must be https"}},
	} {
		err := c.x.Check()
		if c.want == nil {
			if err != nil {
				t.Errorf("%s: refused: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q does not say %q", name, err, w)
			}
		}
	}
}

// TestOpenRefusesAnOIDCContextWithoutScopesOrAudienceBeforeAnyRequest: a context file edited by hand,
// or written before the rule, loads, and the first command that opens a client meets the refusal, not
// a request.
func TestOpenRefusesAnOIDCContextWithoutScopesOrAudienceBeforeAnyRequest(t *testing.T) {
	x := &Context{Name: "lab", Server: "https://bronzeward.example.test", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.test", ClientID: "BWSYNTH-cli"}}
	_, err := Open(x, TokenInput{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "oidc.scopes is empty") || !strings.Contains(err.Error(), "oidc.audience is empty") {
		t.Fatalf("Open = %v, want the refusal naming both fields", err)
	}
}

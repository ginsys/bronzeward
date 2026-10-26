package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// ClientConfig is the static client-configuration file bronzeward-client.json at the web root
// (client-contracts.md §5 item 4, decision D3 (b)).
type ClientConfig struct {
	Issuer      string `json:"issuer"`
	Audience    string `json:"audience"`
	CLIClientID string `json:"cliClientId"`
	WebClientID string `json:"webClientId"`
	// Scopes is the optional member: space-separated scopes requested after openid offline_access.
	Scopes *string `json:"scopes"`
}

// baseScopes are always requested, so the client configuration file does not repeat them.
var baseScopes = []string{"openid", "offline_access"}

// OIDCScopes is the context's oidc.scopes for the file: none, so the defaults, when it names no
// scopes, else openid offline_access followed by its scopes.
func (cc *ClientConfig) OIDCScopes() []string {
	if cc.Scopes == nil {
		return nil
	}
	return append(slices.Clone(baseScopes), splitScopes(*cc.Scopes)...)
}

func splitScopes(s string) []string {
	return slices.DeleteFunc(strings.Split(s, " "), func(s string) bool { return s == "" })
}

// checkScopes is why the scopes member is malformed, "" if it is not. As the web client reads it,
// each scope is an RFC 6749 scope-token and neither base scope is repeated.
func checkScopes(s string) string {
	scopes := splitScopes(s)
	if len(scopes) == 0 {
		return "scopes is empty"
	}
	for _, s := range scopes {
		if slices.Contains(baseScopes, s) {
			return "scopes repeats " + s + ", which is always requested"
		}
		if strings.ContainsFunc(s, func(r rune) bool { return r < 0x21 || r == 0x22 || r == 0x5c || r > 0x7e }) {
			return "scopes holds a character a scope cannot"
		}
	}
	return ""
}

// FetchClientConfig reads bronzeward-client.json at the web root of from, with x's TLS trust
// (§6), sending no credential and following no redirect.
func FetchClientConfig(x *Context, from string, verbose io.Writer) (*ClientConfig, error) {
	if err := endpoint(from, "--from"); err != nil {
		return nil, err
	}
	hc, err := httpClient(x, verbose)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(from)
	u.Path, u.RawPath = "/bronzeward-client.json", ""
	r, err := hc.Get(u.String())
	if err != nil {
		return nil, transportError(x, u.Host, err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s answered %s, not the client configuration file; bronzewardctl follows no redirect", u, r.Status)
	}
	var cc ClientConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&cc); err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	if cc.Issuer == "" || cc.CLIClientID == "" {
		return nil, fmt.Errorf("GET %s: the file names no issuer or no cliClientId", u)
	}
	if cc.Scopes != nil {
		if why := checkScopes(*cc.Scopes); why != "" {
			return nil, fmt.Errorf("GET %s: %s", u, why)
		}
	}
	return &cc, nil
}

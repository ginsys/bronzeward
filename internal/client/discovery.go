package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ClientConfig is the static client-configuration file bronzeward-client.json at the web root
// (client-contracts.md §5 item 4, decision D3 (b)).
type ClientConfig struct {
	Issuer      string `json:"issuer"`
	Audience    string `json:"audience"`
	CLIClientID string `json:"cliClientId"`
	WebClientID string `json:"webClientId"`
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
		return nil, fmt.Errorf("GET %s answered %s, not the client configuration file; bronzewardctl follows no redirect", u, printable(r.Status))
	}
	var cc ClientConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&cc); err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	if cc.Issuer == "" || cc.CLIClientID == "" {
		return nil, fmt.Errorf("GET %s: the file names no issuer or no cliClientId", u)
	}
	return &cc, nil
}

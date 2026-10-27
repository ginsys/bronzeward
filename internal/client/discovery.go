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
	// Nothing the host sent is shown but its status code (remote.go); u is the URL bronzewardctl formed.
	r, err := hc.Get(u.String())
	if err != nil {
		if t := refusedTrust(x, u.Host, err); t != nil {
			return nil, t
		}
		return nil, fmt.Errorf("GET %s: %w", u, remoteFailure(err))
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s answered %d %s, not the client configuration file; bronzewardctl follows no redirect", u, r.StatusCode, http.StatusText(r.StatusCode))
	}
	var cc ClientConfig
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&cc); err != nil {
		return nil, fmt.Errorf("GET %s: its answer could not be read as a client configuration file", u)
	}
	if cc.Issuer == "" || cc.CLIClientID == "" {
		return nil, fmt.Errorf("GET %s: the file names no issuer or no cliClientId", u)
	}
	// The issuer is the host's text until it is checked: Context.Check would quote it whole.
	if err := providerEndpoint(cc.Issuer, fmt.Sprintf("GET %s: the file's issuer", u)); err != nil {
		return nil, err
	}
	if v, _ := url.Parse(cc.Issuer); v.RawQuery != "" || v.ForceQuery {
		return nil, fmt.Errorf("GET %s: the file's issuer %s://%s: give a URL with no query", u, printable(v.Scheme), printable(v.Host))
	}
	return &cc, nil
}

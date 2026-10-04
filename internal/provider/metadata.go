package provider

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/ginsys/bronzeward/internal/classify"
)

// Metadata is the metadata identity's provider client (dependency-monitor.md §4): one GET by name
// of a KV generation's metadata or a Transit key's, returned as the provider answered for the
// classifier (§3), which alone decides what a status means. It has no LIST, no data read and no
// Transit operation (TestMetadataMethodSet).
type Metadata struct{ c *client }

// NewMetadata builds the client without contacting the provider.
func NewMetadata(addr string, tok Token) (*Metadata, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	return &Metadata{c: c}, nil
}

// KV asks for secret/metadata/<p>. The error is only a path NewGenerationPath did not make; no
// request is then sent.
func (m *Metadata) KV(ctx context.Context, p GenerationPath) (classify.Answer, error) {
	if p.zero() {
		return classify.Answer{}, errors.New("provider: a generation path made by NewGenerationPath is required")
	}
	return m.c.get(ctx, "/v1/secret/metadata/"+p.String()), nil
}

// Transit asks for transit/keys/<key>. The error is only a key name transitPath refuses; no
// request is then sent.
func (m *Metadata) Transit(ctx context.Context, key string) (classify.Answer, error) {
	path, err := transitPath("keys", key)
	if err != nil {
		return classify.Answer{}, err
	}
	return m.c.get(ctx, path), nil
}

// get sends one GET and returns the answer unmapped: its status, Date header and body. No answer
// (a transport failure, a timeout, a cancelled context, or a body that could not be read in
// full) is Unreachable. A body over maxResponse is dropped, which the classifier finds
// unreadable. A redirect is not followed (newClient).
func (c *client) get(ctx context.Context, path string) classify.Answer {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return classify.Answer{Unreachable: true}
	}
	req.Header.Set("X-Vault-Token", c.token.value())
	resp, err := c.http.Do(req)
	if err != nil {
		return classify.Answer{Unreachable: true}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return classify.Answer{Unreachable: true}
	}
	if len(body) > maxResponse {
		body = nil
	}
	return classify.Answer{Status: resp.StatusCode, Date: resp.Header.Get("Date"), Body: classify.NewBody(body)}
}

package provider

import (
	"context"

	"github.com/ginsys/bronzeward/internal/classify"
)

// Presence is the access-presence identity's provider client (persistence-api.md §3.3 "Presence"):
// one GET of the metadata of a cluster's Talos CA key, secret/metadata/access/talos-ca/<cluster>,
// returned as the provider answered for the classifier, which alone decides what a status means.
// It never reads the key's data: its method is the whole method set (TestPresenceMethodSet), with
// no data read, no list, no write and no Transit operation.
type Presence struct{ c *client }

// NewPresence builds the client without contacting the provider.
func NewPresence(addr string, tok Token) (*Presence, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	return &Presence{c: c}, nil
}

// CAKeyMetadata asks for the metadata of the CA key of one cluster. The error is only a cluster
// that is not a cl_ identifier; no request is then sent.
func (p *Presence) CAKeyMetadata(ctx context.Context, cluster string) (classify.Answer, error) {
	path, err := CAKeyPath(cluster)
	if err != nil {
		return classify.Answer{}, err
	}
	return p.c.get(ctx, "/v1/secret/metadata/"+path), nil
}

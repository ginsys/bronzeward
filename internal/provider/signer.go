package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// CAKeyPath is the KV path of a cluster's Talos os CA key, access/talos-ca/<cluster>
// (persistence-api.md §3.3). It is derived from a cl_ identifier only, so no caller can point the
// signer at another secret.
func CAKeyPath(cluster string) (string, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return "", fmt.Errorf("provider: ca key path cluster: %w", err)
	}
	return "access/talos-ca/" + cluster, nil
}

// CAKeyVersion identifies one version of a cluster's CA key: its path, its KV version and that
// version's created_time (persistence-api.md §3.3). It holds no secret and renders.
type CAKeyVersion struct {
	Path        string
	Version     int
	CreatedTime time.Time
}

// CAKey is a cluster's Talos os CA certificate and key, with the version it was read at or written
// as (zero before it is stored). It does not render: every fmt verb prints a placeholder and the
// marshallers fail. The PEM texts sit behind pointers to strings for the reason TalosAccess's does.
// Key is the one way out of the secret.
type CAKey struct {
	v        CAKeyVersion
	crt, key *string
}

// NewCAKey is a CAKey holding copies of a CA certificate and key, both PEM.
func NewCAKey(crt, key []byte) CAKey {
	c, k := string(crt), string(key)
	return CAKey{crt: &c, key: &k}
}

// Version is the identity of the version read; zero for a key not yet stored.
func (k CAKey) Version() CAKeyVersion { return k.v }

// Crt is a copy of the CA certificate PEM, which is public.
func (k CAKey) Crt() []byte {
	if k.crt == nil {
		return nil
	}
	return []byte(*k.crt)
}

// Key is a copy of the CA key PEM.
func (k CAKey) Key() []byte {
	if k.key == nil {
		return nil
	}
	return []byte(*k.key)
}

const caKeyText = "[talos ca key]"

var errCAKeyRender = errors.New("provider: a talos ca key is not marshalled")

func (CAKey) Format(f fmt.State, _ rune)   { io.WriteString(f, caKeyText) }
func (CAKey) MarshalJSON() ([]byte, error) { return nil, errCAKeyRender }
func (CAKey) MarshalText() ([]byte, error) { return nil, errCAKeyRender }
func (CAKey) MarshalYAML() (any, error)    { return nil, errCAKeyRender }

// Signer is the signer identity's provider client (persistence-api.md §3.4): `read`, `create` and
// `update` on secret/data/access/talos-ca/* and nothing else. Its two methods are the whole method
// set (TestSignerMethodSet): no list, no delete, no metadata read, no Transit.
type Signer struct{ c *client }

// NewSigner builds the client without contacting the provider.
func NewSigner(addr string, tok Token) (*Signer, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	return &Signer{c: c}, nil
}

// ReadCAKey reads the latest version of cluster's CA key: data exactly {"ca", "key"}. No version is
// ErrAbsent; a policy refusal is ErrDenied; an answer that is not one live version holding both
// members as non-empty strings is ErrProtocol.
func (s *Signer) ReadCAKey(ctx context.Context, cluster string) (CAKey, error) {
	return s.readCAKey(ctx, cluster, 0)
}

// ReadCAKeyVersion reads the one version of cluster's CA key that a first-key act names, never
// "latest" (persistence-api.md §3.4): a stored version no act names is inert. A version that is
// missing, soft-deleted or destroyed is ErrAbsent, and no other version is read in its place; the
// rest is as ReadCAKey.
func (s *Signer) ReadCAKeyVersion(ctx context.Context, cluster string, version int) (CAKey, error) {
	if version < 1 {
		return CAKey{}, errors.New("provider: a ca key version is a positive integer")
	}
	return s.readCAKey(ctx, cluster, version)
}

// readCAKey is a CA key read at version, or at the latest when version is 0.
func (s *Signer) readCAKey(ctx context.Context, cluster string, version int) (CAKey, error) {
	p, err := CAKeyPath(cluster)
	if err != nil {
		return CAKey{}, err
	}
	l, err := readKV(ctx, s.c, p, version)
	if err != nil {
		return CAKey{}, err
	}
	if len(l.data) != 2 {
		return CAKey{}, l.r.bad("the data is not exactly a ca and a key")
	}
	crt, err := l.stringMember("ca")
	if err != nil {
		return CAKey{}, err
	}
	key, err := l.stringMember("key")
	if err != nil {
		return CAKey{}, err
	}
	return CAKey{v: CAKeyVersion{Path: p, Version: l.version, CreatedTime: l.created}, crt: &crt, key: &key}, nil
}

// WriteCAKey stores k as the next version of cluster's CA key: KV v2 with cas set to cas, the version
// the caller read, or 0 for a path with no version. ErrExists means the path changed since (or, for 0,
// already has a version): the key was not written. ErrDenied or ErrUnavailable does not tell whether
// it was. The answer is the new version's identity.
func (s *Signer) WriteCAKey(ctx context.Context, cluster string, k CAKey, cas int) (CAKeyVersion, error) {
	p, err := CAKeyPath(cluster)
	if err != nil {
		return CAKeyVersion{}, err
	}
	if k.crt == nil || k.key == nil || *k.crt == "" || *k.key == "" || cas < 0 {
		return CAKeyVersion{}, errors.New("provider: a ca key with a certificate and a key, and a cas of 0 or more, is required")
	}
	type options struct {
		CAS int `json:"cas"`
	}
	type data struct {
		CA  string `json:"ca"`
		Key string `json:"key"`
	}
	body, err := json.Marshal(struct {
		Options options `json:"options"`
		Data    data    `json:"data"`
	}{options{cas}, data{*k.crt, *k.key}})
	if err != nil {
		return CAKeyVersion{}, errors.New("provider: the ca key could not be encoded")
	}
	r, err := s.c.do(ctx, http.MethodPost, "/v1/secret/data/"+p, body, true)
	if err != nil {
		return CAKeyVersion{}, err
	}
	var out struct {
		Data *struct {
			Version     *int    `json:"version"`
			CreatedTime *string `json:"created_time"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return CAKeyVersion{}, err
	}
	if out.Data == nil || out.Data.Version == nil || out.Data.CreatedTime == nil || *out.Data.Version != cas+1 {
		return CAKeyVersion{}, r.bad("a cas write answered without the next version")
	}
	created, err := time.Parse(time.RFC3339Nano, *out.Data.CreatedTime)
	if err != nil || created.IsZero() {
		return CAKeyVersion{}, r.bad("the created_time is not a time")
	}
	return CAKeyVersion{Path: p, Version: *out.Data.Version, CreatedTime: created}, nil
}

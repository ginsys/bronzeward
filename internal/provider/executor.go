package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Executor is the executor identity's provider client (compilation.md §1, persistence-api.md
// §3.3, execution-recovery.md §3.1). It reads the Talos access its observations use and decrypts
// the rendered artifact it dispatches under the artifact key; no other identity may decrypt that
// key (TestExecutorMethodSet).
type Executor struct {
	c        *client
	artifact string
}

// NewExecutor builds the client without contacting the provider. artifactKey is the Transit key
// the compiler encrypts artifacts under, held to the same rule as every key name (transitPath).
func NewExecutor(addr string, tok Token, artifactKey string) (*Executor, error) {
	if err := keySegment(artifactKey); err != nil {
		return nil, err
	}
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	return &Executor{c: c, artifact: artifactKey}, nil
}

// ArtifactKey is the name of the Transit key the executor decrypts artifacts under, as a use-time
// check records it next to the key version.
func (e *Executor) ArtifactKey() string { return e.artifact }

// TalosAccess reads the latest version of cluster's Talos credential, as Ingestion.TalosAccess
// does, under the executor's token.
func (e *Executor) TalosAccess(ctx context.Context, cluster string) (TalosAccess, error) {
	return readTalosAccess(ctx, e.c, cluster)
}

// Artifact is a decrypted rendered artifact: the machine configuration a dispatch sends. It renders
// as a placeholder under every fmt verb and refuses JSON, YAML and text marshalling, so a log line,
// an error or a response body cannot carry it by accident; ArtifactBytes is the one way out. The
// way out is a function, not a method: no interface the value satisfies or is asserted to can then
// yield the plaintext, and a source guard holds every reference to the function to the dispatch
// sender. The plaintext is held in a closure, not a field of slice or pointer-to-slice type: fmt
// walks the unexported field of a caller's own struct by reflection, where it cannot call Format,
// and renders such a field's bytes (and a pointer to them under %s or %q); a func renders as its
// address under every verb and reflection reaches nothing inside it.
type Artifact struct{ get func() []byte }

// ArtifactBytes is a copy of a's plaintext, one per call: the caller owns it, and editing or
// zeroing it changes nothing a later call returns.
func ArtifactBytes(a Artifact) []byte {
	if a.get == nil {
		return nil
	}
	return a.get()
}

const artifactPlaceholder = "[artifact: not rendered]"

// String, GoString and Format render the placeholder; the marshallers refuse.
func (Artifact) String() string   { return artifactPlaceholder }
func (Artifact) GoString() string { return artifactPlaceholder }
func (Artifact) Format(s fmt.State, _ rune) {
	_, _ = s.Write([]byte(artifactPlaceholder))
}
func (Artifact) MarshalJSON() ([]byte, error) { return nil, errArtifactRender }
func (Artifact) MarshalText() ([]byte, error) { return nil, errArtifactRender }
func (Artifact) MarshalYAML() (any, error)    { return nil, errArtifactRender }

var errArtifactRender = errors.New("provider: a decrypted artifact is not rendered")

// DecryptArtifact decrypts ct under the artifact key, as the bound key version keyVersion
// (execution-recovery.md §3.1 item 2): a ciphertext that names another version, or that is not a
// Transit ciphertext, is refused before any request. An unavailable provider (sealed, stopped,
// rate-limited, unreachable) is ErrUnavailable and neither a refusal nor a verdict on the
// artifact. No error quotes the ciphertext or the plaintext.
func (e *Executor) DecryptArtifact(ctx context.Context, ct Ciphertext, keyVersion int) (Artifact, error) {
	n, err := ct.KeyVersion()
	if err != nil {
		return Artifact{}, err
	}
	if n != keyVersion {
		return Artifact{}, fmt.Errorf("provider: the artifact is encrypted at key version %d, not the bound %d", n, keyVersion)
	}
	path, err := transitPath("decrypt", e.artifact)
	if err != nil {
		return Artifact{}, err
	}
	body, err := json.Marshal(map[string]string{"ciphertext": string(ct)})
	if err != nil {
		return Artifact{}, errors.New("provider: the request could not be encoded")
	}
	r, err := e.c.do(ctx, http.MethodPost, path, body, false)
	if err != nil {
		return Artifact{}, err
	}
	var out struct {
		Data *struct {
			Plaintext *string `json:"plaintext"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return Artifact{}, err
	}
	if out.Data == nil || out.Data.Plaintext == nil {
		return Artifact{}, r.bad("no plaintext")
	}
	plain, err := strictBase64(*out.Data.Plaintext)
	if err != nil {
		return Artifact{}, r.bad("the plaintext is not base64")
	}
	return Artifact{get: func() []byte { return bytes.Clone(plain) }}, nil
}

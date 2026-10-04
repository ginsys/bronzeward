package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Compiler is the compiler identity's provider client (compilation.md §1): it reads a pinned
// version of a secret generation and encrypts under the artifact key. It has no decryption, no
// secret creation and no other read (TestCompilerMethodSet).
type Compiler struct {
	c        *client
	artifact string
}

// NewCompiler builds the client without contacting the provider. artifactKey is the Transit key
// artifacts are encrypted under (compilation.md §11).
func NewCompiler(addr string, tok Token, artifactKey string) (*Compiler, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	if err := keySegment(artifactKey); err != nil {
		return nil, err
	}
	return &Compiler{c: c, artifact: artifactKey}, nil
}

// ParseGenerationPath is NewGenerationPath of a stored generation path, gen/<cluster>/<claim>/<value>.
func ParseGenerationPath(s string) (GenerationPath, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 4 || parts[0] != "gen" {
		return GenerationPath{}, errors.New("provider: a generation path is gen/<cluster>/<claim>/<value>")
	}
	return NewGenerationPath(parts[1], parts[2], parts[3])
}

// ReadGeneration reads version of the generation at p: a KV v2 read of secret/data/<p>?version=N
// (compilation.md §6 step 4). It returns the value and the version's created_time, which the
// caller compares with the one its classification took (step 4: a different one refuses). A
// missing, deleted or destroyed version is OpenBao's 404, ErrAbsent; a policy refusal is
// ErrDenied; an answer that is not the asked version, live, holding exactly a {kind, value} of
// its kind is ErrProtocol. No error quotes the value.
func (c *Compiler) ReadGeneration(ctx context.Context, p GenerationPath, version int64) (Value, time.Time, error) {
	if p.zero() {
		return Value{}, time.Time{}, errors.New("provider: a generation path made by NewGenerationPath is required")
	}
	if version < 1 {
		return Value{}, time.Time{}, errors.New("provider: a generation version is a positive integer")
	}
	r, err := c.c.do(ctx, http.MethodGet, kvDataPath(p)+"?version="+strconv.FormatInt(version, 10), nil, false)
	if err != nil {
		return Value{}, time.Time{}, err
	}
	var out struct {
		Data *struct {
			Data     map[string]json.RawMessage `json:"data"`
			Metadata *struct {
				Version      *int64  `json:"version"`
				CreatedTime  *string `json:"created_time"`
				DeletionTime *string `json:"deletion_time"`
				Destroyed    *bool   `json:"destroyed"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return Value{}, time.Time{}, err
	}
	if out.Data == nil || out.Data.Metadata == nil {
		return Value{}, time.Time{}, r.bad("no data or metadata")
	}
	m := out.Data.Metadata
	if m.Version == nil || *m.Version != version {
		return Value{}, time.Time{}, r.bad("the answer is not the asked version")
	}
	if m.CreatedTime == nil {
		return Value{}, time.Time{}, r.bad("no created_time")
	}
	created, err := time.Parse(time.RFC3339Nano, *m.CreatedTime)
	if err != nil || created.IsZero() {
		return Value{}, time.Time{}, r.bad("the created_time is not a time")
	}
	// A live version states it: destroyed false, and deletion_time "" or a scheduled deletion
	// (delete_version_after). OpenBao answers a version whose deletion has passed with 404, so a
	// version served with its data is live; whether a scheduled one may be used is the
	// classification's (compilation §6 step 3). Absent or null is not live.
	if m.DeletionTime == nil || m.Destroyed == nil {
		return Value{}, time.Time{}, r.bad("no deletion_time or destroyed")
	}
	if *m.Destroyed {
		return Value{}, time.Time{}, r.bad("the version is destroyed")
	}
	if *m.DeletionTime != "" {
		if _, err := time.Parse(time.RFC3339Nano, *m.DeletionTime); err != nil {
			return Value{}, time.Time{}, r.bad("the deletion_time is not a time")
		}
	}
	d := out.Data.Data
	rawKind, okKind := d["kind"]
	rawValue, okValue := d["value"]
	if !okKind || !okValue || len(d) != 2 {
		return Value{}, time.Time{}, r.bad("the data is not exactly a kind and a value")
	}
	var kind string
	if json.Unmarshal(rawKind, &kind) != nil {
		return Value{}, time.Time{}, r.bad("the kind is not a string")
	}
	s := string(rawValue)
	v := Value{&value{kind: Kind(kind), json: &s}}
	if v.check() != nil {
		return Value{}, time.Time{}, r.bad("the value is not of a known kind, or not of its kind")
	}
	return v, created.UTC(), nil
}

// ArtifactKey is the name of the Transit key EncryptArtifact encrypts under: the key §11's
// metadata reads classify and §9 records.
func (c *Compiler) ArtifactKey() string { return c.artifact }

// EncryptArtifact encrypts an artifact under the artifact key (compilation.md §11). Only the
// executor may decrypt it.
func (c *Compiler) EncryptArtifact(ctx context.Context, plaintext []byte) (Ciphertext, error) {
	return c.c.encrypt(ctx, c.artifact, plaintext)
}

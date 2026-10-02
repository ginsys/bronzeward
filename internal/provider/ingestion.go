// Package provider is Bronzeward's OpenBao client. It exposes one role type, Ingestion, with
// exactly the operations compilation.md §1 and persistence-api.md §3.3 give the ingestion
// identity: create-only secret generations, encryption under the baseline key, encryption and
// decryption under the staging key, HMAC under the digest key, and a read of a cluster's Talos
// access credential. It reads no other secret, has no baseline or artifact decryption and no
// other provider call, whatever a token's policy would allow (TestIngestionMethodSet).
//
// No error from this package carries a request or response body, server error text or a
// transport error's text; see requestError. A provider error on CreateGeneration, typed or not,
// means the generation may or may not exist: its path must never be retried, since a retry's
// ErrExists or ErrDenied would then be read as the first attempt's outcome.
package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Keys names the Transit keys of the three ingestion operations. Each is one URL path segment,
// and the three are distinct.
type Keys struct{ Baseline, Staging, Digest string }

// Ingestion is the ingestion identity's provider client.
type Ingestion struct {
	c    *client
	keys Keys
}

// NewIngestion builds the client without contacting the provider. addr is scheme://host[:port];
// which hosts may be reached over plain http is the configuration's rule (internal/config).
func NewIngestion(addr string, tok Token, keys Keys) (*Ingestion, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{keys.Baseline, keys.Staging, keys.Digest} {
		if err := keySegment(k); err != nil {
			return nil, err
		}
	}
	if keys.Baseline == keys.Staging || keys.Baseline == keys.Digest || keys.Staging == keys.Digest {
		return nil, errors.New("provider: the baseline, staging and digest keys must be distinct")
	}
	return &Ingestion{c: c, keys: keys}, nil
}

// Generation is a created generation. A cas=0 create makes version 1 or nothing.
type Generation struct {
	Path    GenerationPath
	Version int
}

// CreateGeneration stores v at p, create-only: KV v2 with cas=0 (compilation.md §2.3 step 6). The
// path must be new. ErrExists or ErrDenied means it was not created by this request, which does
// not mean it does not exist; see the package comment.
func (i *Ingestion) CreateGeneration(ctx context.Context, p GenerationPath, v Value) (Generation, error) {
	if p.zero() {
		return Generation{}, errors.New("provider: a generation path made by NewGenerationPath is required")
	}
	if err := v.check(); err != nil {
		return Generation{}, err
	}
	type data struct {
		Kind  Kind            `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	type options struct {
		CAS int `json:"cas"`
	}
	body, err := json.Marshal(struct {
		Options options `json:"options"`
		Data    data    `json:"data"`
	}{options{CAS: 0}, data{v.p.kind, v.p.json}})
	if err != nil {
		return Generation{}, errors.New("provider: the generation could not be encoded")
	}
	path := kvDataPath(p)
	r, err := i.c.do(ctx, http.MethodPost, path, body, true)
	if err != nil {
		return Generation{}, err
	}
	var out struct {
		Data *struct {
			Version *int `json:"version"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return Generation{}, err
	}
	if out.Data == nil || out.Data.Version == nil || *out.Data.Version != 1 {
		return Generation{}, r.bad("a cas=0 create answered without version 1")
	}
	return Generation{Path: p, Version: 1}, nil
}

// Ciphertext is a Transit ciphertext, "vault:v<N>:<base64>".
type Ciphertext string

// KeyVersion is N, the key version that encrypted c. A c whose payload is not nonempty canonical
// base64 is refused: Transit could not decrypt it.
func (c Ciphertext) KeyVersion() (int, error) {
	n, rest, err := versioned(string(c))
	if err != nil || rest == "" {
		return 0, errors.New(`provider: not a Transit ciphertext ("vault:v<N>:<base64>")`)
	}
	if _, err := strictBase64(rest); err != nil {
		return 0, errors.New(`provider: not a Transit ciphertext ("vault:v<N>:<base64>")`)
	}
	return n, nil
}

// strictBase64 decodes padded standard base64, as OpenBao encodes, refusing non-canonical padding
// bits and the line breaks the decoder would otherwise skip.
func strictBase64(s string) ([]byte, error) {
	if strings.ContainsAny(s, "\r\n") {
		return nil, errors.New("a line break in base64")
	}
	return base64.StdEncoding.Strict().DecodeString(s)
}

// versioned splits "vault:v<N>:<rest>", N a positive integer without sign or leading zero.
func versioned(s string) (int, string, error) {
	after, ok := strings.CutPrefix(s, "vault:v")
	if !ok {
		return 0, "", errors.New("no vault:v prefix")
	}
	num, rest, ok := strings.Cut(after, ":")
	if !ok || num == "" || num[0] == '0' || strings.Trim(num, "0123456789") != "" {
		return 0, "", errors.New("no key version")
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, "", errors.New("no key version")
	}
	return n, rest, nil
}

// EncryptBaseline encrypts the exact input under the baseline key (compilation.md §2.3 step 8):
// no identity of compilation.md §1 can decrypt it.
func (i *Ingestion) EncryptBaseline(ctx context.Context, plaintext []byte) (Ciphertext, error) {
	return i.encrypt(ctx, i.keys.Baseline, plaintext)
}

// EncryptStaging encrypts an encrypted claim's envelope under the staging key (compilation.md §3).
func (i *Ingestion) EncryptStaging(ctx context.Context, envelope []byte) (Ciphertext, error) {
	return i.encrypt(ctx, i.keys.Staging, envelope)
}

func (i *Ingestion) encrypt(ctx context.Context, key string, plaintext []byte) (Ciphertext, error) {
	path, err := transitPath("encrypt", key)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext)})
	if err != nil {
		return "", errors.New("provider: the request could not be encoded")
	}
	r, err := i.c.do(ctx, http.MethodPost, path, body, false)
	if err != nil {
		return "", err
	}
	var out struct {
		Data *struct {
			Ciphertext *string `json:"ciphertext"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return "", err
	}
	if out.Data == nil || out.Data.Ciphertext == nil {
		return "", r.bad("no ciphertext")
	}
	ct := Ciphertext(*out.Data.Ciphertext)
	if _, err := ct.KeyVersion(); err != nil {
		return "", r.bad("the ciphertext is not vault:v<N>:<base64>")
	}
	return ct, nil
}

// DecryptStaging decrypts an envelope encrypted by EncryptStaging. Only ingestion identities may
// (compilation.md §3).
func (i *Ingestion) DecryptStaging(ctx context.Context, ct Ciphertext) ([]byte, error) {
	if _, err := ct.KeyVersion(); err != nil {
		return nil, err
	}
	path, err := transitPath("decrypt", i.keys.Staging)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"ciphertext": string(ct)})
	if err != nil {
		return nil, errors.New("provider: the request could not be encoded")
	}
	r, err := i.c.do(ctx, http.MethodPost, path, body, false)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data *struct {
			Plaintext *string `json:"plaintext"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return nil, err
	}
	if out.Data == nil || out.Data.Plaintext == nil {
		return nil, r.bad("no plaintext")
	}
	plain, err := strictBase64(*out.Data.Plaintext)
	if err != nil {
		return nil, r.bad("the plaintext is not base64")
	}
	return plain, nil
}

// Digest is a keyed digest: HMAC-SHA-256 under a Transit key (compilation.md §4.1, choice
// §16.27), with the key and the version that computed it.
type Digest struct {
	Key     string
	Version int
	Sum     [32]byte
}

// KeyRef is the key identity a record stores beside the digest: "transit/<key>@v<N>".
func (d Digest) KeyRef() string { return "transit/" + d.Key + "@v" + strconv.Itoa(d.Version) }

// Digest computes Transit hmac with sha2-256 under the digest key, at key version version, or the
// latest when version is 0. A retry recomputes under the recorded version (persistence-api.md
// §7). An answer under another version than the one requested is ErrProtocol.
func (i *Ingestion) Digest(ctx context.Context, input []byte, version int) (Digest, error) {
	if version < 0 {
		return Digest{}, errors.New("provider: a digest key version is positive, or 0 for the latest")
	}
	path, err := transitPath("hmac", i.keys.Digest)
	if err != nil {
		return Digest{}, err
	}
	req := map[string]any{"input": base64.StdEncoding.EncodeToString(input), "algorithm": "sha2-256"}
	if version > 0 {
		req["key_version"] = version
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Digest{}, errors.New("provider: the request could not be encoded")
	}
	r, err := i.c.do(ctx, http.MethodPost, path, body, false)
	if err != nil {
		return Digest{}, err
	}
	var out struct {
		Data *struct {
			HMAC *string `json:"hmac"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return Digest{}, err
	}
	if out.Data == nil || out.Data.HMAC == nil {
		return Digest{}, r.bad("no hmac")
	}
	n, rest, err := versioned(*out.Data.HMAC)
	if err != nil {
		return Digest{}, r.bad("the hmac is not vault:v<N>:…")
	}
	if version > 0 && n != version {
		return Digest{}, r.bad("the hmac is under another key version than the one requested")
	}
	sum, err := strictBase64(rest)
	if err != nil || len(sum) != 32 {
		return Digest{}, r.bad("the hmac is not 32 bytes of base64")
	}
	return Digest{Key: i.keys.Digest, Version: n, Sum: [32]byte(sum)}, nil
}

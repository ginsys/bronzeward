// Package provider is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It talks to the OpenBao the investigation fixtures already stand up: KV v2 mounted at secret/,
// the transit engine, and a transit key named bw-artifact. Nothing here selects a secret provider
// for v1 — that comparison is its own work item. What the experiment needs is *a* provider whose
// writes it can observe from outside the prototype, and this one it already has.
//
// It speaks HTTP directly rather than using a client library. Two reasons, both about evidence:
// the request this program makes is then visible in the source rather than behind a library's
// retry and caching behaviour, and go.sum stays short enough to audit in full.
//
// Two limits belong in the report rather than only here. The fixtures run OpenBao with
// tls_disable, so transit plaintext crosses loopback in the clear and no packet capture exists in
// this experiment — a real unmeasured surface. And OpenBao's own storage is not observable: the
// experiment can show a secret *reached* the provider, by reading it back and by the provider's
// own version timestamps, and cannot show what else is in there.
package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

// transitPath is the request path for a Transit operation on one named key. A Transit key name is a
// single path segment, so it is held to kvPath's rules and may not contain a '/' either: a name
// that did would address a different endpoint — "k/../../sys/…" — rather than a different key.
func transitPath(op, keyName string) (string, error) {
	if strings.Contains(keyName, "/") {
		return "", fmt.Errorf("provider: transit key name %q holds a '/', which would address another endpoint", keyName)
	}
	return kvPath("/v1/transit/"+op+"/", keyName)
}

// kvPath is the request path for a KV key under the given prefix, or an error for a key this client
// cannot address exactly.
//
// The key is written into the URL path as it stands, so any character a URL gives meaning to would
// move the request somewhere other than the key: a '#' ends the path and makes the rest a fragment,
// a '?' starts a query, a '%' begins an escape the server decodes, and a "." or ".." segment is
// collapsed. Two different keys could then land on one secret and the second write would replace
// the first. Such a key is refused rather than escaped, because escaping would change the request
// path of every key the matrix already stored, and the committed evidence records those paths.
func kvPath(prefix, key string) (string, error) {
	if key == "" {
		return "", errors.New("provider: an empty key addresses no secret")
	}
	if i := strings.IndexFunc(key, func(r rune) bool {
		return r == '#' || r == '?' || r == '%' || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r)
	}); i >= 0 {
		return "", fmt.Errorf("provider: key %q holds %q at byte %d, which a URL path would not carry literally; the request would reach a different secret", key, key[i], i)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("provider: key %q holds an empty, \".\" or \"..\" segment, which the server would collapse into another path", key)
		}
	}
	return prefix + key, nil
}

// Environment variables the fixtures publish. The token is read from the environment and never
// from a flag: argv is world-readable through /proc and is captured by the evidence bundles this
// program's own runs produce.
const (
	// AddrEnv overrides the address. The fixtures expose OpenBao on the host.
	AddrEnv = "BW_BAO_ADDR"
	// TokenEnv is the root token the fixtures publish in their state directory.
	TokenEnv = "BW_BAO_ROOT_TOKEN"
	// MetadataTokenEnv is the metadata-only identity. It can read versions and never values, and
	// is what the out-of-band observer uses so that reading the evidence cannot itself be the
	// thing that moved a secret.
	MetadataTokenEnv = "BW_BAO_METADATA_TOKEN"
)

// DefaultAddr is where the fixtures publish OpenBao (fixtures/versions.env, OPENBAO_PORT=58200).
const DefaultAddr = "http://127.0.0.1:58200"

// TransitKey is the key the fixtures create for artifact encryption.
const TransitKey = "bw-artifact"

// requestTimeout bounds every call. An unbounded request would let a netsplit injection hang the
// run instead of producing the recovery failure the experiment is trying to measure.
const requestTimeout = 30 * time.Second

// Client is an OpenBao client.
type Client struct {
	addr  string
	token string
	http  *http.Client
}

// New builds a client. It does not contact the server: a constructor that reached out would make
// every unit test need one.
func New(addr, token string) (*Client, error) {
	if addr == "" {
		return nil, fmt.Errorf("provider: no address")
	}
	if token == "" {
		return nil, fmt.Errorf("provider: no token; set %s from the fixtures' state directory", TokenEnv)
	}
	if _, err := url.Parse(addr); err != nil {
		return nil, fmt.Errorf("provider: address %q: %w", addr, err)
	}
	return &Client{
		addr:  strings.TrimSuffix(addr, "/"),
		token: token,
		http:  &http.Client{Timeout: requestTimeout},
	}, nil
}

// FromEnv builds a client from the variables the fixtures publish, falling back to DefaultAddr.
// Which token it reads is the caller's choice: the ingesting run uses the root token, and the
// out-of-band observer uses the metadata-only one so that collecting evidence cannot itself read
// a value.
func FromEnv(tokenEnv string) (*Client, error) {
	addr := os.Getenv(AddrEnv)
	if addr == "" {
		addr = DefaultAddr
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("provider: %s is not set; the fixtures publish it in their state "+
			"directory, and it is read from the environment because argv is world-readable", tokenEnv)
	}
	return New(addr, token)
}

// Name implements extract.Store.
func (c *Client) Name() string { return "openbao:" + c.addr }

// Put stores value at secret/<key> in KV v2 and returns the URI that addresses that version.
//
// The value is stored as a JSON string, which requires it to be valid UTF-8. Every secret in a
// Talos configuration is PEM or base64 text, so this holds here; a binary secret would need
// encoding, and encoding it would also put it beyond what the leak scan can recognise, which is a
// reason to state the constraint rather than to paper over it.
func (c *Client) Put(ctx context.Context, key string, value []byte) (string, error) {
	body := map[string]any{"data": map[string]string{"value": string(value)}}

	var out struct {
		Data struct {
			Version int `json:"version"`
		} `json:"data"`
	}
	path, err := kvPath("/v1/secret/data/", key)
	if err != nil {
		return "", err
	}
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	if out.Data.Version == 0 {
		// Without a version the URI cannot address what was just written, and a later read could
		// return a different value than the one this run stored.
		return "", fmt.Errorf("provider: writing secret/%s returned no version", key)
	}
	return fmt.Sprintf("kv://secret/%s@%d", key, out.Data.Version), nil
}

// Get reads back the latest version at secret/<key>. It is how the experiment shows a value
// reached the provider intact, rather than inferring it from a successful write.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	var out struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	path, err := kvPath("/v1/secret/data/", key)
	if err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	value, ok := out.Data.Data["value"]
	if !ok {
		return nil, fmt.Errorf("provider: secret/%s holds no value field", key)
	}
	return []byte(value), nil
}

// Version is one KV version's metadata.
type Version struct {
	// Number is the version.
	Number int
	// Created is the provider's own timestamp for it. It is an observer independent of this
	// program's journal, which is why it is collected at all.
	Created time.Time
}

// Versions reads the metadata for secret/<key>. It reads no values, so it works under the
// metadata-only token, and that is how the experiment collects it: an observer that could read the
// values would be a worse observer.
func (c *Client) Versions(ctx context.Context, key string) ([]Version, error) {
	var out struct {
		Data struct {
			Versions map[string]struct {
				CreatedTime time.Time `json:"created_time"`
			} `json:"versions"`
		} `json:"data"`
	}
	path, err := kvPath("/v1/secret/metadata/", key)
	if err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	versions := make([]Version, 0, len(out.Data.Versions))
	for n, v := range out.Data.Versions {
		var number int
		if _, err := fmt.Sscanf(n, "%d", &number); err != nil {
			return nil, fmt.Errorf("provider: secret/%s has a version named %q: %w", key, n, err)
		}
		versions = append(versions, Version{Number: number, Created: v.CreatedTime})
	}
	// Sorted, so two runs over the same key produce the same evidence ordering. The response is a
	// JSON object, and Go's map iteration is deliberately unordered.
	sort.Slice(versions, func(i, j int) bool { return versions[i].Number < versions[j].Number })
	return versions, nil
}

// Encrypt returns transit ciphertext for plaintext.
//
// Transit uses a fresh nonce per call, so encrypting identical input twice gives different
// ciphertext. That is correct behaviour and it is why the encrypted baseline is verified by
// decrypting rather than by comparing ciphertext across runs.
func (c *Client) Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	body := map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext)}

	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	path, err := transitPath("encrypt", keyName)
	if err != nil {
		return "", err
	}
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	if out.Data.Ciphertext == "" {
		return "", fmt.Errorf("provider: transit/encrypt/%s returned no ciphertext", keyName)
	}
	return out.Data.Ciphertext, nil
}

// Decrypt returns the plaintext for transit ciphertext.
func (c *Client) Decrypt(ctx context.Context, keyName, ciphertext string) ([]byte, error) {
	body := map[string]string{"ciphertext": ciphertext}

	// A pointer, so a response with no plaintext field is told apart from one whose plaintext is
	// empty. Decoding "" succeeds, so without this a malformed response returned an empty document
	// and no error — the one call that could fail silently, where Put, Get and Encrypt all reject
	// a response missing their field.
	var out struct {
		Data struct {
			Plaintext *string `json:"plaintext"`
		} `json:"data"`
	}
	path, err := transitPath("decrypt", keyName)
	if err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	if out.Data.Plaintext == nil {
		return nil, fmt.Errorf("provider: transit/decrypt/%s returned no plaintext field", keyName)
	}
	plaintext, err := base64.StdEncoding.DecodeString(*out.Data.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("provider: transit/decrypt/%s returned undecodable plaintext: %w", keyName, err)
	}
	return plaintext, nil
}

// do performs one request. It never puts a request or response body in an error: a failing write
// carries the secret it was trying to store, and an error string ends up in logs, which §7.1 names
// as a persistence surface in its own right.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("provider: encoding the request for %s: %w", path, err)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.addr+path, body)
	if err != nil {
		return fmt.Errorf("provider: building the request for %s: %w", path, err)
	}
	req.Header.Set("X-Vault-Token", c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// A transport error's text can hold the URL but never the body, so it is safe to wrap.
		return fmt.Errorf("provider: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("provider: reading the response to %s %s: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider: %s %s: %s: %s", method, path, resp.Status, serverErrors(payload))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("provider: decoding the response to %s %s: %w", method, path, err)
	}
	return nil
}

// serverErrors pulls OpenBao's own error strings out of a failure response and returns nothing
// else. The rest of the body is discarded rather than quoted, because a failed write's response
// is the one place a server might echo what it was sent.
func serverErrors(payload []byte) string {
	var parsed struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil || len(parsed.Errors) == 0 {
		return "the server gave no error message (the response body is not quoted here, since a " +
			"failed write's response is where a server would echo what it was sent)"
	}
	return strings.Join(parsed.Errors, "; ")
}

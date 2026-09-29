// Package issuer is the fixture's disposable OIDC issuer (acceptance-plan.md §2). It serves a
// discovery document and a JWKS for one synthetic ES256 key, and it mints access tokens for the
// synthetic humans, valid or carrying one named defect (persistence-api.md §10.1). Defect tokens
// are built here with go-jose, never through the server's verifier. It is not an identity
// provider: no login, no clients, no refresh. Bronzeward is tested against it only; no
// provider product's interoperability is claimed.
package issuer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// Humans maps each synthetic human, who is also the token's sub, to its groups. The groups are
// examples/bronzeward.yaml's.
var Humans = map[string][]string{
	"h-author":    {"bw-authors"},
	"h-publisher": {"bw-publishers"},
	"h-approver":  {"bw-approvers"},
	"h-recovery":  {"bw-recovery"},
	"h-viewer":    {"bw-viewers"},
	"h-all":       {"bw-authors", "bw-publishers", "bw-approvers"},
}

// Defects lists every defect Mint can put in a token. groups-not-array and groups-null-element
// are Bronzeward's own rules; every other entry is a check of persistence-api.md §10.1.
// over-lifetime is 24 hours, longer than any sane maxTokenLifetime.
var Defects = []string{
	"malformed", "alg-none", "alg-hs256", "unknown-kid", "wrong-key", "bad-signature",
	"wrong-issuer", "wrong-audience", "expired", "not-yet-valid", "no-exp", "no-iat",
	"future-iat", "over-lifetime", "no-sub", "empty-sub", "numeric-sub", "groups-not-array",
	"groups-null-element",
}

// Lifetime is a valid token's exp - iat, inside the default 15-minute maximum.
const Lifetime = 5 * time.Minute

type Issuer struct {
	URL      string // the iss claim and the discovery document's issuer
	Audience string
	key      jose.JSONWebKey
}

// NewKey returns a fresh P-256 signing key with a random key ID.
func NewKey() (jose.JSONWebKey, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return jose.JSONWebKey{}, err
	}
	var kid [8]byte
	rand.Read(kid[:])
	return jose.JSONWebKey{Key: k, KeyID: hex.EncodeToString(kid[:]), Algorithm: string(jose.ES256), Use: "sig"}, nil
}

// WriteKey writes key, private part included, to a new file readable by its owner only.
func WriteKey(path string, key jose.JSONWebKey) error {
	b, err := json.Marshal(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func ReadKey(path string) (jose.JSONWebKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return jose.JSONWebKey{}, err
	}
	var k jose.JSONWebKey
	if err := k.UnmarshalJSON(b); err != nil {
		return jose.JSONWebKey{}, fmt.Errorf("issuer: %s: %w", path, err)
	}
	return k, nil
}

// New returns an issuer at issuerURL, which has no path: the handler serves from the root.
func New(issuerURL, audience string, key jose.JSONWebKey) (*Issuer, error) {
	u, err := url.Parse(issuerURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("issuer: %q is not an http(s) URL without a path", issuerURL)
	}
	if _, ok := key.Key.(*ecdsa.PrivateKey); !ok || key.KeyID == "" {
		return nil, errors.New("issuer: the key is not an ECDSA private key with a key ID")
	}
	return &Issuer{URL: issuerURL, Audience: audience, key: key}, nil
}

// Handler serves the discovery document and the public key set.
func (i *Issuer) Handler() http.Handler {
	discovery := map[string]any{
		"issuer":                                i.URL,
		"jwks_uri":                              i.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{string(jose.ES256)},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
	}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{i.key.Public()}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, discovery) })
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, jwks) })
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// Mint returns an access token for human, issued at now, carrying defect ("" for none).
func (i *Issuer) Mint(human, defect string, now time.Time) (string, error) {
	groups, ok := Humans[human]
	if !ok {
		return "", fmt.Errorf("issuer: unknown human %q", human)
	}
	at := func(d time.Duration) int64 { return now.Add(d).Unix() }
	c := map[string]any{
		"iss": i.URL, "aud": i.Audience, "sub": human, "groups": groups,
		"iat": at(0), "nbf": at(0), "exp": at(Lifetime),
	}
	key := i.key
	switch defect {
	case "":
	case "malformed":
		return "not-a-jwt", nil
	case "alg-none":
		return unsigned(c)
	case "alg-hs256":
		return i.hmac(c)
	case "unknown-kid", "wrong-key":
		other, err := NewKey()
		if err != nil {
			return "", err
		}
		if defect == "wrong-key" {
			other.KeyID = i.key.KeyID
		}
		key = other
	case "bad-signature":
		return i.tampered(c)
	case "wrong-issuer":
		c["iss"] = i.URL + "/other"
	case "wrong-audience":
		c["aud"] = "not-" + i.Audience
	case "expired":
		c["iat"], c["nbf"], c["exp"] = at(-10*time.Minute), at(-10*time.Minute), at(-2*time.Minute)
	case "not-yet-valid":
		c["nbf"] = at(2 * time.Minute)
	case "no-exp":
		delete(c, "exp")
	case "no-iat":
		delete(c, "iat")
	case "future-iat":
		c["iat"], c["exp"] = at(2*time.Minute), at(2*time.Minute+Lifetime)
	case "over-lifetime":
		c["exp"] = at(24 * time.Hour)
	case "no-sub":
		delete(c, "sub")
	case "empty-sub":
		c["sub"] = ""
	case "numeric-sub":
		c["sub"] = 42
	case "groups-not-array":
		c["groups"] = strings.Join(groups, ",")
	case "groups-null-element":
		c["groups"] = append([]any{nil}, stringsToAny(groups)...)
	default:
		return "", fmt.Errorf("issuer: unknown defect %q", defect)
	}
	return sign(key, c)
}

// Sign signs claims with the issuer's key, for a test that needs a token Mint does not make.
func (i *Issuer) Sign(claims map[string]any) (string, error) { return sign(i.key, claims) }

func sign(key jose.JSONWebKey, claims any) (string, error) {
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	obj, err := s.Sign(payload)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

// hmac signs with HS256 keyed by the public JWK's JSON: the algorithm-confusion token a verifier
// that trusted the header's alg would accept.
func (i *Issuer) hmac(claims any) (string, error) {
	pub, err := json.Marshal(i.key.Public())
	if err != nil {
		return "", err
	}
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: pub},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", i.key.KeyID))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	obj, err := s.Sign(payload)
	if err != nil {
		return "", err
	}
	return obj.CompactSerialize()
}

func unsigned(claims any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc.EncodeToString(payload) + ".", nil
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// tampered signs claims, then swaps in a payload naming another subject under the same signature.
func (i *Issuer) tampered(claims map[string]any) (string, error) {
	tok, err := sign(i.key, claims)
	if err != nil {
		return "", err
	}
	// A suffix, not another human's name: the swapped payload must differ for every human.
	claims["sub"] = fmt.Sprint(claims["sub"]) + "-tampered"
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	parts := strings.Split(tok, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join(parts, "."), nil
}

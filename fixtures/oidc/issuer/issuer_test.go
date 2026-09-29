package issuer

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/ginsys/bronzeward/internal/config"
)

func testIssuer(t *testing.T) (*Issuer, jose.JSONWebKey) {
	t.Helper()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	iss, err := New("http://issuer.test", "bronzeward", key)
	if err != nil {
		t.Fatal(err)
	}
	return iss, key
}

func TestDiscoveryAndJWKS(t *testing.T) {
	iss, key := testIssuer(t)
	rec := httptest.NewRecorder()
	iss.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/openid-configuration", nil))
	var doc struct {
		Issuer string `json:"issuer"`
		JWKS   string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Issuer != "http://issuer.test" || doc.JWKS != "http://issuer.test/jwks" {
		t.Fatalf("discovery: %+v, %v", doc, err)
	}
	rec = httptest.NewRecorder()
	iss.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/jwks", nil))
	if strings.Contains(rec.Body.String(), `"d"`) {
		t.Fatal("the JWKS publishes the private key")
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil || len(set.Keys) != 1 || !set.Keys[0].IsPublic() || set.Keys[0].KeyID != key.KeyID {
		t.Fatalf("jwks: %+v, %v", set, err)
	}
}

func TestMintValid(t *testing.T) {
	iss, key := testIssuer(t)
	now := time.Now()
	for human, groups := range Humans {
		tok, err := iss.Mint(human, "", now)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := jwt.ParseSigned(tok, []jose.SignatureAlgorithm{jose.ES256})
		if err != nil {
			t.Fatal(err)
		}
		var c jwt.Claims
		var extra struct {
			Groups []string `json:"groups"`
		}
		if err := parsed.Claims(key.Public(), &c, &extra); err != nil {
			t.Fatal(err)
		}
		if c.Subject != human || c.Issuer != iss.URL || !c.Audience.Contains("bronzeward") ||
			c.Expiry.Time().Sub(c.IssuedAt.Time()) != Lifetime || !slices.Equal(extra.Groups, groups) {
			t.Errorf("%s: %+v %v", human, c, extra.Groups)
		}
	}
}

func TestDefects(t *testing.T) {
	// persistence-api.md §10.1 and §16 name these; the list may grow, never lose one.
	for _, d := range []string{"malformed", "alg-none", "alg-hs256", "unknown-kid", "wrong-key", "bad-signature",
		"wrong-issuer", "wrong-audience", "expired", "not-yet-valid", "no-exp", "no-iat", "future-iat",
		"over-lifetime", "no-sub", "empty-sub", "numeric-sub"} {
		if !slices.Contains(Defects, d) {
			t.Errorf("Defects lacks %s", d)
		}
	}
	iss, key := testIssuer(t)
	now := time.Now()
	for human := range Humans {
		valid, err := iss.Mint(human, "", now)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range Defects {
			if tok, err := iss.Mint(human, d, now); err != nil || tok == valid {
				t.Errorf("%s %s: %v", human, d, err)
			}
		}
		// bad-signature must fail verification for every human, including the subject it swaps in.
		tok, err := iss.Mint(human, "bad-signature", now)
		if err != nil {
			t.Fatal(err)
		}
		jws, err := jose.ParseSigned(tok, []jose.SignatureAlgorithm{jose.ES256})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jws.Verify(key.Public()); err == nil {
			t.Errorf("%s: bad-signature verifies", human)
		}
	}
	if _, err := iss.Mint("h-all", "no-such-defect", now); err == nil {
		t.Error("unknown defect accepted")
	}
	if _, err := iss.Mint("h-nobody", "", now); err == nil {
		t.Error("unknown human accepted")
	}
}

// Every synthetic human's groups must grant a role in the committed example configuration.
func TestExampleConfigMapsEveryGroup(t *testing.T) {
	f, err := os.Open("../../../examples/bronzeward.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Auth.Roles
	mapped := slices.Concat(r.Viewer, r.Author, r.Publisher, r.Approver, r.RecoveryAdmin)
	for human, groups := range Humans {
		for _, g := range groups {
			if !slices.Contains(mapped, g) {
				t.Errorf("%s's group %s grants no role in the example", human, g)
			}
		}
	}
}

func TestKeyFile(t *testing.T) {
	_, key := testIssuer(t)
	p := filepath.Join(t.TempDir(), "key.json")
	if err := WriteKey(p, key); err != nil {
		t.Fatal(err)
	}
	if err := WriteKey(p, key); err == nil {
		t.Fatal("WriteKey overwrote an existing file")
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode())
	}
	got, err := ReadKey(p)
	if err != nil || got.KeyID != key.KeyID || got.IsPublic() {
		t.Fatalf("ReadKey: %+v, %v", got, err)
	}
}

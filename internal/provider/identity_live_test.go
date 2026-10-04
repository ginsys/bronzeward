package provider

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
)

// The compiler identity (compilation.md §1) and the metadata identity (dependency-monitor.md §4,
// choice §11.9), checked as the orphan-report identity is: each policy as the provider holds it,
// compared exactly; then each role client's own requests, and each request the identity must not
// make, refused beside the same request with the one withheld grant added.

// compilerGrant and metadataGrant are the only stanzas each policy may hold.
var (
	compilerGrant = map[string][]string{"secret/data/gen/*": {"read"}, "transit/encrypt/bw-artifact": {"update"}}
	metadataGrant = map[string][]string{"secret/metadata/*": {"read"}, "transit/keys/*": {"read"}}
)

// Extra grants for the identities' refusals, each exactly what one refusal withholds.
const (
	genMetaRead   = `path "secret/metadata/gen/*" { capabilities = ["read"] }`
	genMetaList   = `path "secret/metadata/gen/*" { capabilities = ["list"] }`
	keyConfigure  = `path "transit/keys/*" { capabilities = ["update"] }`
	genCreateOnly = `path "secret/data/gen/*" { capabilities = ["create"] }`
)

// policyExact reads policy name back from the provider, compares its rules with grant, checks
// that a token for it carries that policy alone, and shows the comparison fails once any one of
// extras is added to the committed file.
func policyExact(t *testing.T, b *baotest.Bao, name string, grant map[string][]string, extras []string) {
	t.Helper()
	got := policyRules(t, readPolicy(t, b, name))
	if !equalRules(got, grant) {
		t.Fatalf("%s as the provider holds it: %v, want %v", name, got, grant)
	}
	t.Logf("%s read back: %v", name, got)

	status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/auth/token/lookup", map[string]string{"token": b.Token(name)})
	if err != nil || status != http.StatusOK {
		t.Fatalf("token lookup: status %d, %v", status, err)
	}
	var look struct {
		Data struct {
			Policies []string `json:"policies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &look); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(look.Data.Policies, []string{b.Name(name)}) {
		t.Fatalf("%s token's policies: %q, want exactly %s", name, look.Data.Policies, name)
	}

	committed, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "openbao", "policies", name+".hcl"))
	if err != nil {
		t.Fatal(err)
	}
	for i, extra := range extras {
		// An extra on a path the grant already names widens that stanza: the policy is the
		// grant's other stanzas plus the extra, since a path named twice is not one policy.
		hcl := string(committed) + "\n" + extra + "\n"
		if m := stanza.FindStringSubmatch(extra); m != nil && grant[m[1]] != nil {
			hcl = extra + "\n"
			for p, caps := range grant {
				if p != m[1] {
					hcl += `path "` + p + `" { capabilities = ["` + strings.Join(caps, `", "`) + `"] }` + "\n"
				}
			}
		}
		policy := name + "-extra-" + string(rune('a'+i))
		b.Policy(policy, hcl)
		if equalRules(policyRules(t, readPolicy(t, b, policy)), grant) {
			t.Fatalf("control: the comparison passed %s with %s", name, extra)
		}
		t.Logf("control: %s plus %s fails the comparison", name, extra)
	}
}

func TestLiveCompilerPolicyExact(t *testing.T) {
	b := live(t)
	policyExact(t, b, "bw-compiler", compilerGrant, []string{
		decryptArtifact, genMetaRead, accessRead,
		`path "secret/data/gen/*" { capabilities = ["read", "create"] }`,
		`path "transit/encrypt/*" { capabilities = ["update"] }`,
	})
}

func TestLiveMetadataPolicyExact(t *testing.T) {
	b := live(t)
	policyExact(t, b, "bw-metadata", metadataGrant, []string{
		genRead, genMetaList, encryptArtifact,
		`path "secret/metadata/*" { capabilities = ["read", "list"] }`,
		`path "transit/keys/*" { capabilities = ["read", "update"] }`,
	})
}

func liveGrants(b *baotest.Bao) {
	for name, hcl := range map[string]string{
		"gen-meta-read": genMetaRead, "gen-meta-list": genMetaList, "key-configure": keyConfigure, "gen-create-only": genCreateOnly,
	} {
		b.Policy(name, hcl)
	}
}

// TestLiveCompilerIdentity: the compiler client reads a pinned generation and encrypts under the
// artifact key, which the executor decrypts; the identity is refused an artifact decrypt, a
// generation create and a generation's metadata. (Its refusal of the Talos access is
// TestLiveTalosAccess's.)
func TestLiveCompilerIdentity(t *testing.T) {
	b := live(t)
	liveGrants(b)
	ctx := t.Context()
	cl := id.New(id.Cluster)
	gp, err := ParseGenerationPath(seed(t, b, cl, id.New(id.Ingestion)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCompiler(b.Addr, tokenOf(b.Token("bw-compiler")), "bw-artifact")
	if err != nil {
		t.Fatal(err)
	}
	v, created, err := c.ReadGeneration(ctx, gp, 1)
	if err != nil {
		t.Fatalf("reading the pinned generation: %v", err)
	}
	if d, err := v.Decode(); err != nil || d != "synthetic" || v.Kind() != "string" || created.IsZero() {
		t.Fatalf("the pinned generation read: kind %s, %v, created %v", v.Kind(), err, created)
	}
	ct, err := c.EncryptArtifact(ctx, []byte("artifact"))
	if err != nil {
		t.Fatalf("encrypting under the artifact key: %v", err)
	}
	if got, err := decrypt(t, b, b.Token("bw-executor"), "bw-artifact", ct); err != nil || string(got) != "artifact" {
		t.Fatalf("control: the executor's decrypt of the compiler's artifact: %v", err)
	}
	t.Logf("the compiler read version 1 and encrypted an artifact the executor decrypted")

	deniedWithout(t, b, "the compiler decrypting an artifact", []string{"bw-compiler"}, "decrypt-artifact", func(tok string) error {
		_, err := decrypt(t, b, tok, "bw-artifact", ct)
		return err
	})
	deniedWithout(t, b, "the compiler creating a generation", []string{"bw-compiler"}, "gen-create-only", func(tok string) error {
		fresh, err := NewGenerationPath(cl, id.New(id.Ingestion), NewValueID())
		if err != nil {
			t.Fatal(err)
		}
		removeAtEnd(t, b, fresh.String())
		_, err = writeNoCAS(t, b, tok, fresh, "synthetic")
		return err
	})
	deniedWithout(t, b, "the compiler reading a generation's metadata", []string{"bw-compiler"}, "gen-meta-read", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodGet, "/v1/secret/metadata/"+gp.String(), nil)
		return err
	})
}

// TestLiveMetadataIdentity: the metadata client's two answers, KV metadata and Transit key state,
// classify as retained; the identity is refused a value read (RC row 057), a list, a decrypt
// under the key it classified retained (dependency-monitor.md §10.1 item 5), an encryption and a
// key configuration.
func TestLiveMetadataIdentity(t *testing.T) {
	b := live(t)
	liveGrants(b)
	ctx := t.Context()
	cl := id.New(id.Cluster)
	gp, err := ParseGenerationPath(seed(t, b, cl, id.New(id.Ingestion)))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMetadata(b.Addr, tokenOf(b.Token("bw-metadata")))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := m.KV(ctx, gp)
	if err != nil {
		t.Fatal(err)
	}
	if r := classify.Classify(classify.Dependency{Provider: classify.KV, Object: gp.String(), Version: 1}, kv); kv.Status != http.StatusOK || r.Class != classify.Retained || r.Created.IsZero() || r.Date.IsZero() {
		t.Fatalf("the generation's metadata: status %d, classified %s/%s", kv.Status, r.Class, r.Reason)
	}
	tr, err := m.Transit(ctx, "bw-artifact")
	if err != nil {
		t.Fatal(err)
	}
	if r := classify.Classify(classify.Dependency{Provider: classify.Transit, Object: "bw-artifact", Version: 1}, tr); tr.Status != http.StatusOK || r.Class != classify.Retained {
		t.Fatalf("the artifact key's state: status %d, classified %s/%s", tr.Status, r.Class, r.Reason)
	}
	t.Logf("the metadata identity read both answers; each classified retained")

	deniedWithout(t, b, "the metadata identity reading a value", []string{"bw-metadata"}, "gen-read", func(tok string) error {
		_, err := readVersion(t, b, tok, gp, "1")
		return err
	})
	deniedWithout(t, b, "the metadata identity listing a cluster's claims", []string{"bw-metadata"}, "gen-meta-list", func(tok string) error {
		_, err := raw(t, b, tok, "LIST", "/v1/secret/metadata/gen/"+cl, nil)
		return err
	})
	ct, err := encrypt(t, b, b.Token("bw-compiler"), "bw-artifact", []byte("artifact"))
	if err != nil {
		t.Fatal(err)
	}
	deniedWithout(t, b, "the metadata identity decrypting under a key it classified retained", []string{"bw-metadata"}, "decrypt-artifact", func(tok string) error {
		_, err := decrypt(t, b, tok, "bw-artifact", ct)
		return err
	})
	deniedWithout(t, b, "the metadata identity encrypting", []string{"bw-metadata"}, "encrypt-artifact", func(tok string) error {
		_, err := encrypt(t, b, tok, "bw-artifact", []byte("x"))
		return err
	})
	// deletion_allowed false is the key's default: the control's success changes nothing.
	deniedWithout(t, b, "the metadata identity configuring a key", []string{"bw-metadata"}, "key-configure", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodPost, "/v1/transit/keys/bw-artifact/config", map[string]bool{"deletion_allowed": false})
		return err
	})
	if strings.Contains(string(kv.Body.Bytes()), "synthetic") {
		t.Fatal("the metadata answer carries the value")
	}
}

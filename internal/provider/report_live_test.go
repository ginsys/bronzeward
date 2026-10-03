package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/id"
)

// The orphan-report identity (persistence-api.md §6.4, §16, choice §17.30): read and list on
// secret/metadata/gen/* and nothing else. Its boundary is checked twice: the policy as the
// provider holds it, compared exactly, so that it does not rest on a list of probes; and each
// probe §16 names, refused, then granted the one capability it needs on that path alone, so that
// a malformed probe cannot pass as a refusal.

// reportGrant is the only stanza the orphan-report policy may hold.
var reportGrant = map[string][]string{"secret/metadata/gen/*": {"list", "read"}}

// stanza is one rule of the fixture's policy files: a path and its capability list, nothing else.
var stanza = regexp.MustCompile(`path\s+"([^"]+)"\s*\{\s*capabilities\s*=\s*\[([^\]]*)\]\s*\}`)

// policyRules parses a policy's rules into path → sorted capabilities. Anything left once the
// comments and the stanzas are removed fails, so a construct this parser does not know cannot
// widen the policy unseen; a path named twice fails too.
func policyRules(t *testing.T, hcl string) map[string][]string {
	t.Helper()
	var lines []string
	for l := range strings.SplitSeq(hcl, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines = append(lines, l)
		}
	}
	body := strings.Join(lines, "\n")
	rules := map[string][]string{}
	for _, m := range stanza.FindAllStringSubmatch(body, -1) {
		if _, dup := rules[m[1]]; dup {
			t.Fatalf("the policy names %q twice", m[1])
		}
		var caps []string
		for c := range strings.SplitSeq(m[2], ",") {
			if c = strings.Trim(strings.TrimSpace(c), `"`); c != "" {
				caps = append(caps, c)
			}
		}
		slices.Sort(caps)
		rules[m[1]] = caps
	}
	if rest := strings.TrimSpace(stanza.ReplaceAllString(body, "")); rest != "" {
		t.Fatalf("the policy holds text that is not a path stanza: %q", rest)
	}
	return rules
}

// readPolicy reads a policy's rules back from the provider as the administrator.
func readPolicy(t *testing.T, b *baotest.Bao, name string) string {
	t.Helper()
	status, body, err := b.Do(b.Admin(), http.MethodGet, "/v1/sys/policies/acl/"+b.Name(name), nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("reading policy %s back: status %d, %v", name, status, err)
	}
	var out struct {
		Data struct {
			Policy string `json:"policy"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Policy
}

func equalRules(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for p, c := range a {
		if !slices.Equal(c, b[p]) {
			return false
		}
	}
	return true
}

// TestLiveReportPolicyExact reads the orphan-report policy back from the provider and compares its
// rules with the one grant §6.4 allows; the token holds that policy and no other.
func TestLiveReportPolicyExact(t *testing.T) {
	b := live(t)
	got := policyRules(t, readPolicy(t, b, "bw-orphan-report"))
	if !equalRules(got, reportGrant) {
		t.Fatalf("the orphan-report policy as the provider holds it: %v, want %v", got, reportGrant)
	}
	t.Logf("the orphan-report policy read back: %v", got)

	tok := b.Token("bw-orphan-report")
	status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/auth/token/lookup", map[string]string{"token": tok})
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
	if !slices.Equal(look.Data.Policies, []string{b.Name("bw-orphan-report")}) {
		t.Fatalf("the token's policies: %q, want exactly the orphan-report policy", look.Data.Policies)
	}
	t.Logf("the token carries exactly %q", look.Data.Policies)

	// Control: the committed rules plus any one other stanza, written and read back the same way,
	// fail the comparison.
	committed, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "openbao", "policies", "bw-orphan-report.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	for name, extra := range map[string]string{
		"a value read":       `path "secret/data/gen/*" { capabilities = ["read"] }`,
		"another metadata":   `path "secret/metadata/access/*" { capabilities = ["list"] }`,
		"a transit decrypt":  `path "transit/decrypt/bw-staging" { capabilities = ["update"] }`,
		"a wider capability": `path "secret/metadata/gen/*" { capabilities = ["read", "list", "update"] }`,
	} {
		policy := "report-extra-" + strings.ReplaceAll(name, " ", "-")
		hcl := string(committed) + "\n" + extra + "\n"
		if name == "a wider capability" {
			hcl = extra + "\n"
		}
		b.Policy(policy, hcl)
		if equalRules(policyRules(t, readPolicy(t, b, policy)), reportGrant) {
			t.Fatalf("control %s: the comparison passed a policy with %s", name, extra)
		}
		t.Logf("control %s: the comparison fails", name)
	}
}

// report probes, as the report identity and against the shared test provider.
type probe struct {
	what  string
	grant string // the extra policy holding exactly the capability the probe needs
	call  func(tok string) int
}

// statusOf sends one request as tok and returns the status. Unlike raw it sets the content type a
// KV v2 PATCH needs, and returns 403 as a status rather than as ErrDenied.
func statusOf(t *testing.T, b *baotest.Bao, tok, method, path, contentType string, in any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequest(method, b.Addr+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Vault-Token", tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// seed writes one generation as the administrator and returns its path below the mount.
func seed(t *testing.T, b *baotest.Bao, cluster, claim string) string {
	t.Helper()
	p := "gen/" + cluster + "/" + claim + "/" + NewValueID()
	status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/secret/data/"+p, map[string]any{"data": map[string]string{"kind": "string", "value": "synthetic"}})
	if err != nil || status != http.StatusOK {
		t.Fatalf("seeding %s: status %d, %v: %s", p, status, err, body)
	}
	return p
}

// listKeys lists dir (below secret/metadata/) as tok and returns the names, failing unless 200.
func listKeys(t *testing.T, b *baotest.Bao, tok, dir string) []string {
	t.Helper()
	status, body, err := b.Do(tok, "LIST", "/v1/secret/metadata/"+dir, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("listing %s: status %d, %v", dir, status, err)
	}
	var out struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Keys
}

// TestLiveReportIdentity: the identity lists each level of the generation tree and reads a
// generation's metadata; every other request §16 names is refused, and each succeeds once the
// identity also holds the one capability it needs on that path alone.
func TestLiveReportIdentity(t *testing.T) {
	b := live(t)
	cl, claim := id.New(id.Cluster), id.New(id.Ingestion)
	p := seed(t, b, cl, claim)
	value := p[strings.LastIndexByte(p, '/')+1:]
	tok := b.Token("bw-orphan-report")

	if keys := listKeys(t, b, tok, "gen/"); !slices.Contains(keys, cl+"/") {
		t.Fatalf("listing gen/: %d names, not %s/", len(keys), cl)
	}
	if keys := listKeys(t, b, tok, "gen/"+cl+"/"); !slices.Equal(keys, []string{claim + "/"}) {
		t.Fatalf("listing the cluster: %q", keys)
	}
	if keys := listKeys(t, b, tok, "gen/"+cl+"/"+claim+"/"); !slices.Equal(keys, []string{value}) {
		t.Fatalf("listing the claim: %q", keys)
	}
	if s := statusOf(t, b, tok, http.MethodGet, "/v1/secret/metadata/"+p, "", nil); s != http.StatusOK {
		t.Fatalf("reading the generation's metadata: status %d", s)
	}
	t.Logf("the identity listed gen/, the cluster and the claim, and read the metadata")

	access := "access/talos/" + cl
	if status, _, err := b.Do(b.Admin(), http.MethodPost, "/v1/secret/data/"+access, map[string]any{"data": map[string]string{"talosconfig": "synthetic"}}); err != nil || status != http.StatusOK {
		t.Fatalf("seeding the access path: status %d, %v", status, err)
	}
	ct, err := encrypt(t, b, b.Admin(), "bw-staging", []byte("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	// disposable gives each change probe a path of its own, so a control's success changes
	// nothing another probe reads.
	disposable := func() string { return seed(t, b, cl, id.New(id.Ingestion)) }
	data := map[string]any{"data": map[string]string{"kind": "string", "value": "synthetic"}}
	versions := map[string]any{"versions": []int{1}}
	plain := map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte("synthetic"))}

	grants := map[string]string{
		"read-gen-data":      `path "secret/data/gen/*" { capabilities = ["read"] }`,
		"read-access-data":   `path "secret/data/access/talos/*" { capabilities = ["read"] }`,
		"list-access-meta":   `path "secret/metadata/access/*" { capabilities = ["list"] }`,
		"read-access-meta":   `path "secret/metadata/access/*" { capabilities = ["read"] }`,
		"encrypt-staging":    `path "transit/encrypt/bw-staging" { capabilities = ["update"] }`,
		"decrypt-staging-r":  `path "transit/decrypt/bw-staging" { capabilities = ["update"] }`,
		"create-gen":         `path "secret/data/gen/*" { capabilities = ["create"] }`,
		"update-gen":         `path "secret/data/gen/*" { capabilities = ["update"] }`,
		"patch-gen":          `path "secret/data/gen/*" { capabilities = ["patch"] }`,
		"delete-gen":         `path "secret/data/gen/*" { capabilities = ["delete"] }`,
		"update-gen-meta":    `path "secret/metadata/gen/*" { capabilities = ["update"] }`,
		"delete-gen-meta":    `path "secret/metadata/gen/*" { capabilities = ["delete"] }`,
		"update-gen-delete":  `path "secret/delete/gen/*" { capabilities = ["update"] }`,
		"update-gen-undel":   `path "secret/undelete/gen/*" { capabilities = ["update"] }`,
		"update-gen-destroy": `path "secret/destroy/gen/*" { capabilities = ["update"] }`,
	}
	for name, hcl := range grants {
		b.Policy(name, hcl)
	}
	probes := []probe{
		{"a secret/data/gen/* read", "read-gen-data", func(tok string) int {
			return statusOf(t, b, tok, http.MethodGet, "/v1/secret/data/"+p, "", nil)
		}},
		{"a secret/data/access/talos/* read", "read-access-data", func(tok string) int {
			return statusOf(t, b, tok, http.MethodGet, "/v1/secret/data/"+access, "", nil)
		}},
		{"a list of secret/metadata/access/", "list-access-meta", func(tok string) int {
			return statusOf(t, b, tok, "LIST", "/v1/secret/metadata/access/", "", nil)
		}},
		{"a read of an access path's metadata", "read-access-meta", func(tok string) int {
			return statusOf(t, b, tok, http.MethodGet, "/v1/secret/metadata/"+access, "", nil)
		}},
		{"a Transit encrypt", "encrypt-staging", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/transit/encrypt/bw-staging", "", plain)
		}},
		{"a Transit decrypt", "decrypt-staging-r", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/transit/decrypt/bw-staging", "", map[string]string{"ciphertext": string(ct)})
		}},
		{"a generation create", "create-gen", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/secret/data/gen/"+cl+"/"+id.New(id.Ingestion)+"/"+NewValueID(), "", data)
		}},
		{"a write to an existing generation", "update-gen", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/secret/data/"+disposable(), "", data)
		}},
		{"a generation PATCH", "patch-gen", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPatch, "/v1/secret/data/"+disposable(), "application/merge-patch+json", data)
		}},
		{"a DELETE of a generation's latest version", "delete-gen", func(tok string) int {
			return statusOf(t, b, tok, http.MethodDelete, "/v1/secret/data/"+disposable(), "", nil)
		}},
		{"a generation metadata update", "update-gen-meta", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/secret/metadata/"+disposable(), "", map[string]any{"custom_metadata": map[string]string{"k": "v"}})
		}},
		{"a generation metadata delete", "delete-gen-meta", func(tok string) int {
			return statusOf(t, b, tok, http.MethodDelete, "/v1/secret/metadata/"+disposable(), "", nil)
		}},
		{"a secret/delete/gen/* request", "update-gen-delete", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/secret/delete/"+disposable(), "", versions)
		}},
		{"a secret/undelete/gen/* request", "update-gen-undel", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPost, "/v1/secret/undelete/"+disposable(), "", versions)
		}},
		{"a secret/destroy/gen/* request", "update-gen-destroy", func(tok string) int {
			return statusOf(t, b, tok, http.MethodPut, "/v1/secret/destroy/"+disposable(), "", versions)
		}},
	}
	for _, pr := range probes {
		if s := pr.call(tok); s != http.StatusForbidden {
			t.Fatalf("%s as the orphan-report identity: status %d, want 403", pr.what, s)
		}
		if s := pr.call(b.Token("bw-orphan-report", pr.grant)); s/100 != 2 {
			t.Fatalf("control: %s with %s added: status %d, want 2xx", pr.what, pr.grant, s)
		}
		t.Logf("refused: %s; control: succeeds with %s added", pr.what, pr.grant)
	}
}

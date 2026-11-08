package ingest

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// docOfKind is the index of the first document of the kind, or -1.
func docOfKind(docs []*yaml.Node, kind string) int {
	for i, d := range docs {
		if docKind(d) == kind {
			return i
		}
	}
	return -1
}

// Talos v1.14 moved three secrets out of the paths a v1.13 configuration holds them at. The secretbox
// key left the v1alpha1 document for KubeEtcdEncryptionConfig, the service account key left it for
// KubeServiceAccountConfig, and the generated stream now holds 29 documents, so the WireguardConfig
// that was doc[2] is doc[28]. Extract takes its schema secrets from the machinery, so this test shows
// that our own sanitizer removes each of them from the text: it reads the value from the input,
// runs Extract and fails when the value is still in the sanitized text. Two fields the machinery
// redacted before and still redacts are controls: they show that the read finds a value at all.
func TestExtractRedactsTheV114SecretFields(t *testing.T) {
	stream := generatedStream(t)
	docs, err := parseStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	fields := []struct{ kind, pointer string }{
		{"KubeEtcdEncryptionConfig", "config/resources/0/providers/0/secretbox/keys/0/secret"},
		{"KubeServiceAccountConfig", "issuer/privateKey"},
		{"WireguardConfig", "privateKey"},
		{"WireguardConfig", "peers/0/presharedKey"},
		{"v1alpha1", "machine/ca/key"}, // control
		{"v1alpha1", "machine/token"},  // control
	}
	var values []string
	for _, f := range fields {
		i := docOfKind(docs, f.kind)
		if i < 0 {
			t.Fatalf("control: the generated stream holds no %s document", f.kind)
		}
		n, ok := resolve(docs, i, strings.Split(f.pointer, "/"))
		if !ok || len(n.Value) < 16 {
			t.Fatalf("control: the generated stream holds no value at %s %s", f.kind, f.pointer)
		}
		values = append(values, n.Value)
	}
	c, err := Extract(Request{Input: unresolved(string(stream))})
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range fields {
		if bytes.Contains(c.docs, []byte(values[i])) {
			t.Errorf("the sanitized text still holds the value at %s %s", f.kind, f.pointer)
		}
	}
	if len(c.values) < len(fields) {
		t.Errorf("control: %d values extracted, want at least %d", len(c.values), len(fields))
	}
}

// A v1alpha1 document that holds the v1.13 locations of the two moved fields still loads, and Extract
// still removes both values from the text.
func TestExtractRedactsTheV113SecretLocations(t *testing.T) {
	// Built at run time: a literal base64 value here reads as a credential to a secret scanner.
	secretbox := base64.StdEncoding.EncodeToString([]byte("synthetic-secretbox-encryption!!"))
	svcKey := base64.StdEncoding.EncodeToString([]byte("synthetic-service-account-key-pem, not real"))
	text := "version: v1alpha1\nmachine:\n  type: controlplane\n  token: aaaaaa.bbbbbbbbbbbbbbbb\n" +
		"cluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443\n" +
		"  secretboxEncryptionSecret: " + secretbox + "\n  serviceAccount:\n    key: " + svcKey + "\n"
	c, err := Extract(Request{Input: unresolved(text)})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{secretbox, svcKey, "aaaaaa.bbbbbbbbbbbbbbbb"} {
		if bytes.Contains(c.docs, []byte(v)) {
			t.Errorf("the sanitized text still holds %s", v)
		}
	}
}

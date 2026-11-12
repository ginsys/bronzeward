package discover

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
)

// twoContexts is a talosconfig of two contexts, each with its own certificate authority, client
// certificate, key, endpoint and node. Context a is the current one.
func twoContexts(a, b talostest.PKI, aEndpoint, bEndpoint string) []byte {
	b64 := base64.StdEncoding.EncodeToString
	entry := func(name string, p talostest.PKI, endpoint string) string {
		return "  " + name + ":\n" +
			"    endpoints: [" + endpoint + "]\n" +
			"    nodes: [10.9.9.9]\n" +
			"    ca: " + b64(p.CAPEM) + "\n" +
			"    crt: " + b64(p.CrtPEM) + "\n" +
			"    key: " + b64(p.KeyPEM) + "\n"
	}
	return []byte("context: a\ncontexts:\n" + entry("a", a, aEndpoint) + entry("b", b, bEndpoint))
}

// TestDiscoverUsesTheNamedContext: without a name the current context's credentials and first endpoint
// are used, and with a name only that context's. Each cluster accepts only its own certificate
// authority, so an answer proves which context dialled.
func TestDiscoverUsesTheNamedContext(t *testing.T) {
	ca, cb := newCluster(t), newCluster(t)
	ca.a.SetClusterID(t, "c-a")
	cb.a.SetClusterID(t, "c-b")
	tc := twoContexts(ca.p, cb.p, ca.a.Endpoint, cb.a.Endpoint)
	for name, want := range map[string]string{"": "c-a", "a": "c-a", "b": "c-b"} {
		got, err := Discover(ctx20(t), tc, name, "")
		if err != nil || got.ClusterID != want {
			t.Errorf("context %q: cluster %q, %v, want %q", name, got.ClusterID, err, want)
		}
	}
}

// TestDiscoverUnknownContext: a name that the file does not hold is refused before any dial, with a
// mark the caller can test. The endpoint has no listener, so a dial would give another error.
func TestDiscoverUnknownContext(t *testing.T) {
	c := newCluster(t)
	tc := twoContexts(c.p, c.p, c.closed, c.closed)
	for _, name := range []string{"c", "A", " b"} {
		if _, err := Discover(ctx20(t), tc, name, c.closed); !errors.Is(err, ErrUnknownContext) {
			t.Errorf("context %q: %v, want ErrUnknownContext", name, err)
		}
	}
}

// TestOneContext: the document holds the chosen context and no other, with its certificate authority,
// certificate and key and nothing else. The key of the other context is in no byte of it, and the
// result is a talosconfig that the machinery reads back.
func TestOneContext(t *testing.T) {
	pa, pb := talostest.NewPKI(t), talostest.NewPKI(t)
	tc := twoContexts(pa, pb, "10.0.0.1:50000", "10.0.0.2:50000")
	for name, p := range map[string]talostest.PKI{"": pa, "a": pa, "b": pb} {
		other := pb
		if p.KeyPEM != nil && bytes.Equal(p.KeyPEM, pb.KeyPEM) {
			other = pa
		}
		out, err := OneContext(tc, name)
		if err != nil {
			t.Fatalf("context %q: %v", name, err)
		}
		for _, planted := range [][]byte{other.KeyPEM, []byte(base64.StdEncoding.EncodeToString(other.KeyPEM)), other.CrtPEM, []byte(base64.StdEncoding.EncodeToString(other.CAPEM))} {
			if bytes.Contains(out, planted) {
				t.Errorf("context %q: the document holds a value of the other context", name)
			}
		}
		cfg, err := clientconfig.FromBytes(out)
		if err != nil {
			t.Fatalf("context %q: the document is not a talosconfig: %v", name, err)
		}
		want := name
		if want == "" {
			want = "a"
		}
		got := cfg.Contexts[want]
		if cfg.Context != want || len(cfg.Contexts) != 1 || got == nil {
			t.Fatalf("context %q: the document has current %q and %d contexts", name, cfg.Context, len(cfg.Contexts))
		}
		b64 := base64.StdEncoding.EncodeToString
		if got.CA != b64(p.CAPEM) || got.Crt != b64(p.CrtPEM) || got.Key != b64(p.KeyPEM) {
			t.Errorf("context %q: the certificate authority, certificate or key is not the chosen context's", name)
		}
		if len(got.Endpoints) != 0 || len(got.Nodes) != 0 || got.Auth.Basic != nil || got.Auth.SideroV1 != nil || strings.Contains(string(out), "10.9.9.9") {
			t.Errorf("context %q: the document keeps an endpoint, a node or an auth block", name)
		}
	}
}

// TestOneContextRefusals: a name the file does not hold has the mark, a context without its
// credentials or a file that is no talosconfig has a plain error, and no error holds the document.
func TestOneContextRefusals(t *testing.T) {
	pa, pb := talostest.NewPKI(t), talostest.NewPKI(t)
	tc := twoContexts(pa, pb, "10.0.0.1:50000", "10.0.0.2:50000")
	if _, err := OneContext(tc, "c"); !errors.Is(err, ErrUnknownContext) {
		t.Errorf("an unknown name: %v, want ErrUnknownContext", err)
	}
	for name, doc := range map[string][]byte{
		"no key":            bytes.ReplaceAll(tc, []byte("    key:"), []byte("    kex:")),
		"not a talosconfig": []byte("not: [a talosconfig"),
		"a null context":    []byte("context: a\ncontexts:\n  a:\n"),
	} {
		_, err := OneContext(doc, "")
		if err == nil || errors.Is(err, ErrUnknownContext) {
			t.Errorf("%s: %v, want a plain error", name, err)
			continue
		}
		for _, leak := range leaked(err.Error(), pa) {
			t.Errorf("%s: the error holds %s", name, leak)
		}
	}
}

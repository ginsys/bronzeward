package provider

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// executorStandIn is an Executor pointed at handler, with the recorder standIn fills.
func executorStandIn(t *testing.T, handler http.HandlerFunc) (*Executor, *recorder) {
	t.Helper()
	i, rec := standIn(t, testKeys, handler)
	e, err := NewExecutor(i.c.base, tokenOf(testToken), testArtifactKey)
	if err != nil {
		t.Fatal(err)
	}
	return e, rec
}

// The executor reads a cluster's Talos access as ingestion does (persistence-api.md §3.3): the same
// request under its own token, the same version identity and the same typed outcomes.
func TestExecutorTalosAccess(t *testing.T) {
	cl := id.New(id.Cluster)
	e, rec := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, kvRead(map[string]any{"talosconfig": talosconfigText}, kvMetadata(4, "2026-10-08T10:11:12Z")))
	})
	a, err := e.TalosAccess(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.last()
	if got.method != http.MethodGet || got.path != "/v1/secret/data/access/talos/"+cl || got.token != testToken || len(got.body) != 0 {
		t.Fatalf("request %s %s, token %q, body %q", got.method, got.path, got.token, got.body)
	}
	if v := a.Version(); v.Path != "access/talos/"+cl || v.Version != 4 || v.CreatedTime.IsZero() || string(a.Talosconfig()) != talosconfigText {
		t.Fatalf("version %+v", v)
	}
	if _, err := e.TalosAccess(t.Context(), "cl_../../sys/raw"); err == nil || len(rec.all()) != 1 {
		t.Fatalf("a malformed cluster: %v after %d requests", err, len(rec.all()))
	}

	for status, want := range map[int]error{404: ErrAbsent, 403: ErrDenied} {
		e, _ := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, status, map[string]any{"errors": []string{}})
		})
		if _, err := e.TalosAccess(t.Context(), cl); !errors.Is(err, want) {
			t.Errorf("status %d: %v, want %v", status, err, want)
		}
	}
}

// The role type has exactly the executor's operations: the artifact key it decrypts under, that
// decryption and the Talos access read (compilation.md §1).
func TestExecutorMethodSet(t *testing.T) {
	want := []string{"ArtifactKey", "DecryptArtifact", "TalosAccess"}
	if got := methodNames(reflect.TypeFor[*Executor]()); !slices.Equal(got, want) {
		t.Fatalf("*Executor exports %q, want exactly %q", got, want)
	}
}

// An artifact has no method that yields its plaintext, so no interface assertion on it can: its
// methods are the placeholder renderers and the refusing marshallers only. The control asserts the
// one a caller would reach for.
func TestArtifactExposesNoPlaintextMethod(t *testing.T) {
	want := []string{"Format", "GoString", "MarshalJSON", "MarshalText", "MarshalYAML", "String"}
	if got := methodNames(reflect.TypeFor[Artifact]()); !slices.Equal(got, want) {
		t.Fatalf("Artifact exports %q, want exactly %q", got, want)
	}
	var a any = Artifact{}
	if _, ok := a.(interface{ Bytes() []byte }); ok {
		t.Fatal("an artifact satisfies interface{ Bytes() []byte }")
	}
	if _, ok := a.(interface{ Plaintext() []byte }); ok {
		t.Fatal("an artifact satisfies interface{ Plaintext() []byte }")
	}
}

const artifactText = "machine:\n  token: artifact-secret-9Zq\n"

// artifactCiphertext is a well-formed Transit ciphertext at key version 3.
var artifactCiphertext = Ciphertext("vault:v3:" + base64.StdEncoding.EncodeToString([]byte("sealed")))

// The executor decrypts an artifact under the artifact key, with its own token, at the version the
// ciphertext names, and hands it back as a value that does not render (execution-recovery.md §3.1
// item 2, §3.5).
func TestExecutorDecryptArtifact(t *testing.T) {
	e, rec := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"plaintext": base64.StdEncoding.EncodeToString([]byte(artifactText))}))
	})
	if e.ArtifactKey() != testArtifactKey {
		t.Fatalf("ArtifactKey() = %q, want %q", e.ArtifactKey(), testArtifactKey)
	}
	a, err := e.DecryptArtifact(t.Context(), artifactCiphertext, 3)
	if err != nil {
		t.Fatal(err)
	}
	q := rec.last()
	var body struct{ Ciphertext string }
	if err := json.Unmarshal(q.body, &body); err != nil {
		t.Fatal(err)
	}
	if q.method != http.MethodPost || q.path != "/v1/transit/decrypt/"+testArtifactKey || q.token != testToken ||
		body.Ciphertext != string(artifactCiphertext) {
		t.Fatalf("sent %s %s as %q: %q", q.method, q.path, q.token, q.body)
	}
	if string(ArtifactBytes(a)) != artifactText {
		t.Fatal("the decrypted artifact is not the plaintext")
	}
	for _, s := range []string{fmt.Sprintf("%v", a), fmt.Sprintf("%+v", a), fmt.Sprintf("%#v", a), fmt.Sprintf("%s", a),
		fmt.Sprintf("%q", a), fmt.Sprint(&a)} {
		if strings.Contains(s, "artifact-secret") {
			t.Fatalf("formatted artifact %q holds the plaintext", s)
		}
	}
	if _, err := json.Marshal(a); err == nil {
		t.Fatal("an artifact marshals to JSON")
	}
	if _, err := json.Marshal(struct{ A Artifact }{a}); err == nil {
		t.Fatal("an artifact in a struct marshals to JSON")
	}
}

// A decrypted artifact does not render when it is the unexported field of a caller's own type, where
// fmt cannot call its Format method and walks it by reflection: under every verb, in a value, a
// pointer, a slice, a map and a struct nested in a struct, whatever the verb, the output carries
// neither the plaintext nor its decimal or hexadecimal bytes. The control proves the forms searched
// for are the ones fmt renders a byte slice in.
func TestArtifactDoesNotRenderThroughAnUnexportedField(t *testing.T) {
	e, _ := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"plaintext": base64.StdEncoding.EncodeToString([]byte(artifactText))}))
	})
	a, err := e.DecryptArtifact(t.Context(), artifactCiphertext, 3)
	if err != nil {
		t.Fatal(err)
	}
	head := []byte(artifactText)[:12]
	forbidden := []string{
		string([]byte(artifactText)[:12]),
		strings.TrimSuffix(fmt.Sprintf("%d", head), "]"),
		fmt.Sprintf("%x", head),
		fmt.Sprintf("%X", head),
		fmt.Sprintf("% x", head),
		base64.StdEncoding.EncodeToString(head)[:12],
	}
	// Control: a bare byte slice, its pointer and a struct holding it do render in those forms.
	plain := []byte(artifactText)
	for _, v := range []any{plain, &plain, struct{ b []byte }{plain}, struct{ b *[]byte }{&plain}} {
		var hit bool
		for _, verb := range []string{"%v", "%s", "%q", "%x", "%+v"} {
			s := fmt.Sprintf(verb, v)
			for _, f := range forbidden {
				hit = hit || strings.Contains(s, f)
			}
		}
		if !hit {
			t.Fatalf("control: %T renders none of the forbidden forms", v)
		}
	}
	type inner struct{ held Artifact }
	type outer struct {
		n int
		i inner
	}
	values := map[string]any{
		"struct":          struct{ a Artifact }{a},
		"pointer":         &struct{ a Artifact }{a},
		"nested":          outer{1, inner{a}},
		"nested pointer":  &outer{1, inner{a}},
		"slice of struct": []struct{ a Artifact }{{a}},
		"map of struct":   map[string]struct{ a Artifact }{"k": {a}},
		"array":           [1]struct{ a Artifact }{{a}},
		"interface field": struct{ a any }{a},
		"error wrapper":   fmt.Errorf("apply: %v", struct{ a Artifact }{a}),
	}
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "% x", "%d", "%t", "%c", "%U", "%e", "%p", "%T", "%10s", "%-8q"}
	for name, v := range values {
		for _, verb := range verbs {
			s := fmt.Sprintf(verb, v)
			for _, f := range forbidden {
				if strings.Contains(s, f) {
					t.Errorf("%s under %s renders %q: %.80q", name, verb, f, s)
				}
			}
		}
		for _, s := range []string{fmt.Sprint(v), fmt.Sprintln(v), fmt.Sprintf("%v", []any{v})} {
			for _, f := range forbidden {
				if strings.Contains(s, f) {
					t.Errorf("%s renders %q: %.80q", name, f, s)
				}
			}
		}
	}
}

// A ciphertext naming another key version than the bound one, or none, is refused before any
// request: the use-time check is of the bound version (execution-recovery.md §3.1 item 2).
func TestExecutorDecryptArtifactRefuses(t *testing.T) {
	e, rec := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"plaintext": base64.StdEncoding.EncodeToString([]byte(artifactText))}))
	})
	if _, err := e.DecryptArtifact(t.Context(), artifactCiphertext, 2); err == nil {
		t.Error("a ciphertext at version 3 decrypted as the bound version 2")
	}
	if _, err := e.DecryptArtifact(t.Context(), "vault:v3:", 3); err == nil {
		t.Error("a ciphertext with no payload decrypted")
	}
	if _, err := e.DecryptArtifact(t.Context(), "not a ciphertext", 3); err == nil {
		t.Error("garbage decrypted")
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d requests sent for a ciphertext refused before any", n)
	}
}

// Every failure is a typed outcome that quotes neither the plaintext nor the ciphertext; a sealed or
// stopped provider is unavailable, which is neither a refusal nor a verdict on the artifact.
func TestExecutorDecryptArtifactOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   any
		want   error
	}{
		{"denied", 403, map[string]any{"errors": []string{artifactText}}, ErrDenied},
		{"sealed", 503, map[string]any{"errors": []string{"Vault is sealed"}}, ErrUnavailable},
		{"limited", 429, map[string]any{"errors": []string{}}, ErrUnavailable},
		{"refused", 400, map[string]any{"errors": []string{"invalid ciphertext " + artifactText}}, ErrStatus},
		{"not base64", 200, data(map[string]any{"plaintext": "!!" + artifactText}), ErrProtocol},
		{"no plaintext", 200, data(map[string]any{}), ErrProtocol},
		{"not json", 200, "x", ErrProtocol},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, c.status, c.body) })
			_, err := e.DecryptArtifact(t.Context(), artifactCiphertext, 3)
			if !errors.Is(err, c.want) {
				t.Fatalf("%v, want %v", err, c.want)
			}
			if s := err.Error(); strings.Contains(s, "artifact-secret") || strings.Contains(s, string(artifactCiphertext)) {
				t.Fatalf("the error quotes what was sent or received: %s", s)
			}
		})
	}
	// A provider that is not there is unavailable too.
	e, err := NewExecutor("http://127.0.0.1:1", tokenOf(testToken), testArtifactKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.DecryptArtifact(t.Context(), artifactCiphertext, 3); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a provider that is not there: %v, want %v", err, ErrUnavailable)
	}
}

func TestNewExecutorRefuses(t *testing.T) {
	for _, key := range []string{"", "..", "a/b", "a%2f"} {
		if _, err := NewExecutor("http://127.0.0.1:1", tokenOf(testToken), key); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
	if _, err := NewExecutor("http://127.0.0.1:1", Token{}, testArtifactKey); err == nil {
		t.Error("no token accepted")
	}
}

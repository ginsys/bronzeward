package ingest

import (
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// An alias of a container that holds a restored reference is a place of its own: the value inside it
// is restored only where the schema holds a secret at the alias path, as for an alias of the
// reference itself (ginsys/bronzeward#739).
func TestTraceKeepingTestsEachAliasOfAContainer(t *testing.T) {
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	values := map[string]provider.Value{"app/str": value(t, provider.KindString, resolveSecret)}
	const auth = "      reg.test:\n        auth: &auth\n          password: !bwref app/str\n"
	trace := func(t *testing.T, text string, secret ...Path) map[string]any {
		t.Helper()
		r, _, _, err := TraceKeeping(sanitizedOf(t, text, decl), values, 0, -1, SecretLeaves{paths: secret})
		if err != nil {
			t.Fatal(err)
		}
		return decodeStream(t, string(r.bytes()))[0]
	}
	password := func(doc map[string]any, key string) string {
		s, _ := doc["machine"].(map[string]any)[key].(map[string]any)["password"].(string)
		return s
	}
	home := Path{Doc: 0, Pointer: []string{"machine", "registries", "config", "reg.test", "auth", "password"}}
	head := "machine:\n  registries:\n    config:\n" + auth

	t.Run("an alias in a field that is no secret keeps the stand-in", func(t *testing.T) {
		doc := trace(t, head+"  nodeLabels: *auth\n", home)
		got := doc["machine"].(map[string]any)["registries"].(map[string]any)["config"].(map[string]any)["reg.test"].(map[string]any)["auth"].(map[string]any)["password"]
		if got != resolveSecret {
			t.Errorf("the secret place = %v, want the real value", got)
		}
		if s := password(doc, "nodeLabels"); s == "" || strings.Contains(s, resolveSecret) {
			t.Errorf("the alias holds %q, want a stand-in", s)
		}
	})
	t.Run("an alias in a place that holds a secret is restored", func(t *testing.T) {
		doc := trace(t, head+"  nodeLabels: *auth\n", home, Path{Doc: 0, Pointer: []string{"machine", "nodeLabels", "password"}})
		if s := password(doc, "nodeLabels"); s != resolveSecret {
			t.Errorf("the alias holds %q, want the real value", s)
		}
	})
	t.Run("an alias of a container inside a container", func(t *testing.T) {
		doc := trace(t, head+"  kubelet:\n    extraConfig:\n      first: &outer\n        inner: *auth\n      second: *outer\n", home)
		extra := doc["machine"].(map[string]any)["kubelet"].(map[string]any)["extraConfig"].(map[string]any)
		for _, key := range []string{"first", "second"} {
			inner := extra[key].(map[string]any)["inner"].(map[string]any)["password"].(string)
			if inner == "" || strings.Contains(inner, resolveSecret) {
				t.Errorf("%s.inner.password = %q, want a stand-in", key, inner)
			}
		}
	})
	t.Run("a short secret at an alias of a container is unmarkable", func(t *testing.T) {
		const short = "qz9!"
		short9 := map[string]provider.Value{"app/str": value(t, provider.KindString, short)}
		r, ts, _, err := TraceKeeping(sanitizedOf(t, head+"  nodeLabels: *auth\n", decl), short9, 0, -1,
			SecretLeaves{paths: []Path{{Doc: 0, Pointer: []string{"machine", "nodeLabels", "password"}}}})
		if err != nil {
			t.Fatal(err)
		}
		doc := decodeStream(t, string(r.bytes()))[0]
		if s := password(doc, "nodeLabels"); s != short {
			t.Fatalf("the alias holds %q, want the real value", s)
		}
		// No tracer stands at the alias and the value check ignores a secret this short, so the pass
		// must tell the message check that the real value is held there.
		if len(ts) != 1 || !ts[0].Unmarkable(6) {
			t.Errorf("the tracer is not unmarkable, so a message could quote the secret")
		}
	})
	// A literal alias inside a copied container keeps its target. The copy is written and parsed
	// again, so an alias left as a name would bind to the nearest earlier anchor of that name, which
	// here is the anchor of a restored secret.
	t.Run("a literal alias inside the copy keeps its target", func(t *testing.T) {
		both := Declarations{References: map[string]Reference{"app/str": str(provider.KindString), "app/token": str(provider.KindString)}}
		vals := map[string]provider.Value{
			"app/str":   value(t, provider.KindString, "some-value"),
			"app/token": value(t, provider.KindString, "token-secret"),
		}
		text := "machine:\n  kubelet:\n    extraConfig: &outer\n      ref: !bwref app/str\n      literal: &label old\n      echo: *label\n  token: &label !bwref app/token\n  sysctls: *outer\n"
		r, _, _, err := TraceKeeping(sanitizedOf(t, text, both), vals, 0, -1,
			SecretLeaves{paths: []Path{{Doc: 0, Pointer: []string{"machine", "token"}}}})
		if err != nil {
			t.Fatal(err)
		}
		m := decodeStream(t, string(r.bytes()))[0]["machine"].(map[string]any)
		if got := m["sysctls"].(map[string]any)["echo"]; got != "old" {
			t.Errorf("sysctls.echo = %v, want old", got)
		}
		if got := m["token"]; got != "token-secret" {
			t.Errorf("the secret place = %v, want the real value", got)
		}
	})
	// An alias used as a mapping key is copied by the same rule as a value, but no input reaches that
	// branch: a document that holds a reference and an alias key does not load with the machinery,
	// which refuses a key that is an alias ("unexpected mapping key type"). Extract refuses the
	// input as schema-unloadable before any trace pass, whether or not the key's anchor name is
	// reused on a reference.
	t.Run("an alias key beside a reference is refused before the trace", func(t *testing.T) {
		for name, text := range map[string]string{
			"anchor name reused on a reference": "machine:\n  kubelet:\n    extraConfig: &outer\n      ref: !bwref app/str\n      literal: &label old\n      ? *label\n      : value\n  token: &label !bwref app/str\n  sysctls: *outer\n",
			"anchor name used once":             "machine:\n  kubelet:\n    extraConfig: &outer\n      ref: !bwref app/str\n      literal: &label old\n      ? *label\n      : value\n  sysctls: *outer\n",
		} {
			req := request(t, text)
			req.Declarations = decl
			if _, err := Extract(req); !isRule(err, RuleSchemaUnloadable) {
				t.Errorf("%s: %v, want a %s refusal", name, err, RuleSchemaUnloadable)
			}
		}
	})
	// An alias inside the copy whose target is a restored reference is a place of its own at the new
	// path, restored only where the schema holds a secret there.
	t.Run("a reference alias inside the copy is restored only at a secret path", func(t *testing.T) {
		both := Declarations{References: map[string]Reference{"app/str": str(provider.KindString), "app/token": str(provider.KindString)}}
		vals := map[string]provider.Value{
			"app/str":   value(t, provider.KindString, "some-value"),
			"app/token": value(t, provider.KindString, "token-secret"),
		}
		text := "machine:\n  token: &tok !bwref app/token\n  kubelet:\n    extraConfig: &outer\n      ref: !bwref app/str\n      echo: *tok\n  sysctls: *outer\n"
		echo := func(secret ...Path) (home, copied, kept any) {
			r, _, _, err := TraceKeeping(sanitizedOf(t, text, both), vals, 0, -1, SecretLeaves{paths: secret})
			if err != nil {
				t.Fatal(err)
			}
			m := decodeStream(t, string(r.bytes()))[0]["machine"].(map[string]any)
			return m["kubelet"].(map[string]any)["extraConfig"].(map[string]any)["echo"], m["sysctls"].(map[string]any)["echo"], m["token"]
		}
		token := Path{Doc: 0, Pointer: []string{"machine", "token"}}
		home, copied, kept := echo(token)
		if kept != "token-secret" || home == "token-secret" || copied == "token-secret" {
			t.Errorf("token %v, kubelet echo %v, sysctls echo %v: want the real value at the token only", kept, home, copied)
		}
		_, copied, _ = echo(token, Path{Doc: 0, Pointer: []string{"machine", "sysctls", "echo"}})
		if copied != "token-secret" {
			t.Errorf("sysctls.echo = %v, want the real value at its own secret path", copied)
		}
	})
	// The copy follows an alias to its target, so it would not end on an anchor that holds an alias
	// of itself. The parse stage refuses that input before any trace pass (cyclicAlias).
	t.Run("a self-referencing anchor is refused before the trace", func(t *testing.T) {
		text := "machine:\n  kubelet:\n    extraConfig: &outer\n      ref: !bwref app/str\n      loop: &a [ *a ]\n  sysctls: *outer\n"
		req := request(t, text)
		req.Declarations = decl
		if _, err := Extract(req); !isRule(err, RuleParse) {
			t.Errorf("%v, want a %s refusal", err, RuleParse)
		}
	})
	t.Run("an alias of a sequence", func(t *testing.T) {
		text := "machine:\n  certSANs: &sans [!bwref app/str]\ncluster:\n  apiServer:\n    certSANs: *sans\n"
		doc := trace(t, text, Path{Doc: 0, Pointer: []string{"machine", "certSANs", "0"}})
		first := doc["machine"].(map[string]any)["certSANs"].([]any)[0]
		other, _ := doc["cluster"].(map[string]any)["apiServer"].(map[string]any)["certSANs"].([]any)[0].(string)
		if first != resolveSecret {
			t.Errorf("the secret place = %v, want the real value", first)
		}
		if other == "" || strings.Contains(other, resolveSecret) {
			t.Errorf("the alias holds %q, want a stand-in", other)
		}
	})
}

package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCreateGenerationSendsCASZero(t *testing.T) {
	i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"version": 1}))
	})
	p := newPath(t)
	g, err := i.CreateGeneration(t.Context(), p, mustValue(t, KindMapping, map[string]any{"user": "u", "port": 5432, "tls": true}))
	if err != nil {
		t.Fatal(err)
	}
	if g.Path != p || g.Version != 1 {
		t.Fatalf("Generation = %+v", g)
	}
	got := rec.last()
	if got.method != http.MethodPost || got.path != "/v1/secret/data/"+p.String() || got.token != testToken {
		t.Fatalf("request %s %s token %q", got.method, got.path, got.token)
	}
	var body struct {
		Options map[string]json.RawMessage `json:"options"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(got.body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("body %s: %v", got.body, err)
	}
	if cas, ok := body.Options["cas"]; !ok || string(cas) != "0" || len(body.Options) != 1 {
		t.Fatalf("options = %s, want exactly {\"cas\":0} with cas a number", got.body)
	}
	if len(body.Data) != 2 || string(body.Data["kind"]) != `"mapping"` {
		t.Fatalf("data = %s, want exactly kind and value", got.body)
	}
	var value map[string]any
	if err := json.Unmarshal(body.Data["value"], &value); err != nil || value["user"] != "u" || value["port"] != 5432.0 || value["tls"] != true {
		t.Fatalf("value = %s", body.Data["value"])
	}
}

// A cas=0 create can only produce version 1; any other answer is a provider this client does not
// understand, never a generation to record.
func TestCreateGenerationRefusesVersionNotOne(t *testing.T) {
	for _, v := range []int{0, 2, -1} {
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, 200, data(map[string]any{"version": v}))
		})
		g, err := i.CreateGeneration(t.Context(), newPath(t), mustValue(t, KindString, "x"))
		if !errors.Is(err, ErrProtocol) {
			t.Errorf("version %d: %+v, %v; want ErrProtocol", v, g, err)
		}
	}
}

func TestCreateGenerationRefusesUnchecked(t *testing.T) {
	i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"version": 1}))
	})
	raw := func(k Kind, s string) Value { return Value{&value{kind: k, json: json.RawMessage(s)}} }
	for name, c := range map[string]struct {
		p GenerationPath
		v Value
	}{
		"zero path":        {GenerationPath{}, mustValue(t, KindString, "x")},
		"zero value":       {newPath(t), Value{}},
		"kind mismatch":    {newPath(t), raw(KindInteger, `"x"`)},
		"unknown kind":     {newPath(t), raw("list", `[1]`)},
		"invalid UTF-8":    {newPath(t), raw(KindString, "\"a\xffb\"")},
		"trailing data":    {newPath(t), raw(KindString, `"a" "b"`)},
		"nested mapping":   {newPath(t), raw(KindMapping, `{"a":{"b":1}}`)},
		"float":            {newPath(t), raw(KindInteger, `1.5`)},
		"null":             {newPath(t), raw(KindString, `null`)},
		"duplicate member": {newPath(t), raw(KindMapping, `{"a":1,"a":2}`)},
	} {
		if _, err := i.CreateGeneration(t.Context(), c.p, c.v); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("%d request(s) sent for refused inputs", n)
	}
}

func TestNewValueKinds(t *testing.T) {
	for _, c := range []struct {
		k    Kind
		v    any
		want string
	}{
		{KindString, "s3cret", `"s3cret"`},
		{KindString, "", `""`},
		{KindInteger, 42, `42`},
		{KindInteger, int64(math.MinInt64), `-9223372036854775808`},
		{KindInteger, uint64(math.MaxUint64), `18446744073709551615`},
		{KindInteger, json.Number("-7"), `-7`},
		{KindBoolean, false, `false`},
		{KindMapping, map[string]any{"a": "x", "b": 1, "c": true}, `{"a":"x","b":1,"c":true}`},
		{KindMapping, map[string]string{"a": "x"}, `{"a":"x"}`},
		{KindMapping, map[string]any{}, `{}`},
	} {
		got, err := NewValue(c.k, c.v)
		if err != nil {
			t.Errorf("NewValue(%s, %T): %v", c.k, c.v, err)
			continue
		}
		if got.Kind() != c.k || string(got.p.json) != c.want {
			t.Errorf("NewValue(%s, %T) = %s %s, want %s", c.k, c.v, got.Kind(), got.p.json, c.want)
		}
	}
	for _, c := range []struct {
		name string
		k    Kind
		v    any
	}{
		{"kind mismatch", KindInteger, "1"},
		{"bool as string", KindString, true},
		{"float", KindInteger, 1.0},
		{"float32", KindInteger, float32(1)},
		{"non-integer number", KindInteger, json.Number("1.0")},
		{"exponent", KindInteger, json.Number("1e3")},
		{"null", KindString, nil},
		{"list", KindMapping, []any{"a"}},
		{"string list", KindString, []string{"a"}},
		{"nested mapping", KindMapping, map[string]any{"a": map[string]any{"b": 1}}},
		{"null member", KindMapping, map[string]any{"a": nil}},
		{"float member", KindMapping, map[string]any{"a": 1.5}},
		{"invalid UTF-8", KindString, "a\xffb"},
		{"invalid UTF-8 key", KindMapping, map[string]any{"a\xff": "b"}},
		{"invalid UTF-8 member", KindMapping, map[string]string{"a": "\xff"}},
		{"unknown kind", "list", "a"},
	} {
		if got, err := NewValue(c.k, c.v); err == nil {
			t.Errorf("%s: NewValue accepted it as %s", c.name, got.p.json)
		} else if strings.Contains(err.Error(), "a\xffb") {
			t.Errorf("%s: the error quotes the value: %v", c.name, err)
		}
	}
}

// TestValueDoesNotRender covers the generic sinks a caller can hand a Value to: fmt (where a
// containing struct's unexported field is printed by reflection, which no method intercepts),
// encoding/json, YAML and both slog handlers. A Token held the same way is checked alongside.
func TestValueDoesNotRender(t *testing.T) {
	v := mustValue(t, KindString, plaintext)
	tok := Token{p: new(plaintext)}
	type nested struct {
		v   Value
		tok Token
	}
	type exported struct {
		V   Value
		Tok Token
	}
	outputs := map[string]string{}
	for _, x := range []any{v, &v, tok, nested{v, tok}, exported{v, tok}, []Value{v}, map[string]Value{"k": v}} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			outputs[fmt.Sprintf("%s of %T", f, x)] = fmt.Sprintf(f, x)
		}
		if b, err := json.Marshal(x); err == nil {
			outputs[fmt.Sprintf("json of %T", x)] = string(b)
		}
		if b, err := yaml.Marshal(x); err == nil {
			outputs[fmt.Sprintf("yaml of %T", x)] = string(b)
		}
		for name, h := range map[string]func(*bytes.Buffer) slog.Handler{
			"slog text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
			"slog json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		} {
			var b bytes.Buffer
			slog.New(h(&b)).Info("value", "v", x)
			outputs[fmt.Sprintf("%s of %T", name, x)] = b.String()
		}
	}
	for what, out := range outputs {
		if strings.Contains(out, "PROVIDER-PLAINTEXT") || strings.Contains(out, "50524f") || strings.Contains(out, "UFJPVklE") {
			t.Errorf("%s renders the value: %s", what, out)
		}
	}
	for _, m := range []func() ([]byte, error){v.MarshalJSON, v.MarshalText} {
		if b, err := m(); err == nil || b != nil {
			t.Errorf("a marshaller succeeded: %q", b)
		}
	}
	// Control: the value is there to leak.
	if !strings.Contains(string(v.p.json), "PROVIDER-PLAINTEXT") || v.Kind() != KindString {
		t.Fatal("the value does not hold the plaintext; this test proves nothing")
	}
}

func TestEncryptUsesItsOwnKey(t *testing.T) {
	paths := func(keys Keys) []string {
		i, rec := standIn(t, keys, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/decrypt/") {
				respond(t, w, 200, data(map[string]any{"plaintext": "eA=="}))
				return
			}
			respond(t, w, 200, data(map[string]any{"ciphertext": "vault:v1:YWJj"}))
		})
		if _, err := i.EncryptBaseline(t.Context(), []byte("b")); err != nil {
			t.Fatal(err)
		}
		if _, err := i.EncryptStaging(t.Context(), []byte("s")); err != nil {
			t.Fatal(err)
		}
		if _, err := i.DecryptStaging(t.Context(), "vault:v1:YWJj"); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range rec.all() {
			got = append(got, r.path)
		}
		return got
	}
	want := []string{"/v1/transit/encrypt/k-baseline", "/v1/transit/encrypt/k-staging", "/v1/transit/decrypt/k-staging"}
	if got := paths(testKeys); !slices.Equal(got, want) {
		t.Fatalf("paths %q, want %q", got, want)
	}
	// Control: with the keys swapped, the same check sees the other key in each call.
	swapped := Keys{Baseline: testKeys.Staging, Staging: testKeys.Baseline, Digest: testKeys.Digest}
	if got := paths(swapped); slices.Equal(got, want) {
		t.Fatal("swapping the keys did not change the request paths; the check cannot fail")
	}
}

func TestEncryptParsesCiphertext(t *testing.T) {
	for body, ok := range map[string]bool{
		`{"data":{"ciphertext":"vault:v1:YWJj"}}`:  true,
		`{"data":{"ciphertext":"vault:v12:YWJj"}}`: true,
		`{"data":{"ciphertext":"YWJj"}}`:           false,
		`{"data":{"ciphertext":"vault:v0:YWJj"}}`:  false,
		`{"data":{"ciphertext":"vault:vx:YWJj"}}`:  false,
		`{"data":{"ciphertext":"vault:v1:"}}`:      false,
		`{"data":{"ciphertext":"vault:v01:YWJj"}}`: false,
		`{"data":{"ciphertext":""}}`:               false,
		// The payload is Transit's canonical base64; anything else cannot be decrypted later.
		`{"data":{"ciphertext":"vault:v1:!!!not-base64!!!"}}`: false,
		`{"data":{"ciphertext":"vault:v1:YWJ"}}`:              false,
		`{"data":{"ciphertext":"vault:v1:YWI="}}`:             true,
		`{"data":{"ciphertext":"vault:v1:YWJ="}}`:             false,
		`{"data":{"ciphertext":"vault:v1:YW\nJj"}}`:           false,
		`{"data":{"ciphertext":"vault:v1:YWJj:YWJj"}}`:        false,
	} {
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(body))
		})
		ct, err := i.EncryptStaging(t.Context(), []byte("x"))
		if ok != (err == nil) {
			t.Errorf("%s: %q, %v", body, ct, err)
		}
		if !ok && !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v, want ErrProtocol", body, err)
		}
	}
	if n, err := Ciphertext("vault:v7:YWJj").KeyVersion(); n != 7 || err != nil {
		t.Errorf("KeyVersion = %d, %v", n, err)
	}
}

// E1's two cases: an absent plaintext field, and one that is not base64.
func TestDecryptStagingRefusesMissingOrBadPlaintext(t *testing.T) {
	for _, body := range []string{`{"data":{}}`, `{"data":{"plaintext":null}}`, `{"data":{"plaintext":"not base64!!"}}`, `{"data":{"plaintext":"YWJ="}}`} {
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(body))
		})
		if got, err := i.DecryptStaging(t.Context(), "vault:v1:YWJj"); !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %q, %v; want ErrProtocol", body, got, err)
		}
	}
	// An empty plaintext is a plaintext.
	i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"plaintext":""}}`))
	})
	if got, err := i.DecryptStaging(t.Context(), "vault:v1:YWJj"); err != nil || len(got) != 0 {
		t.Errorf("empty plaintext: %q, %v", got, err)
	}
	// A ciphertext that is not Transit's is refused before it is sent.
	i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {})
	for _, ct := range []Ciphertext{"not-a-ciphertext", "vault:v1:!!!not-base64!!!"} {
		if _, err := i.DecryptStaging(t.Context(), ct); err == nil || len(rec.all()) != 0 {
			t.Errorf("%s: %v, %d request(s)", ct, err, len(rec.all()))
		}
	}
}

// methodNames is the exported method set of typ.
func methodNames(typ reflect.Type) []string {
	var names []string
	for m := range typ.Methods() {
		names = append(names, m.Name)
	}
	slices.Sort(names)
	return names
}

// withBaselineDecrypt is the control: the role type with one more method.
type withBaselineDecrypt struct{ *Ingestion }

func (withBaselineDecrypt) DecryptBaseline(context.Context, Ciphertext) ([]byte, error) {
	return nil, nil
}

// The role type has exactly ingestion's five operations: no baseline or artifact decryption and
// no KV read exist in code, whatever the policy would allow.
func TestIngestionMethodSet(t *testing.T) {
	want := []string{"CreateGeneration", "DecryptStaging", "Digest", "EncryptBaseline", "EncryptStaging"}
	if got := methodNames(reflect.TypeFor[*Ingestion]()); !slices.Equal(got, want) {
		t.Fatalf("*Ingestion exports %q, want exactly %q", got, want)
	}
	if got := methodNames(reflect.TypeFor[withBaselineDecrypt]()); slices.Equal(got, want) {
		t.Fatal("the control with an extra method passed the same check")
	}
}

func TestDigestRequest(t *testing.T) {
	sum := sha256.Sum256([]byte("k"))
	for _, version := range []int{0, 3} {
		i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, 200, data(map[string]any{"hmac": "vault:v3:" + base64.StdEncoding.EncodeToString(sum[:])}))
		})
		d, err := i.Digest(t.Context(), []byte("input"), version)
		if err != nil {
			t.Fatal(err)
		}
		if d.Key != "k-digest" || d.Version != 3 || d.Sum != sum || d.KeyRef() != "transit/k-digest@v3" {
			t.Fatalf("Digest = %+v, KeyRef %q", d, d.KeyRef())
		}
		got := rec.last()
		if got.method != http.MethodPost || got.path != "/v1/transit/hmac/k-digest" {
			t.Fatalf("request %s %s", got.method, got.path)
		}
		var body map[string]any
		if err := json.Unmarshal(got.body, &body); err != nil {
			t.Fatal(err)
		}
		if body["input"] != base64.StdEncoding.EncodeToString([]byte("input")) || body["algorithm"] != "sha2-256" {
			t.Fatalf("body %s", got.body)
		}
		kv, sent := body["key_version"]
		if (version > 0) != sent || (sent && kv != float64(version)) {
			t.Fatalf("version %d: body %s", version, got.body)
		}
	}
	i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {})
	if _, err := i.Digest(t.Context(), []byte("x"), -1); err == nil || len(rec.all()) != 0 {
		t.Fatalf("a negative version: %v", err)
	}
}

func TestDigestParse(t *testing.T) {
	sum := sha256.Sum256([]byte("k"))
	b64 := base64.StdEncoding.EncodeToString(sum[:])
	i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"hmac": "vault:v3:" + b64}))
	})
	if d, err := i.Digest(t.Context(), []byte("x"), 0); err != nil || d.Version != 3 || d.Sum != sum {
		t.Fatalf("vault:v3:<32 bytes> = %+v, %v", d, err)
	}
	short := base64.StdEncoding.EncodeToString(sum[:31])
	long := base64.StdEncoding.EncodeToString(append(sum[:], 0))
	for name, c := range map[string]struct {
		hmac    string
		version int
	}{
		"31 bytes":          {"vault:v3:" + short, 0},
		"33 bytes":          {"vault:v3:" + long, 0},
		"no prefix":         {b64, 0},
		"other prefix":      {"vault:x3:" + b64, 0},
		"non-integer":       {"vault:vx:" + b64, 0},
		"version zero":      {"vault:v0:" + b64, 0},
		"leading zero":      {"vault:v03:" + b64, 0},
		"signed":            {"vault:v+3:" + b64, 0},
		"not base64":        {"vault:v3:!!", 0},
		"a newline":         {"vault:v3:" + b64[:8] + "\n" + b64[8:], 0},
		"not the requested": {"vault:v3:" + b64, 2},
		"empty":             {"", 0},
	} {
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, 200, data(map[string]any{"hmac": c.hmac}))
		})
		if d, err := i.Digest(t.Context(), []byte("x"), c.version); !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %+v, %v; want ErrProtocol", name, d, err)
		}
	}
}

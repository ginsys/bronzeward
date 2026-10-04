package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

const testArtifactKey = "k-artifact"

// serve starts handler and returns its address and a recorder of what it was sent; each recorded
// path carries its query.
func serve(t *testing.T, handler http.HandlerFunc) (string, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		rec.add(request{r.Method, path, r.Header.Get("X-Vault-Token"), body})
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, rec
}

func compilerStandIn(t *testing.T, handler http.HandlerFunc) (*Compiler, *recorder) {
	t.Helper()
	addr, rec := serve(t, handler)
	c, err := NewCompiler(addr, tokenOf(testToken), testArtifactKey)
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

// The secret text a generation holds in these tests; no error may quote it.
const pinnedText = "pinned-secret-text-7Qx"

var pinnedCreated = time.Date(2026, 9, 24, 19, 51, 3, 464091849, time.UTC)

// kvVersion is a KV v2 read of one version: its data and its metadata.
func kvVersion(dataField any, version any, created any, deletion any, destroyed any) map[string]any {
	meta := map[string]any{"version": version, "created_time": created, "deletion_time": deletion, "destroyed": destroyed}
	return data(map[string]any{"data": dataField, "metadata": meta})
}

func pinned() map[string]any {
	return kvVersion(map[string]any{"kind": "string", "value": pinnedText}, 3, pinnedCreated.Format(time.RFC3339Nano), "", false)
}

func TestReadGeneration(t *testing.T) {
	c, rec := compilerStandIn(t, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, 200, pinned()) })
	p := newPath(t)
	v, created, err := c.ReadGeneration(t.Context(), p, 3)
	if err != nil {
		t.Fatal(err)
	}
	q := rec.last()
	if q.method != http.MethodGet || q.path != "/v1/secret/data/"+p.String()+"?version=3" || q.token != testToken || len(q.body) != 0 {
		t.Fatalf("sent %s %s (token %t, %d body bytes)", q.method, q.path, q.token == testToken, len(q.body))
	}
	if got, err := v.Decode(); err != nil || got != pinnedText || v.Kind() != KindString {
		t.Fatalf("value %v (%s), %v", got == pinnedText, v.Kind(), err)
	}
	if !created.Equal(pinnedCreated) {
		t.Fatalf("created %v, want %v", created, pinnedCreated)
	}
}

// Every kind round-trips; an integer stays exact.
func TestReadGenerationKinds(t *testing.T) {
	for _, tc := range []struct {
		kind  Kind
		value string // the JSON the provider holds
		want  any
	}{
		{KindInteger, `9007199254740993`, json.Number("9007199254740993")},
		{KindBoolean, `true`, true},
		{KindMapping, `{"a":"b","n":1}`, map[string]any{"a": "b", "n": json.Number("1")}},
	} {
		c, _ := compilerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			io.WriteString(w, `{"data":{"data":{"kind":"`+string(tc.kind)+`","value":`+tc.value+`},"metadata":{"version":1,"created_time":"2026-09-24T19:51:03.464091849Z","deletion_time":"","destroyed":false}}}`)
		})
		v, _, err := c.ReadGeneration(t.Context(), newPath(t), 1)
		if err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		got, err := v.Decode()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: %#v, %v", tc.kind, got, err)
		}
	}
}

// Anything but the asked version, live, holding exactly a checked {kind, value}, is refused, and
// no refusal quotes the value.
func TestReadGenerationRefusesMalformed(t *testing.T) {
	created := pinnedCreated.Format(time.RFC3339Nano)
	value := map[string]any{"kind": "string", "value": pinnedText}
	for name, payload := range map[string]any{
		"another version":       kvVersion(value, 4, created, "", false),
		"no version":            kvVersion(value, nil, created, "", false),
		"no created_time":       kvVersion(value, 3, nil, "", false),
		"created_time not time": kvVersion(value, 3, pinnedText, "", false),
		"deleted":               kvVersion(value, 3, created, "2026-09-25T00:00:00Z", false),
		"destroyed":             kvVersion(value, 3, created, "", true),
		"no deletion_time":      kvVersion(value, 3, created, nil, false),
		"no destroyed":          kvVersion(value, 3, created, "", nil),
		"no data":               kvVersion(nil, 3, created, "", false),
		"an extra field":        kvVersion(map[string]any{"kind": "string", "value": pinnedText, "x": pinnedText}, 3, created, "", false),
		"no kind":               kvVersion(map[string]any{"value": pinnedText}, 3, created, "", false),
		"wrong kind":            kvVersion(map[string]any{"kind": "integer", "value": pinnedText}, 3, created, "", false),
		"unknown kind":          kvVersion(map[string]any{"kind": pinnedText, "value": pinnedText}, 3, created, "", false),
		"a float":               kvVersion(map[string]any{"kind": "integer", "value": 1.5}, 3, created, "", false),
		"no metadata":           data(map[string]any{"data": value}),
	} {
		c, _ := compilerStandIn(t, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, 200, payload) })
		_, _, err := c.ReadGeneration(t.Context(), newPath(t), 3)
		if !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v, want ErrProtocol", name, err)
		}
		if err != nil && strings.Contains(err.Error(), pinnedText) {
			t.Errorf("%s: the error quotes the value", name)
		}
	}
}

// A missing or deleted version is OpenBao's 404, a refusal its 403: typed, as every role's reads.
func TestReadGenerationTypedOutcomes(t *testing.T) {
	for status, want := range map[int]error{404: ErrAbsent, 403: ErrDenied, 503: ErrUnavailable} {
		c, _ := compilerStandIn(t, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, status, pinned()) })
		if _, _, err := c.ReadGeneration(t.Context(), newPath(t), 3); !errors.Is(err, want) {
			t.Errorf("%d: %v, want %v", status, err, want)
		}
	}
}

func TestReadGenerationRefusesBeforeSending(t *testing.T) {
	c, rec := compilerStandIn(t, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, 200, pinned()) })
	if _, _, err := c.ReadGeneration(t.Context(), GenerationPath{}, 3); err == nil {
		t.Error("the zero path was read")
	}
	for _, v := range []int64{0, -1} {
		if _, _, err := c.ReadGeneration(t.Context(), newPath(t), v); err == nil {
			t.Errorf("version %d was read", v)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d request(s) sent", n)
	}
}

func TestEncryptArtifact(t *testing.T) {
	c, rec := compilerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, data(map[string]any{"ciphertext": "vault:v2:" + base64.StdEncoding.EncodeToString([]byte("sealed"))}))
	})
	ct, err := c.EncryptArtifact(t.Context(), []byte(pinnedText))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ct.KeyVersion(); err != nil || n != 2 {
		t.Fatalf("key version %d, %v", n, err)
	}
	q := rec.last()
	var body struct{ Plaintext string }
	if err := json.Unmarshal(q.body, &body); err != nil {
		t.Fatal(err)
	}
	if plain, err := base64.StdEncoding.DecodeString(body.Plaintext); q.method != http.MethodPost || q.path != "/v1/transit/encrypt/"+testArtifactKey || err != nil || string(plain) != pinnedText {
		t.Fatalf("sent %s %s", q.method, q.path)
	}
}

func TestNewCompilerRefuses(t *testing.T) {
	for _, key := range []string{"", "..", "a/b", "a%2f"} {
		if _, err := NewCompiler("http://127.0.0.1:1", tokenOf(testToken), key); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
	if _, err := NewCompiler("http://127.0.0.1:1", Token{}, testArtifactKey); err == nil {
		t.Error("no token accepted")
	}
}

// withArtifactDecrypt is the control: the role type with one more method.
type withArtifactDecrypt struct{ *Compiler }

func (withArtifactDecrypt) DecryptArtifact(context.Context, Ciphertext) ([]byte, error) {
	return nil, nil
}

// The compiler identity's operations and nothing else (compilation.md §1): no decryption, no
// secret creation, no other read, whatever the policy would allow.
func TestCompilerMethodSet(t *testing.T) {
	want := []string{"EncryptArtifact", "ReadGeneration"}
	if got := methodNames(reflect.TypeFor[*Compiler]()); !slices.Equal(got, want) {
		t.Fatalf("*Compiler exports %q, want exactly %q", got, want)
	}
	if got := methodNames(reflect.TypeFor[withArtifactDecrypt]()); slices.Equal(got, want) {
		t.Fatal("the control with an extra method passed the same check")
	}
}

func TestParseGenerationPath(t *testing.T) {
	p := newPath(t)
	got, err := ParseGenerationPath(p.String())
	if err != nil || got != p {
		t.Fatalf("ParseGenerationPath(%s) = %v, %v", p, got, err)
	}
	cl, ing, v := id.New(id.Cluster), id.New(id.Ingestion), NewValueID()
	for _, s := range []string{
		"",
		"gen/" + cl + "/" + ing,
		"gen/" + cl + "/" + ing + "/" + v + "/x",
		"gen/" + cl + "/" + ing + "/",
		"/gen/" + cl + "/" + ing + "/" + v,
		"other/" + cl + "/" + ing + "/" + v,
		"gen/" + ing + "/" + cl + "/" + v,
		"gen/" + cl + "/" + ing + "/a.b",
		"gen/" + cl + "/" + ing + "/..",
	} {
		if _, err := ParseGenerationPath(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

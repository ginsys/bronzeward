package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const (
	caText  = "-----BEGIN CERTIFICATE-----\nCA-CERT-MARKER\n-----END CERTIFICATE-----\n"
	keyText = "-----BEGIN ED25519 PRIVATE KEY-----\nCA-KEY-MARKER\n-----END ED25519 PRIVATE KEY-----\n"
)

// signerStandIn is a Signer pointed at handler, and the recorder it fills.
func signerStandIn(t *testing.T, handler http.HandlerFunc) (*Signer, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		rec.add(request{r.Method, r.URL.Path, r.Header.Get("X-Vault-Token"), body})
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	s, err := NewSigner(srv.URL, tokenOf(testToken))
	if err != nil {
		t.Fatal(err)
	}
	return s, rec
}

func TestCAKeyPath(t *testing.T) {
	cl := id.New(id.Cluster)
	if p, err := CAKeyPath(cl); err != nil || p != "access/talos-ca/"+cl {
		t.Fatalf("CAKeyPath(%q) = %q, %v", cl, p, err)
	}
	for name, in := range map[string]string{
		"empty": "", "other kind": id.New(id.Ingestion), "slash": cl + "/x", "dot-dot": "cl_../../sys", "query": cl + "?x",
	} {
		if p, err := CAKeyPath(in); err == nil {
			t.Errorf("%s: CAKeyPath(%q) = %q, want a refusal", name, in, p)
		}
	}
}

func TestReadCAKey(t *testing.T) {
	cl := id.New(id.Cluster)
	created := "2026-11-19T01:02:03.123456789Z"
	s, rec := signerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, kvRead(map[string]any{"ca": caText, "key": keyText}, kvMetadata(4, created)))
	})
	k, err := s.ReadCAKey(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.last()
	if got.method != http.MethodGet || got.path != "/v1/secret/data/access/talos-ca/"+cl || got.token != testToken || len(got.body) != 0 {
		t.Fatalf("request %s %s, token %q, body %q", got.method, got.path, got.token, got.body)
	}
	want, _ := time.Parse(time.RFC3339Nano, created)
	if v := k.Version(); v.Path != "access/talos-ca/"+cl || v.Version != 4 || !v.CreatedTime.Equal(want) {
		t.Fatalf("version %+v", v)
	}
	if string(k.Crt()) != caText || string(k.Key()) != keyText {
		t.Fatal("the key read is not the key served")
	}
	k.Key()[0] = 'X'
	if string(k.Key()) != keyText {
		t.Fatal("Key shares its bytes")
	}
}

func TestReadCAKeyOutcomes(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		want   error
	}{"absent": {404, ErrAbsent}, "denied": {403, ErrDenied}, "sealed": {503, ErrUnavailable}} {
		s, _ := signerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, c.status, map[string]any{"errors": []string{keyText}})
		})
		_, err := s.ReadCAKey(t.Context(), id.New(id.Cluster))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "CA-KEY-MARKER") {
			t.Errorf("%s: the error quotes the response: %v", name, err)
		}
	}
	// Anything but one live version holding exactly a ca and a key is a protocol error.
	for name, fields := range map[string]map[string]any{
		"no key":      {"ca": caText},
		"extra":       {"ca": caText, "key": keyText, "more": "x"},
		"empty ca":    {"ca": "", "key": keyText},
		"not strings": {"ca": 1, "key": keyText},
	} {
		s, _ := signerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, 200, kvRead(fields, kvMetadata(1, "2026-11-19T01:02:03Z")))
		})
		if _, err := s.ReadCAKey(t.Context(), id.New(id.Cluster)); !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v, want ErrProtocol", name, err)
		}
	}
	// A deleted latest version is not live.
	s, _ := signerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		md := kvMetadata(2, "2026-11-19T01:02:03Z")
		md["deletion_time"] = "2026-11-19T02:00:00Z"
		respond(t, w, 200, kvRead(map[string]any{"ca": caText, "key": keyText}, md))
	})
	if _, err := s.ReadCAKey(t.Context(), id.New(id.Cluster)); !errors.Is(err, ErrProtocol) {
		t.Errorf("deleted version: %v, want ErrProtocol", err)
	}
	// Refused before sending.
	s, rec := signerStandIn(t, func(http.ResponseWriter, *http.Request) {})
	if _, err := s.ReadCAKey(t.Context(), "cl_../../sys"); err == nil || len(rec.all()) != 0 {
		t.Fatalf("a non-cluster id: %v, %d requests", err, len(rec.all()))
	}
}

func TestWriteCAKey(t *testing.T) {
	cl := id.New(id.Cluster)
	created := "2026-11-19T01:02:03.5Z"
	for _, cas := range []int{0, 3} {
		s, rec := signerStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, 200, data(map[string]any{"version": cas + 1, "created_time": created}))
		})
		v, err := s.WriteCAKey(t.Context(), cl, NewCAKey([]byte(caText), []byte(keyText)), cas)
		if err != nil {
			t.Fatal(err)
		}
		got := rec.last()
		if got.method != http.MethodPost || got.path != "/v1/secret/data/access/talos-ca/"+cl || got.token != testToken {
			t.Fatalf("request %s %s, token %q", got.method, got.path, got.token)
		}
		var body struct {
			Options map[string]any    `json:"options"`
			Data    map[string]string `json:"data"`
		}
		if err := json.Unmarshal(got.body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Options) != 1 || body.Options["cas"] != float64(cas) || len(body.Data) != 2 || body.Data["ca"] != caText || body.Data["key"] != keyText {
			t.Fatalf("body %s", got.body)
		}
		want, _ := time.Parse(time.RFC3339Nano, created)
		if v.Path != "access/talos-ca/"+cl || v.Version != cas+1 || !v.CreatedTime.Equal(want) {
			t.Fatalf("version %+v", v)
		}
	}
}

func TestWriteCAKeyOutcomes(t *testing.T) {
	k := NewCAKey([]byte(caText), []byte(keyText))
	cl := id.New(id.Cluster)
	for name, c := range map[string]struct {
		status int
		body   any
		want   error
	}{
		"cas mismatch":  {400, map[string]any{"errors": []string{"check-and-set parameter did not match the current version"}}, ErrExists},
		"denied":        {403, map[string]any{"errors": []string{keyText}}, ErrDenied},
		"sealed":        {503, map[string]any{"errors": []string{keyText}}, ErrUnavailable},
		"wrong version": {200, data(map[string]any{"version": 9, "created_time": "2026-11-19T01:02:03Z"}), ErrProtocol},
		"no version":    {200, data(map[string]any{"created_time": "2026-11-19T01:02:03Z"}), ErrProtocol},
		"no time":       {200, data(map[string]any{"version": 1}), ErrProtocol},
	} {
		s, _ := signerStandIn(t, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, c.status, c.body) })
		_, err := s.WriteCAKey(t.Context(), cl, k, 0)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if err != nil && (strings.Contains(err.Error(), "CA-KEY-MARKER") || strings.Contains(err.Error(), "CA-CERT-MARKER")) {
			t.Errorf("%s: the error quotes the key: %v", name, err)
		}
	}
	// Refused before sending: a zero key, a negative cas, a bad cluster.
	s, rec := signerStandIn(t, func(http.ResponseWriter, *http.Request) {})
	for name, f := range map[string]func() error{
		"zero key":     func() error { _, err := s.WriteCAKey(t.Context(), cl, CAKey{}, 0); return err },
		"empty key":    func() error { _, err := s.WriteCAKey(t.Context(), cl, NewCAKey([]byte(caText), nil), 0); return err },
		"negative cas": func() error { _, err := s.WriteCAKey(t.Context(), cl, k, -1); return err },
		"bad cluster":  func() error { _, err := s.WriteCAKey(t.Context(), "cl_x/../y", k, 0); return err },
	} {
		if err := f(); err == nil {
			t.Errorf("%s: no refusal", name)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d request(s) sent", n)
	}
}

func TestCAKeyDoesNotRender(t *testing.T) {
	k := NewCAKey([]byte(caText), []byte(keyText))
	holder := struct{ K CAKey }{k}
	for _, v := range []any{k, &k, holder, &holder, []CAKey{k}, map[string]CAKey{"k": k}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			if out := fmt.Sprintf(verb, v); strings.Contains(out, "MARKER") {
				t.Errorf("%s of %T prints the key: %q", verb, v, out)
			}
		}
	}
	if _, err := json.Marshal(k); err == nil {
		t.Error("json.Marshal(CAKey) succeeded")
	}
	if _, err := json.Marshal(holder); err == nil {
		t.Error("json.Marshal of a struct holding a CAKey succeeded")
	}
	var b bytes.Buffer
	fmt.Fprint(&b, k)
	if b.String() != caKeyText {
		t.Errorf("Fprint = %q", b.String())
	}
}

// withSignerList is the control: the role type with one more method.
type withSignerList struct{ *Signer }

func (withSignerList) List(context.Context, string) ([]string, error) { return nil, nil }

// The signer identity reads and writes the one CA key path and does nothing else: no list, no delete,
// no metadata read, no Transit operation, whatever its policy would allow.
func TestSignerMethodSet(t *testing.T) {
	want := []string{"ReadCAKey", "WriteCAKey"}
	if got := methodNames(reflect.TypeFor[*Signer]()); !slices.Equal(got, want) {
		t.Fatalf("*Signer exports %q, want exactly %q", got, want)
	}
	if got := methodNames(reflect.TypeFor[withSignerList]()); slices.Equal(got, want) {
		t.Fatal("the control with an extra method passed the same check")
	}
}

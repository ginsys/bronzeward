package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// plaintext holds characters JSON escapes, so its JSON spelling differs from its raw one: a
// redaction by value would miss one of the two, which is why no server text is relayed at all.
const plaintext = `PROVIDER-PLAINTEXT-MUST-NOT-APPEAR "q" \b <&>`

// spellings is every form a server could quote the plaintext back in.
func spellings() []string {
	quoted, _ := json.Marshal(plaintext)
	escaped := strings.Trim(string(quoted), `"`)
	return []string{
		plaintext,
		escaped,
		base64.StdEncoding.EncodeToString([]byte(plaintext)),
		"PROVIDER-PLAINTEXT-MUST-NOT-APPEAR", // any part of it
	}
}

// call is one plaintext-carrying operation, and the spelling its request carries the plaintext in.
type call struct {
	name     string
	path     string
	inFlight string
	run      func(context.Context, *Ingestion) error
}

func plaintextCalls(t *testing.T) []call {
	b64 := base64.StdEncoding.EncodeToString([]byte(plaintext))
	quoted, _ := json.Marshal(plaintext)
	p := newPath(t)
	v := mustValue(t, KindMapping, map[string]any{"password": plaintext})
	return []call{
		{"CreateGeneration", "/v1/secret/data/" + p.String(), strings.Trim(string(quoted), `"`), func(ctx context.Context, i *Ingestion) error {
			_, err := i.CreateGeneration(ctx, p, v)
			return err
		}},
		{"EncryptBaseline", "/v1/transit/encrypt/k-baseline", b64, func(ctx context.Context, i *Ingestion) error {
			_, err := i.EncryptBaseline(ctx, []byte(plaintext))
			return err
		}},
		{"EncryptStaging", "/v1/transit/encrypt/k-staging", b64, func(ctx context.Context, i *Ingestion) error {
			_, err := i.EncryptStaging(ctx, []byte(plaintext))
			return err
		}},
		{"Digest", "/v1/transit/hmac/k-digest", b64, func(ctx context.Context, i *Ingestion) error {
			_, err := i.Digest(ctx, []byte(plaintext), 0)
			return err
		}},
	}
}

func TestErrorsNeverQuoteThePlaintext(t *testing.T) {
	quoted, _ := json.Marshal(plaintext)
	echoes := map[string]func(body []byte) []byte{
		"raw": func(body []byte) []byte { return body },
		"errors[] raw": func(body []byte) []byte {
			b, _ := json.Marshal(map[string]any{"errors": []string{"rejected " + plaintext, string(body)}})
			return b
		},
		"errors[] base64": func([]byte) []byte {
			b, _ := json.Marshal(map[string]any{"errors": []string{"rejected " + base64.StdEncoding.EncodeToString([]byte(plaintext))}})
			return b
		},
		// The JSON-escaped spelling placed into the error string as it stands, so it reaches the
		// client escaped twice and decodes to the escaped form once.
		"errors[] escaped": func([]byte) []byte {
			return []byte(`{"errors":["rejected ` + strings.ReplaceAll(strings.Trim(string(quoted), `"`), `\`, `\\`) + `"]}`)
		},
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusOK} {
		for echoName, echo := range echoes {
			for _, c := range plaintextCalls(t) {
				t.Run(fmt.Sprintf("%d/%s/%s", status, echoName, c.name), func(t *testing.T) {
					i, rec := standIn(t, testKeys, func(w http.ResponseWriter, r *http.Request) {
						body, _ := io.ReadAll(r.Body)
						w.WriteHeader(status)
						w.Write(echo(body))
					})
					err := c.run(t.Context(), i)
					if err == nil {
						t.Fatalf("succeeded against a %d echoing the request", status)
					}
					// Control: the request really carried the plaintext.
					if !strings.Contains(string(rec.last().body), c.inFlight) {
						t.Fatalf("the request did not carry the plaintext as %q, so this test proves nothing: %s", c.inFlight, rec.last().body)
					}
					for _, s := range spellings() {
						if strings.Contains(err.Error(), s) {
							t.Errorf("the error quotes %q: %v", s, err)
						}
					}
					if strings.Contains(err.Error(), "rejected") {
						t.Errorf("the error relays server text: %v", err)
					}
					for _, want := range []string{fmt.Sprint(status), c.path} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("the error does not name %q: %v", want, err)
						}
					}
					if status == http.StatusOK && !errors.Is(err, ErrProtocol) {
						t.Errorf("an echoed 200 is %v, want ErrProtocol", err)
					}
				})
			}
		}
	}
}

func TestStatusClassification(t *testing.T) {
	enc := func(i *Ingestion) error {
		_, err := i.EncryptBaseline(t.Context(), []byte("x"))
		return err
	}
	create := func(i *Ingestion) error {
		_, err := i.CreateGeneration(t.Context(), newPath(t), mustValue(t, KindString, "x"))
		return err
	}
	for _, c := range []struct {
		name   string
		status int
		body   string
		run    func(*Ingestion) error
		want   error
	}{
		{"403", 403, `{"errors":["permission denied"]}`, enc, ErrDenied},
		{"403 create", 403, `{"errors":["1 error occurred:\n\t* permission denied\n\n"]}`, create, ErrDenied},
		{"400 CAS", 400, `{"errors":["check-and-set parameter did not match the current version"]}`, create, ErrExists},
		{"400 CAS wrapped", 400, `{"errors":["1 error occurred:\n\t* check-and-set parameter did not match the current version\n\n"]}`, create, ErrExists},
		{"400 other", 400, `{"errors":["bad request"]}`, create, nil},
		{"400 CAS on transit", 400, `{"errors":["check-and-set parameter did not match the current version"]}`, enc, nil},
		{"503", 503, `{"errors":["Vault is sealed"]}`, enc, ErrUnavailable},
		{"429", 429, `{}`, enc, ErrUnavailable},
		{"500", 500, `{}`, enc, nil},
		{"404", 404, `{}`, enc, nil},
		{"307 not followed", 307, ``, enc, nil},
		{"200 missing field", 200, `{"data":{}}`, enc, ErrProtocol},
		{"200 null field", 200, `{"data":{"ciphertext":null}}`, enc, ErrProtocol},
		{"200 null data", 200, `{"data":null}`, enc, ErrProtocol},
		{"200 ill-typed field", 200, `{"data":{"ciphertext":7}}`, enc, ErrProtocol},
		{"200 not JSON", 200, `<html>`, enc, ErrProtocol},
		{"200 trailing data", 200, `{"data":{"ciphertext":"vault:v1:YQ=="}} {}`, enc, ErrProtocol},
		{"200 create missing version", 200, `{"data":{}}`, create, ErrProtocol},
		{"200 create ill-typed version", 200, `{"data":{"version":"1"}}`, create, ErrProtocol},
	} {
		t.Run(c.name, func(t *testing.T) {
			i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				io.WriteString(w, c.body)
			})
			err := c.run(i)
			if err == nil {
				t.Fatal("no error")
			}
			typed := []error{ErrDenied, ErrExists, ErrUnavailable, ErrProtocol}
			if c.want == nil {
				for _, k := range typed {
					if errors.Is(err, k) {
						t.Fatalf("%v is %v, want an untyped error", err, k)
					}
				}
				return
			}
			for _, k := range typed {
				if errors.Is(err, k) != (k == c.want) {
					t.Fatalf("%v: errors.Is(%v) = %t", err, k, errors.Is(err, k))
				}
			}
		})
	}

	t.Run("connection refused", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		l.Close()
		i, err := NewIngestion("http://"+addr, tokenOf(testToken), testKeys)
		if err != nil {
			t.Fatal(err)
		}
		if err := enc(i); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%v, want ErrUnavailable", err)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		release := make(chan struct{})
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		defer close(release)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := i.EncryptBaseline(ctx, []byte("x"))
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%v, want ErrUnavailable and context.DeadlineExceeded", err)
		}
	})
}

// E1's test: neither the token nor a body is replayed to wherever a 3xx points.
func TestRedirectsNotFollowed(t *testing.T) {
	var elsewhere atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	for _, code := range []int{301, 302, 303, 307, 308} {
		i, rec := standIn(t, testKeys, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, code)
		})
		if _, err := i.EncryptBaseline(t.Context(), []byte(plaintext)); err == nil {
			t.Fatalf("%d: EncryptBaseline succeeded against a redirect", code)
		}
		if got := rec.last(); got.token != testToken || !strings.Contains(string(got.body), base64.StdEncoding.EncodeToString([]byte(plaintext))) {
			t.Fatalf("%d: the first server did not see the token and the plaintext, so this test proves nothing", code)
		}
	}
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("a redirect was followed %d time(s), replaying the token and the body", n)
	}
}

func TestResponseCapped(t *testing.T) {
	big := `{"data":{"ciphertext":"vault:v1:` + strings.Repeat("A", 5<<20) + `"}}`
	i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, big)
	})
	if _, err := i.EncryptBaseline(t.Context(), []byte("x")); !errors.Is(err, ErrProtocol) {
		t.Fatalf("a 5 MiB response gave %v, want ErrProtocol", err)
	}
	// Control: the same shape under the cap is accepted.
	small := `{"data":{"ciphertext":"vault:v1:` + strings.Repeat("A", 1<<20) + `"}}`
	i, _ = standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, small)
	})
	if _, err := i.EncryptBaseline(t.Context(), []byte("x")); err != nil {
		t.Fatalf("a 1 MiB response was refused: %v", err)
	}
}

func TestNewIngestionRefuses(t *testing.T) {
	ok := tokenOf(testToken)
	for _, c := range []struct {
		name string
		addr string
		tok  Token
		keys Keys
	}{
		{"empty address", "", ok, testKeys},
		{"no scheme", "127.0.0.1:8200", ok, testKeys},
		{"ftp", "ftp://127.0.0.1", ok, testKeys},
		{"userinfo", "http://u:p@127.0.0.1:8200", ok, testKeys},
		{"query", "http://127.0.0.1:8200?x=1", ok, testKeys},
		{"fragment", "http://127.0.0.1:8200#x", ok, testKeys},
		{"path", "http://127.0.0.1:8200/v1", ok, testKeys},
		{"no host", "http://", ok, testKeys},
		{"zero token", "http://127.0.0.1:8200", Token{}, testKeys},
		{"empty key", "http://127.0.0.1:8200", ok, Keys{"a", "b", ""}},
		{"slash key", "http://127.0.0.1:8200", ok, Keys{"a", "b", "c/d"}},
		{"shared key", "http://127.0.0.1:8200", ok, Keys{"a", "b", "a"}},
	} {
		if _, err := NewIngestion(c.addr, c.tok, c.keys); err == nil {
			t.Errorf("%s: NewIngestion accepted it", c.name)
		} else if strings.Contains(err.Error(), "u:p") {
			t.Errorf("%s: the error quotes the userinfo: %v", c.name, err)
		}
	}
	if _, err := NewIngestion("http://127.0.0.1:8200/", ok, testKeys); err != nil {
		t.Errorf("a valid address was refused: %v", err)
	}
}

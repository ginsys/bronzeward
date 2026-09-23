package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The tests below run against a stand-in for OpenBao rather than a live one. What they pin is this
// program's half of the contract: the paths it calls, the token header it sends, the shape it
// expects back, and — the one that matters most — that a failing request never puts what it was
// sending into an error string. The live provider is exercised by the experiment's matrix run
// against the fixtures, which is where a wrong assumption about OpenBao's API would surface.

// recorder captures what the client sent, so a test can assert the request rather than infer it.
// The handler fills it on the server's goroutine and the test reads it on its own, so both sides go
// through the mutex: the ordering must not rest on the client having read a response first.
type recorder struct {
	mu  sync.Mutex
	got request
}

// request is one recorded request.
type request struct {
	method string
	path   string
	token  string
	body   string
}

func (r *recorder) set(req request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = req
}

// last is the most recent request, copied under the lock.
func (r *recorder) last() request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got
}

// server returns a client pointed at a handler, plus the recorder it fills.
func server(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		rec.set(request{method: r.Method, path: r.URL.Path, token: r.Header.Get("X-Vault-Token"), body: string(body)})
		// Put the body back. Without this the handler reads nothing, and a test that asserts an
		// error does not quote the request would pass against an empty request — which is the
		// shape of a check that cannot fail.
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, rec
}

func respond(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encoding the stand-in response: %v", err)
	}
}

// TestKVPathRefusesKeysAURLWouldRewrite covers the keys that would reach a different secret than
// the one named. The control case is the shape every real key has — a run identifier and a
// document path, brackets included — and it must pass through byte-for-byte, because the committed
// evidence records those request paths.
func TestKVPathRefusesKeysAURLWouldRewrite(t *testing.T) {
	const real = "honest-import-transient/doc[0].machine.token"
	if got, err := kvPath("/v1/secret/data/", real); err != nil || got != "/v1/secret/data/"+real {
		t.Fatalf("kvPath(%q) = %q, %v; want it unchanged", real, got, err)
	}
	for _, key := range []string{
		"",
		"run/doc[0].a#b",
		"run/doc[0].a?b",
		"run/doc[0].a%2Fb",
		"run/doc[0].a b",
		"run//doc[0].a",
		"run/../other",
		"run/./doc[0].a",
	} {
		if got, err := kvPath("/v1/secret/data/", key); err == nil {
			t.Errorf("kvPath accepted %q as %q", key, got)
		}
	}
}

// TestTransitPathRefusesKeyNamesAURLWouldRewrite holds Encrypt and Decrypt to the KV rules, plus
// no '/' at all, since a Transit key name is one segment. The fixture's own key name must pass.
func TestTransitPathRefusesKeyNamesAURLWouldRewrite(t *testing.T) {
	if got, err := transitPath("encrypt", "bw-artifact"); err != nil || got != "/v1/transit/encrypt/bw-artifact" {
		t.Fatalf("transitPath(bw-artifact) = %q, %v", got, err)
	}
	for _, name := range []string{"", "k#x", "k?x", "k%2F", "k x", "a/b", "../sys", "."} {
		if got, err := transitPath("encrypt", name); err == nil {
			t.Errorf("transitPath accepted %q as %q", name, got)
		}
	}
}

// TestPutAddressesTheVersionItWrote covers the KV v2 write and the URI it produces. The version
// matters: without it the URI names a path whose value a later write could change, and a read back
// would return something this run never stored.
func TestPutAddressesTheVersionItWrote(t *testing.T) {
	c, rec := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{"data": map[string]any{"version": 3}})
	})

	uri, err := c.Put(t.Context(), "run-1/machine.ca.key", []byte("private-key-value"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if want := "kv://secret/run-1/machine.ca.key@3"; uri != want {
		t.Errorf("Put returned %q, want %q", uri, want)
	}

	if rec.last().method != http.MethodPost {
		t.Errorf("method = %s, want POST", rec.last().method)
	}
	if want := "/v1/secret/data/run-1/machine.ca.key"; rec.last().path != want {
		t.Errorf("path = %q, want %q", rec.last().path, want)
	}
	if rec.last().token != "test-token" {
		t.Errorf("token header = %q", rec.last().token)
	}
	if !strings.Contains(rec.last().body, "private-key-value") {
		t.Errorf("the value did not reach the server: %q", rec.last().body)
	}
}

// TestPutRefusesInvalidUTF8 checks a value JSON would rewrite is refused before it is sent, rather
// than stored as U+FFFD under a URI that claims to address it.
func TestPutRefusesInvalidUTF8(t *testing.T) {
	c, rec := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{"data": map[string]any{"version": 1}})
	})

	if uri, err := c.Put(t.Context(), "run-1/x", []byte("ab\xff\xfecd")); err == nil {
		t.Fatalf("Put accepted a value that is not valid UTF-8 and returned %q", uri)
	}
	if rec.last().method != "" {
		t.Errorf("the value was sent before it was refused: %s %s", rec.last().method, rec.last().path)
	}
}

// TestRedirectsAreNotFollowed checks neither the token nor a secret body is replayed to wherever a
// 3xx points. The second server stands for that other host: it must never see a request.
func TestRedirectsAreNotFollowed(t *testing.T) {
	const plaintext = "E1-PROVIDER-PLAINTEXT-MUST-NOT-TRAVEL"

	var elsewhere atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)

	c, rec := server(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	})

	if _, err := c.Put(t.Context(), "run-1/machine.token", []byte(plaintext)); err == nil {
		t.Fatal("Put succeeded against a redirect")
	}
	// The control: the first server really was asked, with the secret in the body.
	if !strings.Contains(rec.last().body, plaintext) {
		t.Fatalf("the request did not carry the plaintext, so this test proves nothing: %q", rec.last().body)
	}
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("the redirect was followed %d time(s), replaying the token and the body", n)
	}
}

// TestPutRefusesAResponseWithoutAVersion checks the run stops rather than returning a URI that
// cannot address what was just written.
func TestPutRefusesAResponseWithoutAVersion(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{"data": map[string]any{}})
	})

	if _, err := c.Put(t.Context(), "run-1/x", []byte("v")); err == nil {
		t.Fatal("Put accepted a response with no version")
	}
}

// TestDecryptRefusesAResponseWithoutPlaintext covers the one call that used to fail silently: an
// absent plaintext field decoded as "" and came back as an empty document with no error.
func TestDecryptRefusesAResponseWithoutPlaintext(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{"data": map[string]any{}})
	})

	if got, err := c.Decrypt(t.Context(), "bw-artifact", "vault:v1:xyz"); err == nil {
		t.Fatalf("Decrypt accepted a response with no plaintext and returned %q", got)
	}
}

// TestAFailedWriteDoesNotLeakWhatItWasSending is the one that matters. A provider error ends up in
// a log and an error report, both of which §7.1 names as persistence surfaces, and the request
// body is the secret itself.
func TestAFailedWriteDoesNotLeakWhatItWasSending(t *testing.T) {
	const plaintext = "E1-PROVIDER-PLAINTEXT-MUST-NOT-APPEAR"

	// A hostile-but-realistic server: it echoes the request back in its error response, which is
	// exactly how a secret ends up in a client's error string.
	c, rec := server(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		w.WriteHeader(http.StatusForbidden)
		if _, err := w.Write(body); err != nil {
			t.Errorf("writing the stand-in response: %v", err)
		}
	})

	_, err := c.Put(t.Context(), "run-1/machine.token", []byte(plaintext))
	if err == nil {
		t.Fatal("Put succeeded against a 403")
	}
	// The control: the secret really was in flight, and really was echoed back. Without this the
	// assertion below could pass against an empty request, which is a check that cannot fail.
	if !strings.Contains(rec.last().body, plaintext) {
		t.Fatalf("the request did not carry the plaintext, so this test proves nothing: %q", rec.last().body)
	}
	if strings.Contains(err.Error(), plaintext) {
		t.Errorf("the error holds what was being written: %v", err)
	}
	// It must still be actionable: status and path are not secret and are what a reader needs.
	for _, want := range []string{"403", "/v1/secret/data/run-1/machine.token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestAStructuredErrorDoesNotEchoTheSecret covers the form the raw-echo test above cannot: a server
// that puts the value inside a well-formed OpenBao error. It parses, so it used to be relayed
// verbatim. Both writes are covered, each in the spelling its request carried.
func TestAStructuredErrorDoesNotEchoTheSecret(t *testing.T) {
	const plaintext = "E1-PROVIDER-PLAINTEXT-MUST-NOT-APPEAR"
	b64 := base64.StdEncoding.EncodeToString([]byte(plaintext))

	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusBadRequest, map[string]any{"errors": []string{
			"rejected value " + plaintext, "rejected plaintext " + b64, "permission denied",
		}})
	})
	_, putErr := c.Put(t.Context(), "run-1/machine.token", []byte(plaintext))
	_, encErr := c.Encrypt(t.Context(), "bw-artifact", []byte(plaintext))
	for name, err := range map[string]error{"Put": putErr, "Encrypt": encErr} {
		if err == nil {
			t.Fatalf("%s succeeded against a 400", name)
		}
		for _, spelling := range []string{plaintext, b64} {
			if strings.Contains(err.Error(), spelling) {
				t.Errorf("%s's error echoes the secret as %q: %v", name, spelling, err)
			}
		}
		// The server's own message still has to reach the operator.
		if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%s's error dropped the server's message: %v", name, err)
		}
	}
}

// TestAServerErrorMessageIsReported checks OpenBao's own error strings do come through, since
// those are what tell an operator whether the token is wrong or the mount is missing.
func TestAServerErrorMessageIsReported(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusForbidden, map[string]any{"errors": []string{"permission denied"}})
	})

	_, err := c.Put(t.Context(), "run-1/x", []byte("v"))
	if err == nil {
		t.Fatal("Put succeeded against a 403")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("the server's message was dropped: %v", err)
	}
}

// TestGetReadsTheValueBack covers the read used to show a secret reached the provider intact,
// rather than inferring it from a write that returned 200.
func TestGetReadsTheValueBack(t *testing.T) {
	c, rec := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{
			"data": map[string]any{"data": map[string]string{"value": "private-key-value"}},
		})
	})

	got, err := c.Get(t.Context(), "run-1/machine.ca.key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "private-key-value" {
		t.Errorf("Get returned %q", got)
	}
	if rec.last().method != http.MethodGet {
		t.Errorf("method = %s, want GET", rec.last().method)
	}
}

// TestGetRefusesAResponseWithoutTheField checks a malformed read is an error rather than an empty
// value, which a caller would compare against the input and report as a mismatch for the wrong
// reason.
func TestGetRefusesAResponseWithoutTheField(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{"data": map[string]any{"data": map[string]string{}}})
	})

	if _, err := c.Get(t.Context(), "run-1/x"); err == nil {
		t.Fatal("Get accepted a response with no value field")
	}
}

// TestVersionsRefusesAVersionNameThatIsNotAnInteger covers names a lenient parse accepted: "1x"
// read as version 1, which could duplicate the real one in the evidence.
func TestVersionsRefusesAVersionNameThatIsNotAnInteger(t *testing.T) {
	for _, name := range []string{"1x", "x", "", "0", "-1", "1.0"} {
		c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, http.StatusOK, map[string]any{"data": map[string]any{"versions": map[string]any{
				name: map[string]string{"created_time": "2026-09-22T10:00:00Z"},
			}}})
		})
		if got, err := c.Versions(t.Context(), "run-1/x"); err == nil {
			t.Errorf("Versions accepted the version name %q as %+v", name, got)
		}
	}
}

// TestVersionsReadsMetadataAndSorts covers the out-of-band observer. Sorting matters because the
// response is a JSON object and Go's map iteration is deliberately unordered, so an unsorted
// result would make two reads of the same key produce different evidence.
func TestVersionsReadsMetadataAndSorts(t *testing.T) {
	c, rec := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"versions": map[string]any{
					"2": map[string]string{"created_time": "2026-09-22T10:01:00Z"},
					"1": map[string]string{"created_time": "2026-09-22T10:00:00Z"},
					"3": map[string]string{"created_time": "2026-09-22T10:02:00Z"},
				},
			},
		})
	})

	versions, err := c.Versions(t.Context(), "run-1/machine.ca.key")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if !strings.HasPrefix(rec.last().path, "/v1/secret/metadata/") {
		t.Errorf("Versions read %q, not the metadata path", rec.last().path)
	}
	if len(versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions))
	}
	for i, v := range versions {
		if v.Number != i+1 {
			t.Errorf("version %d is numbered %d; the result is not sorted", i, v.Number)
		}
	}
	if want := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC); !versions[0].Created.Equal(want) {
		t.Errorf("version 1 created at %s, want %s", versions[0].Created, want)
	}
}

// TestTransitRoundTrip covers encrypt and decrypt against a stand-in that behaves the way transit
// does, including a fresh nonce per call.
func TestTransitRoundTrip(t *testing.T) {
	var calls int
	c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/transit/encrypt/") {
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Plaintext string `json:"plaintext"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			t.Errorf("the client sent an undecodable encrypt body: %v", err)
		}
		// A nonce per call, as transit does: the same input encrypts differently each time.
		calls++
		respond(t, w, http.StatusOK, map[string]any{
			"data": map[string]string{"ciphertext": fmt.Sprintf("vault:v1:nonce%d:%s", calls, in.Plaintext)},
		})
	})

	first, err := c.Encrypt(t.Context(), TransitKey, []byte("configuration bytes"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := c.Encrypt(t.Context(), TransitKey, []byte("configuration bytes"))
	if err != nil {
		t.Fatalf("Encrypt (again): %v", err)
	}

	// The property the corrected acceptance criterion rests on: identical input, different
	// ciphertext. A byte-for-byte comparison across runs would fail for a correct implementation.
	if first == second {
		t.Error("two encryptions of identical input produced identical ciphertext; the stand-in " +
			"is not behaving like transit and this test proves nothing about the criterion")
	}
	// And the plaintext must have been base64 encoded on the way out, as transit requires.
	encoded := base64.StdEncoding.EncodeToString([]byte("configuration bytes"))
	if !strings.Contains(first, encoded) {
		t.Errorf("the client did not base64 encode the plaintext: %q", first)
	}
}

// TestDecryptDecodesBase64 checks the response side of transit's encoding.
func TestDecryptDecodesBase64(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{
			"data": map[string]string{
				"plaintext": base64.StdEncoding.EncodeToString([]byte("configuration bytes")),
			},
		})
	})

	got, err := c.Decrypt(t.Context(), TransitKey, "vault:v1:whatever")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != "configuration bytes" {
		t.Errorf("Decrypt returned %q", got)
	}
}

// TestDecryptRefusesUndecodablePlaintext checks a malformed response is an error rather than
// silently producing bytes that would then fail a digest comparison for the wrong reason.
func TestDecryptRefusesUndecodablePlaintext(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, http.StatusOK, map[string]any{"data": map[string]string{"plaintext": "not base64!!"}})
	})

	if _, err := c.Decrypt(t.Context(), TransitKey, "vault:v1:whatever"); err == nil {
		t.Fatal("Decrypt accepted an undecodable response")
	}
}

// TestNewRejectsIncompleteConfiguration checks a client cannot be built without the two things
// every request needs, so the failure is at construction rather than on the first call.
func TestNewRejectsIncompleteConfiguration(t *testing.T) {
	if _, err := New("", "token"); err == nil {
		t.Error("New accepted an empty address")
	}
	if _, err := New("http://127.0.0.1:58200", ""); err == nil {
		t.Error("New accepted an empty token")
	}
}

// TestFromEnvReadsTheFixturesVariables covers the environment contract, including the default
// address and the named error when the token is absent.
func TestFromEnvReadsTheFixturesVariables(t *testing.T) {
	t.Setenv(AddrEnv, "")
	t.Setenv(TokenEnv, "root-token")

	c, err := FromEnv(TokenEnv)
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !strings.Contains(c.Name(), DefaultAddr) {
		t.Errorf("Name() = %q, want it to use the default address", c.Name())
	}

	t.Setenv(TokenEnv, "")
	if _, err := FromEnv(TokenEnv); err == nil {
		t.Fatal("FromEnv accepted an unset token")
	} else if !strings.Contains(err.Error(), TokenEnv) {
		t.Errorf("the error does not name the variable to set: %v", err)
	}
}

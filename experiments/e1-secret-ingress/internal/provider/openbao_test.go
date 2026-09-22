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
	"testing"
	"time"
)

// The tests below run against a stand-in for OpenBao rather than a live one. What they pin is this
// program's half of the contract: the paths it calls, the token header it sends, the shape it
// expects back, and — the one that matters most — that a failing request never puts what it was
// sending into an error string. The live provider is exercised by the experiment's matrix run
// against the fixtures, which is where a wrong assumption about OpenBao's API would surface.

// recorder captures what the client sent, so a test can assert the request rather than infer it.
type recorder struct {
	method string
	path   string
	token  string
	body   string
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
		rec.method, rec.path, rec.token, rec.body = r.Method, r.URL.Path, r.Header.Get("X-Vault-Token"), string(body)
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

	if rec.method != http.MethodPost {
		t.Errorf("method = %s, want POST", rec.method)
	}
	if want := "/v1/secret/data/run-1/machine.ca.key"; rec.path != want {
		t.Errorf("path = %q, want %q", rec.path, want)
	}
	if rec.token != "test-token" {
		t.Errorf("token header = %q", rec.token)
	}
	if !strings.Contains(rec.body, "private-key-value") {
		t.Errorf("the value did not reach the server: %q", rec.body)
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
	if !strings.Contains(rec.body, plaintext) {
		t.Fatalf("the request did not carry the plaintext, so this test proves nothing: %q", rec.body)
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
	if rec.method != http.MethodGet {
		t.Errorf("method = %s, want GET", rec.method)
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
	if !strings.HasPrefix(rec.path, "/v1/secret/metadata/") {
		t.Errorf("Versions read %q, not the metadata path", rec.path)
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

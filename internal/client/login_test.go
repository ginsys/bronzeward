package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestDevicePromptIsEscaped: the device prompt shows the user code, which the identity provider
// chose, escaped, and the prompt keeps the lines of a plain code. A verification address with a
// control character is refused, and the refusal shows it escaped (client-contracts.md section 6
// item 8). The guard on standard error escapes ESC; only the line feed shows a missing escape here.
func TestDevicePromptIsEscaped(t *testing.T) {
	prompt := func(userCode, verification string) (string, error) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/token" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			v := verification
			if v == "" {
				v = "http://" + r.Host + "/activate"
			}
			b, _ := json.Marshal(map[string]any{"device_code": "BWSYNTH-device", "user_code": userCode, "verification_uri": v, "expires_in": 5, "interval": 1})
			_, _ = w.Write(b)
		}))
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		i := &idp{ctx: ctx, hc: srv.Client(), conf: oauth2.Config{ClientID: "bronzewardctl",
			Endpoint: oauth2.Endpoint{DeviceAuthURL: srv.URL + "/device", TokenURL: srv.URL + "/token"}}}
		var b strings.Builder
		_, err := i.device(&Context{Name: "lab", Server: srv.URL, OIDC: &OIDC{Issuer: srv.URL}}, &b)
		return b.String(), err
	}
	plain, _ := prompt("BWSY-NTH1", "")
	hostile, _ := prompt("BW\x1b[2J\nFORGED", "")
	if !strings.Contains(hostile, `BW\x1b[2J\x0aFORGED`) || strings.Count(hostile, "\n") != strings.Count(plain, "\n") || strings.Contains(hostile, "\nFORGED") {
		t.Errorf("the prompt %q does not show the code escaped on the lines of %q", hostile, plain)
	}
	_, err := prompt("BWSY-NTH1", "http://bw.example.invalid/\x1b[2J\nFORGED")
	if err == nil || strings.Contains(Shown(err), "\n") || strings.Contains(Shown(err), "\x1b") {
		t.Errorf("a verification address with a control character: %v; want a refusal that shows on one line, escaped", err)
	}
}

// TestDeviceAuthExpiryFromRequest checks that a device code's lifetime counts from before the
// request (#444), as golang.org/x/oauth2 counted it: an answer that arrives late does not lengthen
// the polling window past the lifetime the provider gave.
func TestDeviceAuthExpiryFromRequest(t *testing.T) {
	const delay, lifetime = 1500 * time.Millisecond, 2 * time.Second
	received := make(chan time.Time, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- time.Now()
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"BWSYNTH-device","user_code":"BWSY-NTH1","verification_uri":"` +
			"http://" + r.Host + `/activate","expires_in":2,"interval":1}`))
	}))
	defer srv.Close()
	i := &idp{ctx: context.Background(), hc: srv.Client(),
		conf: oauth2.Config{ClientID: "bronzewardctl", Endpoint: oauth2.Endpoint{DeviceAuthURL: srv.URL + "/device"}}}
	before := time.Now()
	a, err := i.deviceAuth(&Context{OIDC: &OIDC{Issuer: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(before); took < delay {
		t.Fatalf("the answer took %s; the test needs at least %s", took, delay)
	}
	// The expiry counts from a time between the call's start and the provider receiving the
	// request. An expiry counted from the answer is at least the delay later, so it fails, and a
	// pause before the request is sent moves both bounds, so it does not.
	got := <-received
	if a.Expiry.Before(before.Add(lifetime)) || a.Expiry.After(got.Add(lifetime)) {
		t.Errorf("expiry %s after the call began and %s after the provider received the request; want at least %s after the first and at most %s after the second",
			a.Expiry.Sub(before), a.Expiry.Sub(got), lifetime, lifetime)
	}
}

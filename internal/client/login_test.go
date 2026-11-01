package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestDeviceAuthExpiryFromRequest checks that a device code's lifetime counts from before the
// request (#444), as golang.org/x/oauth2 counted it: an answer that arrives late does not lengthen
// the polling window past the lifetime the provider gave.
func TestDeviceAuthExpiryFromRequest(t *testing.T) {
	const delay, lifetime, slack = 1500 * time.Millisecond, 2 * time.Second, 500 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	// The expiry is at least the lifetime after the call began, and at most the lifetime plus a
	// margin shorter than the delay, so an expiry counted from the answer fails.
	if window := a.Expiry.Sub(before); window < lifetime || window > lifetime+slack {
		t.Errorf("polling window %s from the request; want %s to %s", window, lifetime, lifetime+slack)
	}
}

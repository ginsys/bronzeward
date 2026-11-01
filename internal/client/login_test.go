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

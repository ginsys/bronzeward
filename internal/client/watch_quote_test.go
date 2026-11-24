package client

import (
	"net/http"
	"strings"
	"testing"
)

// TestWatchRedirectQuotesItsLocation: the watch's refusal to follow a redirect puts the remote
// Location between double quotation marks, so a quote inside it is written \x22, as every message
// that quotes a remote value does (client-contracts.md §6 item 8), never as Go's \". A control
// character cannot be in this Location: the HTTP client refuses to parse one before the message is
// built, and the control characters are tested where the value is a string (the onboard cases).
func TestWatchRedirectQuotesItsLocation(t *testing.T) {
	const location = "https://BWSYNTH.example.invalid/a\"b"
	o := &opServer{
		epoch:  "ep_BWSYNTH1",
		states: []string{"running"},
		streams: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusTemporaryRedirect)
		}},
	}
	_, _, _, _, err := watched(t, o, "ep_BWSYNTH1", nil)
	if err == nil {
		t.Fatal("a redirect from the stream ended the watch with no error")
	}
	got := err.Error()
	want := `redirecting to "https://BWSYNTH.example.invalid/a\x22b"; bronzewardctl follows no redirect`
	if !strings.Contains(got, want) {
		t.Errorf("the redirect message is %q; want it to contain %q", got, want)
	}
}

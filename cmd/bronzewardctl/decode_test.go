package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDecodeWholeBody checks that a read refuses data after the first JSON value, as json.Unmarshal
// does, and still accepts trailing space and a newline.
func TestDecodeWholeBody(t *testing.T) {
	for _, c := range []struct {
		name, suffix string
		ok           bool
	}{
		{"a second object", `{}`, false},
		{"text", ` x`, false},
		{"a newline", "\n", true},
		{"space and newlines", " \n\t\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/server":
					fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":3,"startMode":"normal"}`)
				case "/api/v1/machines/mch_a":
					fmt.Fprint(w, `{"id":"mch_a"}`+c.suffix)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			automationContext(t, srv.URL)
			var out, errb bytes.Buffer
			code := run([]string{"machine", "show", "mch_a", "-o", "json"}, &out, &errb)
			if c.ok && (code != 0 || !strings.Contains(out.String(), "mch_a")) {
				t.Errorf("body with %s after the value: exit %d; want 0 and the machine:\n%s%s", c.name, code, out.String(), errb.String())
			}
			if !c.ok && (code == 0 || errb.Len() == 0 || out.Len() != 0) {
				t.Errorf("body with %s after the value: exit %d; want a refusal and no output:\n%s%s", c.name, code, out.String(), errb.String())
			}
		})
	}
}

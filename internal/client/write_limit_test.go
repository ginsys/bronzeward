package client

import (
	"net/http"
	"strings"
	"testing"
)

// TestSendRefusesABodyOverTheLimit: client-contracts.md §8.1 says a body over the server's 1 MiB
// limit is refused before it is sent, for every input. Send is the one place every write passes, so
// a body a flag built, which no input reader measured, is refused there with nothing sent.
func TestSendRefusesABodyOverTheLimit(t *testing.T) {
	const limit = 1 << 20
	for _, c := range []struct {
		name    string
		size    int
		refused bool
	}{
		{"one byte over", limit + 1, true},
		{"exactly the limit", limit, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
			cl, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
			w := clusterWrite()
			w.Body = []byte(`"` + strings.Repeat("a", c.size-2) + `"`)
			_, err := cl.Send(w)
			if !c.refused {
				if err != nil {
					t.Fatalf("a body of %d bytes: %v; want it sent", c.size, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "over the server's 1 MiB body limit") {
				t.Fatalf("a body of %d bytes: error %v; want it to say it is over the server's 1 MiB body limit", c.size, err)
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("a refused body reached the server: %v", got)
			}
		})
	}
}

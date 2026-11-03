package client

import (
	"fmt"
	"net/http"
	"testing"
)

// TestPageTail: the tail of a timeline answer is kept as sent, and is empty when the server sends
// none, as every other list's answer does.
func TestPageTail(t *testing.T) {
	for _, c := range []struct{ body, tail string }{
		{`{"items":[],"tail":"t7"}`, "t7"},
		{`{"items":[]}`, ""},
	} {
		f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, c.body) }}
		cl, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
		p, err := cl.Page("/machines/mch_a/timeline", "")
		if err != nil {
			t.Fatal(err)
		}
		if p.Tail != c.tail {
			t.Errorf("answer %s: Tail %q, want %q", c.body, p.Tail, c.tail)
		}
	}
}

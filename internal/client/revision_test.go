package client

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestMinimumRevision: a server at API revision 2 is refused with the message that names revision 3,
// the lowest whose machine timeline serves order and tail (client-contracts.md §8.6 item 6); a server
// at revision 3 is accepted.
func TestMinimumRevision(t *testing.T) {
	if MinRevision != 3 {
		t.Fatalf("MinRevision = %d, want 3", MinRevision)
	}
	for _, c := range []struct {
		revision int
		want     string // in the error, or "" for none
	}{
		{2, "the server serves API revision 2; this bronzewardctl needs revision 3 or later"},
		{3, ""},
	} {
		f := &fake{epoch: "ep_BWSYNTH-1", server: fmt.Sprintf(`{"version":"BWSYNTH","apiRevision":%d,"startMode":"normal"}`, c.revision)}
		cl, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
		_, _, err := cl.Server()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("revision %d: %v, want it accepted", c.revision, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("revision %d: error %v, want %q", c.revision, err, c.want)
		}
	}
}

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

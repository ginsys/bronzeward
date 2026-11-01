package client

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func entryWrite() *Write {
	return &Write{Method: http.MethodPost, Path: "/recovery/entries", Body: []byte(`{"reason":"BWSYNTH"}`), HumanOnly: true, Entry: true,
		Check: "recovery status", Read: func(*Client) (string, error) { return "BWSYNTH shows", nil }}
}

func entered(epoch string, replayed bool) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Bronzeward-Epoch", epoch)
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"epoch":%q,"previousEpoch":"ep_BWSYNTH-1"}`, epoch)
	}
}

// TestEntry checks §8.4 item 3's exception for the recovery entry: no Bronzeward-Expected-Epoch on
// any send, a repeat under the same key that is not set aside on the epoch the entry minted
// (REC-04 (a)), and the start mode its answers show (§8.5; REC-02).
func TestEntry(t *testing.T) {
	t.Run("lost 201", func(t *testing.T) {
		// The first answer is lost after the commit; the confirming read shows the new epoch.
		f := &fake{epoch: "ep_BWSYNTH-1", beforeEntry: true, recovery: func(int) string { return "ep_BWSYNTH-2" },
			post: func(n int, w http.ResponseWriter, r *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				entered("ep_BWSYNTH-2", true)(n, w, r)
			}}
		c, notices, _ := writeClient(t, f, "ep_BWSYNTH-1")
		r, err := c.Send(entryWrite())
		if err != nil {
			t.Fatalf("%v\n%s", err, notices)
		}
		posts := f.postsSeen()
		if len(posts) != 2 || posts[0].Header.Get("Idempotency-Key") != posts[1].Header.Get("Idempotency-Key") {
			t.Fatalf("%d posts, or the repeat changed its key", len(posts))
		}
		for i, q := range posts {
			if v := q.Header.Values("Bronzeward-Expected-Epoch"); v != nil {
				t.Errorf("post %d carried Bronzeward-Expected-Epoch %q", i, v)
			}
		}
		if !strings.Contains(string(r.Body), "ep_BWSYNTH-2") || c.Context.LastEpoch != "ep_BWSYNTH-2" || c.Context.StartMode != "recovery" {
			t.Errorf("answer %s, epoch %q, start mode %q", r.Body, c.Context.LastEpoch, c.Context.StartMode)
		}
		if !strings.Contains(notices.String(), "already done earlier") {
			t.Errorf("the replay was not shown:\n%s", notices)
		}
	})
	for _, tc := range []struct {
		name, extra, mode, shown string
		before                   bool
	}{
		{"normal start", "", "normal", "This server was started normally; only a server started for recovery serves the entry.", false},
		{"another key's entry", `,"epoch":"ep_BWSYNTH-3"`, "recovery", "Another recovery entry committed first (epoch ep_BWSYNTH-3)", false},
		{"an earlier record", `,"epoch":"ep_BWSYNTH-0","request":"req_BWSYNTH"`, "recovery", "Another recovery entry committed first (epoch ep_BWSYNTH-0)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{epoch: "ep_BWSYNTH-1", beforeEntry: tc.before, post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusConflict, "conflict", tc.extra)
			}}
			c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
			c.Context.StartMode = "unknown-BWSYNTH"
			_, err := c.Send(entryWrite())
			var u *Unknown
			if err == nil || errors.As(err, &u) || !strings.Contains(err.Error(), tc.shown) {
				t.Errorf("err %v, want a refusal saying %q", err, tc.shown)
			}
			if c.Context.StartMode != tc.mode || len(f.postsSeen()) != 1 {
				t.Errorf("start mode %q, %d posts; want %q and 1", c.Context.StartMode, len(f.postsSeen()), tc.mode)
			}
		})
	}
	t.Run("forbidden shows no mode", func(t *testing.T) {
		// The 403 carries Bronzeward-Epoch, as one found at the role check does; GET /server showed a
		// normal start, and the 403 does not make it a recovery start (§8.5 item 1).
		f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) {
			problemAnswer(w, http.StatusForbidden, "forbidden", `,"roles":["recovery-admin"]`)
		}}
		c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
		if _, err := c.Send(entryWrite()); err == nil || c.Context.StartMode != "normal" {
			t.Errorf("err %v, start mode %q", err, c.Context.StartMode)
		}
	})
	t.Run("operator repeat", func(t *testing.T) {
		f := &fake{epoch: "ep_BWSYNTH-1", beforeEntry: true, post: entered("ep_BWSYNTH-2", true)}
		c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
		w := entryWrite()
		w.Repeat = &Repeat{Key: "BWSYNTH-" + strings.Repeat("k", 14), Principal: "svc_BWSYNTH"}
		if _, err := c.Send(w); err != nil {
			t.Fatal(err)
		}
		// No confirming read: the entry's repeat checks the principal and not the epoch (§8.4 item 4).
		want := []string{"GET /api/v1/identity", "GET /api/v1/server", "POST /api/v1/recovery/entries"}
		if got := f.requests(); !slices.Equal(got, want) || f.postsSeen()[0].Header.Get("Idempotency-Key") != w.Repeat.Key {
			t.Errorf("requests %v, want %v under the given key", got, want)
		}
		w.Repeat.Principal = "svc_BWSYNTH-other"
		var refused RefusedRepeat
		if _, err := c.Send(w); !errors.As(err, &refused) || len(f.postsSeen()) != 1 {
			t.Errorf("another principal: err %v, %d posts", err, len(f.postsSeen()))
		}
	})
	t.Run("block", func(t *testing.T) {
		f := &fake{epoch: "ep_BWSYNTH-1", beforeEntry: true, post: func(_ int, w http.ResponseWriter, _ *http.Request) { drop(w) }}
		c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
		_, err := c.Send(entryWrite())
		var u *Unknown
		if !errors.As(err, &u) || u.SetAside || len(f.postsSeen()) != 4 {
			t.Fatalf("err %v, %d posts; want an unknown outcome after 4", err, len(f.postsSeen()))
		}
		s := u.Error()
		if !strings.Contains(s, "epoch:     none: the recovery entry is sent without one") || strings.Contains(s, "--expect-epoch") ||
			!strings.Contains(s, "--idempotency-key "+u.Key+" --expect-principal 'svc_BWSYNTH'") {
			t.Errorf("block:\n%s", s)
		}
	})
}

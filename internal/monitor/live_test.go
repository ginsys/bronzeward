package monitor

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// live is one test's OpenBao and the fixture whose dependency records name objects created in it.
type live struct {
	b *baotest.Bao
	f *fixture
	m *Monitor
}

// admin sends one request as the administrator and fails the test unless the answer is 2xx.
func (l *live) admin(t *testing.T, method, path string, in any) []byte {
	t.Helper()
	status, body, err := l.b.Do(l.b.Admin(), method, path, in)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if status/100 != 2 {
		t.Fatalf("%s %s: status %d: %s", method, path, status, body)
	}
	return body
}

// newLive creates a KV secret at the fixture's generation path and a Transit key of this test's own
// name, seeds the fixture with their version 1 and its creation time as OpenBao reports them, and
// builds a monitor reading them through the metadata client under the fixture's bw-metadata policy.
// The objects are removed when the test ends.
func newLive(t *testing.T) *live {
	t.Helper()
	l := &live{b: baotest.New(t)}
	l.f = seedWith(t, func(f *fixture) {
		l.f = f
		l.admin(t, http.MethodPost, "/v1/secret/data/"+f.kv, map[string]any{"data": map[string]string{"value": "synthetic-live"}})
		t.Cleanup(func() { _, _, _ = l.b.Do(l.b.Admin(), http.MethodDelete, "/v1/secret/metadata/"+f.kv, nil) })
		var kv struct {
			Data struct {
				Versions map[string]struct {
					Created string `json:"created_time"`
				} `json:"versions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(l.admin(t, http.MethodGet, "/v1/secret/metadata/"+f.kv, nil), &kv); err != nil {
			t.Fatal(err)
		}
		f.kvCreated = kv.Data.Versions["1"].Created
		f.key = l.b.Name("artifact")
		l.admin(t, http.MethodPost, "/v1/transit/keys/"+f.key, nil)
		t.Cleanup(func() {
			_, _, _ = l.b.Do(l.b.Admin(), http.MethodPost, "/v1/transit/keys/"+f.key+"/config", map[string]any{"deletion_allowed": true})
			_, _, _ = l.b.Do(l.b.Admin(), http.MethodDelete, "/v1/transit/keys/"+f.key, nil)
		})
		f.keyCreated = l.keyVersion(t, 1).UTC().Format(time.RFC3339)
	})
	meta, err := provider.NewMetadata(l.b.Addr, tokenFile(t, l.b.Token("bw-metadata")))
	if err != nil {
		t.Fatal(err)
	}
	lg := &logs{}
	l.m = New(l.f.db, meta, Defaults(), lg.logf, io.Discard)
	l.step(t, "the first pass", l.f.depKV, "retained", "")
	if got := statusOf(t, l.f.db, l.f.depKey); got.class != "retained" {
		t.Fatalf("the Transit key version on the first pass: %s %s, want retained", got.class, got.reason.String)
	}
	return l
}

// keyVersion is the creation time the Transit key's keys map gives version v.
func (l *live) keyVersion(t *testing.T, v int) time.Time {
	t.Helper()
	var key struct {
		Data struct {
			Keys map[string]int64 `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(l.admin(t, http.MethodGet, "/v1/transit/keys/"+l.f.key, nil), &key); err != nil {
		t.Fatal(err)
	}
	unix, ok := key.Data.Keys[strconv.Itoa(v)]
	if !ok {
		t.Fatalf("transit key %s has no version %d", l.f.key, v)
	}
	return time.Unix(unix, 0)
}

// secondTurns waits until the clock is past the second that holds now. OpenBao's Date header has
// one-second resolution, and the classifier cannot order a deletion within the answer's own second
// (deletion-time-undecidable), so a pass that should see a soft delete as past waits for it.
func secondTurns() {
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(1100 * time.Millisecond)))
}

// step runs one pass and requires dep's recorded class and reason.
func (l *live) step(t *testing.T, what, dep, class, reason string) {
	t.Helper()
	pass(t, l.m)
	got := statusOf(t, l.f.db, dep)
	if got.class != class || got.reason.String != reason {
		t.Fatalf("%s: %s %q, want %s %q", what, got.class, got.reason.String, class, reason)
	}
}

// alerted requires dep's alerts, in recording order, to be exactly kinds (§6.2: each alert as the
// table sets and no other).
func (l *live) alerted(t *testing.T, dep string, want ...string) {
	t.Helper()
	if got := kinds(alerts(t, l.f.db, dep)); !slices.Equal(got, want) {
		t.Fatalf("alerts %v, want %v", got, want)
	}
}

// Dependency monitor §10.1 items 2 and 3, against a real OpenBao through the metadata client under
// the fixture's bw-metadata policy: each provider state an object can be put in classifies as §3's
// table sets, and alerts as §6.2 sets and no other. The whole-server states (sealed, partitioned,
// paused) are acceptance-plan S2's, on the fixture. Controls: each case first records the object
// retained, so a classifier that ignored the change would leave it retained and fail the case.
func TestLiveProviderStates(t *testing.T) {
	t.Run("KV soft-deleted, undeleted, destroyed", func(t *testing.T) {
		l := newLive(t)
		dep, versions := l.f.depKV, map[string]any{"versions": []int{1}}
		l.admin(t, http.MethodPost, "/v1/secret/delete/"+l.f.kv, versions)
		secondTurns()
		l.step(t, "soft-deleted", dep, "blocked", "soft-deleted")
		l.admin(t, http.MethodPost, "/v1/secret/undelete/"+l.f.kv, versions)
		l.step(t, "undeleted", dep, "retained", "")
		l.admin(t, http.MethodPut, "/v1/secret/destroy/"+l.f.kv, versions)
		l.step(t, "destroyed", dep, "lost", "destroyed")
		l.alerted(t, dep, "blocked", "lost")
	})
	t.Run("KV pruned past max_versions", func(t *testing.T) {
		l := newLive(t)
		l.admin(t, http.MethodPost, "/v1/secret/metadata/"+l.f.kv, map[string]any{"max_versions": 1})
		l.admin(t, http.MethodPost, "/v1/secret/data/"+l.f.kv, map[string]any{"data": map[string]string{"value": "synthetic-live-2"}})
		l.step(t, "pruned", l.f.depKV, "lost", "pruned")
		l.alerted(t, l.f.depKV, "lost")
	})
	t.Run("KV metadata deleted after retained", func(t *testing.T) {
		// Item 3: a 404 after retained raises regression on the next pass, and only then: a later
		// pass over the same 404 raises nothing until persistent.
		l := newLive(t)
		l.admin(t, http.MethodDelete, "/v1/secret/metadata/"+l.f.kv, nil)
		l.step(t, "metadata deleted", l.f.depKV, "unknown", "absent")
		l.step(t, "still deleted", l.f.depKV, "unknown", "absent")
		l.alerted(t, l.f.depKV, "regression")
	})
	t.Run("KV metadata deleted without the seed", func(t *testing.T) {
		// Item 3's control: a dependency with no §5.2 seed, first recorded unknown and never
		// retained, raises nothing on the 404 until persistent.
		l := newLive(t)
		exec(t, l.f.db, `UPDATE dependency_status SET class = 'unknown', reason = 'unreachable', first_retained_at = NULL,
			unknown_since = now() WHERE id = $1`, l.f.depKV)
		l.admin(t, http.MethodDelete, "/v1/secret/metadata/"+l.f.kv, nil)
		l.step(t, "metadata deleted", l.f.depKV, "unknown", "absent")
		l.alerted(t, l.f.depKV)
		exec(t, l.f.db, `UPDATE dependency_status SET unknown_since = unknown_since - interval '15 minutes' WHERE id = $1`, l.f.depKV)
		l.step(t, "15 minutes unknown", l.f.depKV, "unknown", "absent")
		l.alerted(t, l.f.depKV, "persistent")
	})
	t.Run("Transit below the decryption floor, lowered, then trimmed", func(t *testing.T) {
		l := newLive(t)
		dep, config := l.f.depKey, "/v1/transit/keys/"+l.f.key+"/config"
		l.admin(t, http.MethodPost, "/v1/transit/keys/"+l.f.key+"/rotate", nil)
		l.admin(t, http.MethodPost, config, map[string]any{"min_decryption_version": 2})
		l.step(t, "below the decryption floor", dep, "unknown", "below-decryption-floor-unverified")
		l.admin(t, http.MethodPost, config, map[string]any{"min_decryption_version": 1})
		l.step(t, "the floor lowered", dep, "retained", "")
		l.admin(t, http.MethodPost, config, map[string]any{"min_decryption_version": 2, "min_encryption_version": 2})
		l.admin(t, http.MethodPost, "/v1/transit/keys/"+l.f.key+"/trim", map[string]any{"min_available_version": 2})
		l.step(t, "trimmed", dep, "unknown", "trimmed-unverified")
		l.alerted(t, dep, "regression", "regression")
	})
	t.Run("Transit key deleted", func(t *testing.T) {
		l := newLive(t)
		l.admin(t, http.MethodPost, "/v1/transit/keys/"+l.f.key+"/config", map[string]any{"deletion_allowed": true})
		l.admin(t, http.MethodDelete, "/v1/transit/keys/"+l.f.key, nil)
		l.step(t, "key deleted", l.f.depKey, "unknown", "absent")
		l.step(t, "still deleted", l.f.depKey, "unknown", "absent")
		l.alerted(t, l.f.depKey, "regression")
	})
}

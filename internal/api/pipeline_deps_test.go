package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

// pinHeld is the stand-in value the readers hold; no refusal or log line may quote it.
const pinHeld = "pin-held-Q4w"

var pinDate = time.Date(2026, 9, 26, 9, 5, 0, 0, time.UTC)

// kvAnswer is a KV v2 metadata answer for versions 1..n, version i created at created[i-1];
// destroyed versions are marked so.
func kvAnswer(destroyed map[int]bool, created ...time.Time) classify.Answer {
	versions := map[string]any{}
	for i, c := range created {
		versions[strconv.Itoa(i+1)] = map[string]any{"created_time": c.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": destroyed[i+1]}
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"current_version": len(created), "oldest_version": 0, "versions": versions}})
	return classify.Answer{Status: http.StatusOK, Date: pinDate.Format(http.TimeFormat), Body: classify.NewBody(b)}
}

// standInMeta answers KV by path, or fails with err, and counts the requests.
type standInMeta struct {
	kv    map[string]classify.Answer
	err   error
	calls *int
}

func (m standInMeta) KV(_ context.Context, p provider.GenerationPath) (classify.Answer, error) {
	*m.calls++
	if m.err != nil {
		return classify.Answer{}, m.err
	}
	return m.kv[p.String()], nil
}

func (standInMeta) Transit(context.Context, string) (classify.Answer, error) {
	return classify.Answer{}, errors.New("stand-in: no transit")
}

// standInReader gives pinHeld with the creation time it holds by path, or fails with err.
type standInReader struct {
	created map[string]time.Time
	err     error
	calls   *int
}

func (r standInReader) ReadGeneration(_ context.Context, p provider.GenerationPath, _ int64) (provider.Value, time.Time, error) {
	*r.calls++
	if r.err != nil {
		return provider.Value{}, time.Time{}, r.err
	}
	v, err := provider.NewValue(provider.KindString, pinHeld)
	return v, r.created[p.String()], err
}

func (p *publishEnv) readPins(m standInMeta, r standInReader) (pinned, *refusal, error) {
	p.t.Helper()
	s, ref := p.snapshot()
	if ref != nil {
		p.t.Fatalf("snapshot refused: %v", ref)
	}
	return p.a.readPinned(p.t.Context(), p.job, s, m, r)
}

func decoded(t *testing.T, v provider.Value) any {
	t.Helper()
	d, err := v.Decode()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// compilation.md §6 steps 2-4: every pin of the release is classified and read once, each source
// gets the value under its own reference name, and the classification is the status publication
// seeds, with a database time no later than its request began (ruling R32).
func TestReadPinnedRetained(t *testing.T) {
	p := newPublishEnv(t)
	// A composed fragment pins the import base's generation version under another name; a
	// revision's references are written in the transaction that writes it.
	network := id.New(id.FragmentRevision)
	tx, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, 'network', 'site', 'machine: {}', $3, '[]', now())`, network, p.cluster, p.seed); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO fragment_reference (revision, name, kind, version, generation)
		VALUES ($1, 'other/pass', 'string', 1, $2)`, network, p.kvPath); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p.entry("fragment", "network", network)
	var metaCalls, reads int
	var before time.Time
	if err := p.db.QueryRow(`SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, ref, err := p.readPins(standInMeta{kv: map[string]classify.Answer{p.kvPath: kvAnswer(nil, kvCreated)}, calls: &metaCalls},
		standInReader{created: map[string]time.Time{p.kvPath: kvCreated}, calls: &reads})
	if err != nil || ref != nil {
		t.Fatalf("readPinned: %v %v", ref, err)
	}
	if metaCalls != 1 || reads != 1 {
		t.Fatalf("%d classifications, %d reads; want one each", metaCalls, reads)
	}
	for _, at := range []struct{ source, name string }{{p.ibr, "registry/pass"}, {network, "other/pass"}} {
		v, ok := got.values[at.source][at.name]
		if !ok || decoded(t, v) != pinHeld {
			t.Fatalf("source %s reference %s: no pinned value", at.source, at.name)
		}
	}
	if len(got.values) != 2 {
		t.Fatalf("values for %d sources, want 2", len(got.values))
	}
	if c := got.created[pinKey{p.kvPath, 1}]; !c.Equal(kvCreated) {
		t.Fatalf("created %v, want %v", c, kvCreated)
	}
	if len(got.statuses) != 1 {
		t.Fatalf("%d statuses, want 1", len(got.statuses))
	}
	s := got.statuses[0]
	if s.provider != classify.KV || s.object != p.kvPath || s.version != 1 || s.result.Class != classify.Retained ||
		!s.result.Created.Equal(kvCreated) || s.began.Before(before) || s.began.After(time.Now()) {
		t.Fatalf("status %+v (began not in [%v, now])", s, before)
	}
}

// A composition without references reads nothing.
func TestReadPinnedNone(t *testing.T) {
	p := newPublishEnv(t)
	ibr := id.New(id.ImportBase)
	mustExec(t, p.db, `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '[]', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
		ibr, p.machine, make([]byte, 32))
	mustExec(t, p.db, `UPDATE draft_entry SET import_base_revision = $1 WHERE draft = $2 AND machine = $3`, ibr, p.draft, p.machine)
	var metaCalls, reads int
	got, ref, err := p.readPins(standInMeta{calls: &metaCalls}, standInReader{calls: &reads})
	if err != nil || ref != nil || metaCalls+reads != 0 || len(got.statuses) != 0 {
		t.Fatalf("readPinned: %v %v, %d calls, %d statuses", ref, err, metaCalls+reads, len(got.statuses))
	}
}

// Ruling R33: a dependency not retained, or one whose identity changed, refuses 422 naming it; a
// classification the provider's state leaves undecided, or a provider it cannot reach, refuses
// 503; any other failure is the caller's error. None quotes a value.
func TestReadPinnedRefuses(t *testing.T) {
	later := kvCreated.Add(time.Second)
	unreachable := fmt.Errorf("reading: %w", provider.ErrUnavailable)
	cases := []struct {
		name     string
		meta     map[string]classify.Answer
		metaErr  error
		created  time.Time // the read's created_time
		readErr  error
		recorded bool // a status recorded the version at a later created_time
		status   int
		code     string
	}{
		{name: "lost", meta: map[string]classify.Answer{"": kvAnswer(map[int]bool{1: true}, kvCreated)}, created: kvCreated,
			status: 422, code: "validation-failed"},
		{name: "recorded identity changed", meta: map[string]classify.Answer{"": kvAnswer(nil, kvCreated)}, created: kvCreated,
			recorded: true, status: 422, code: "validation-failed"},
		{name: "read identity changed", meta: map[string]classify.Answer{"": kvAnswer(nil, kvCreated)}, created: later,
			status: 422, code: "validation-failed"},
		{name: "undecided", meta: map[string]classify.Answer{"": {Unreachable: true}}, created: kvCreated,
			status: 503, code: "dependency-unavailable"},
		{name: "classification unreachable", metaErr: unreachable, status: 503, code: "dependency-unavailable"},
		{name: "read unreachable", meta: map[string]classify.Answer{"": kvAnswer(nil, kvCreated)}, readErr: unreachable,
			status: 503, code: "dependency-unavailable"},
		{name: "read failed", meta: map[string]classify.Answer{"": kvAnswer(nil, kvCreated)}, readErr: errors.New("stand-in: refused")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPublishEnv(t)
			if c.recorded {
				mustExec(t, p.db, `INSERT INTO dependency_status (id, provider, object, version, created, class, observed_from, recorded_at)
					VALUES ($1, 'kv', $2, 1, $3, 'retained', now(), now())`, id.New(id.Dependency), p.kvPath, createdText(later))
			}
			meta := map[string]classify.Answer{}
			for _, a := range c.meta {
				meta[p.kvPath] = a
			}
			var metaCalls, reads int
			_, ref, err := p.readPins(standInMeta{kv: meta, err: c.metaErr, calls: &metaCalls},
				standInReader{created: map[string]time.Time{p.kvPath: c.created}, err: c.readErr, calls: &reads})
			if c.status == 0 {
				if err == nil || ref != nil {
					t.Fatalf("readPinned: %v %v; want an error", ref, err)
				}
				return
			}
			if err != nil || ref == nil || ref.status != c.status || ref.code != c.code {
				t.Fatalf("readPinned: %v %v; want %d %s", ref, err, c.status, c.code)
			}
			if d, _ := ref.extra["dependency"].(map[string]any); d == nil || d["object"] != p.kvPath || d["reference"] != "registry/pass" {
				t.Fatalf("refusal names %v", ref.extra["dependency"])
			}
			// PA §6.1: a 503 names the dependency with class unknown, an unreachable provider's too.
			if d, _ := ref.extra["dependency"].(map[string]any); c.status == 503 && d["class"] != string(classify.Unknown) {
				t.Fatalf("503 refusal names %v without class unknown", d)
			}
			if strings.Contains(fmt.Sprint(ref.extra)+ref.detail, pinHeld) || p.logged(pinHeld) {
				t.Fatal("a refusal or log line quotes the value")
			}
		})
	}
}

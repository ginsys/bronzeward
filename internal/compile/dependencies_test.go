package compile

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The role clients satisfy the interfaces the compiler asks them through.
var (
	_ MetadataReader    = (*provider.Metadata)(nil)
	_ PinnedReader      = (*provider.Compiler)(nil)
	_ ArtifactEncrypter = (*provider.Compiler)(nil)
)

// depSecret is the stand-in value the fakes hold; no error may quote it.
const depSecret = "dep-secret-Z8q"

var depDate = time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)

func dateHeader(t time.Time) string { return t.Format(http.TimeFormat) }

// kvBody is a KV v2 metadata answer whose versions 1..n were created at created[i-1], none
// deleted.
func kvBody(created ...time.Time) []byte {
	versions := map[string]any{}
	for i, c := range created {
		versions[strconv.Itoa(i+1)] = map[string]any{"created_time": c.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": false}
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"current_version": len(created), "oldest_version": 0, "versions": versions}})
	return b
}

// transitBody is a Transit key answer: versions 1..n created at created[i-1], with the floor.
func transitBody(minDecryption int, created ...time.Time) []byte {
	keys := map[string]int64{}
	for i, c := range created {
		keys[strconv.Itoa(i+1)] = c.Unix()
	}
	b, _ := json.Marshal(map[string]any{"data": map[string]any{
		"keys": keys, "latest_version": len(created), "min_available_version": 0,
		"min_decryption_version": minDecryption, "soft_deleted": false,
	}})
	return b
}

func ok(body []byte) classify.Answer {
	return classify.Answer{Status: http.StatusOK, Date: dateHeader(depDate), Body: classify.NewBody(body)}
}

// fakeMeta answers KV by path and Transit with its answers in turn, and records every request.
type fakeMeta struct {
	kv      map[string]classify.Answer
	transit []classify.Answer
	calls   *[]string
}

func (f fakeMeta) KV(_ context.Context, p provider.GenerationPath) (classify.Answer, error) {
	*f.calls = append(*f.calls, "kv "+p.String())
	a, ok := f.kv[p.String()]
	if !ok {
		return classify.Answer{}, errors.New("fake: no answer")
	}
	return a, nil
}

func (f fakeMeta) Transit(_ context.Context, key string) (classify.Answer, error) {
	*f.calls = append(*f.calls, "transit "+key)
	if len(f.transit) == 0 {
		return classify.Answer{}, errors.New("fake: no answer")
	}
	a := f.transit[0]
	copy(f.transit, f.transit[1:])
	f.transit[len(f.transit)-1] = classify.Answer{Unreachable: true}
	return a, nil
}

func genPath(t *testing.T) provider.GenerationPath {
	t.Helper()
	p, err := provider.NewGenerationPath(id.New(id.Cluster), id.New(id.Ingestion), provider.NewValueID())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func noValue(t *testing.T, err error) {
	t.Helper()
	if err != nil && (strings.Contains(err.Error(), depSecret) || strings.Contains(fmt.Sprintf("%+v", err), depSecret)) {
		t.Fatalf("the error quotes the value: %v", err)
	}
}

func asDependency(t *testing.T, err error) *DependencyError {
	t.Helper()
	noValue(t, err)
	var d *DependencyError
	if !errors.As(err, &d) {
		t.Fatalf("got %v, want a *DependencyError", err)
	}
	return d
}

func TestCheckPinned(t *testing.T) {
	created := depDate.Add(-time.Hour).Add(123456789 * time.Nanosecond)
	deletion := depDate.Add(24 * time.Hour)
	a, b := genPath(t), genPath(t)
	scheduled, _ := json.Marshal(map[string]any{"data": map[string]any{"current_version": 2, "oldest_version": 0, "versions": map[string]any{
		"1": map[string]any{"created_time": created.Add(-time.Hour).Format(time.RFC3339Nano), "deletion_time": "", "destroyed": false},
		"2": map[string]any{"created_time": created.Format(time.RFC3339Nano), "deletion_time": deletion.Format(time.RFC3339Nano), "destroyed": false},
	}}})
	var calls []string
	m := fakeMeta{kv: map[string]classify.Answer{
		a.String(): ok(kvBody(created)),
		b.String(): ok(scheduled),
	}, calls: &calls}
	got, err := CheckPinned(t.Context(), m, []Pin{{Reference: "a", Path: a, Version: 1}, {Reference: "b", Path: b, Version: 2}})
	if err != nil {
		t.Fatal(err)
	}
	// Each pin carries its whole classification: the status publication seeds (DM §5.2).
	want := []Pinned{
		{Pin: Pin{Reference: "a", Path: a, Version: 1}, Status: classify.Result{Class: classify.Retained, Reason: classify.None, Created: created, Date: depDate}},
		{Pin: Pin{Reference: "b", Path: b, Version: 2}, Status: classify.Result{Class: classify.Retained, Reason: classify.DeletionScheduled, Created: created, Deletion: deletion, Date: depDate}},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if !slices.Equal(calls, []string{"kv " + a.String(), "kv " + b.String()}) {
		t.Fatalf("requests %q", calls)
	}
}

func TestCheckPinnedRefuses(t *testing.T) {
	created := depDate.Add(-time.Hour)
	p := genPath(t)
	destroyed, _ := json.Marshal(map[string]any{"data": map[string]any{"current_version": 1, "oldest_version": 0, "versions": map[string]any{
		"1": map[string]any{"created_time": created.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": true},
	}}})
	softDeleted, _ := json.Marshal(map[string]any{"data": map[string]any{"current_version": 1, "oldest_version": 0, "versions": map[string]any{
		"1": map[string]any{"created_time": created.Format(time.RFC3339Nano), "deletion_time": depDate.Add(-time.Minute).Format(time.RFC3339Nano), "destroyed": false},
	}}})
	for _, c := range []struct {
		name     string
		answer   classify.Answer
		recorded time.Time
		class    classify.Class
		reason   classify.Reason
	}{
		{"destroyed", ok(destroyed), time.Time{}, classify.Lost, classify.Destroyed},
		{"soft-deleted", ok(softDeleted), time.Time{}, classify.Blocked, classify.SoftDeleted},
		{"denied", classify.Answer{Status: http.StatusForbidden, Body: classify.NewBody([]byte(depSecret))}, time.Time{}, classify.Unknown, classify.Denied},
		{"unreadable", classify.Answer{Status: http.StatusOK, Body: classify.NewBody([]byte(`{"data":"` + depSecret + `"}`))}, time.Time{}, classify.Unknown, classify.Unreadable},
		{"absent version", ok(kvBody()), time.Time{}, classify.Unknown, classify.InsufficientEvidence},
		{"recorded identity differs", ok(kvBody(created)), created.Add(time.Second), classify.Unknown, classify.IdentityMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			var calls []string
			m := fakeMeta{kv: map[string]classify.Answer{p.String(): c.answer}, calls: &calls}
			_, err := CheckPinned(t.Context(), m, []Pin{{Reference: "db", Path: p, Version: 1, Recorded: c.recorded}})
			d := asDependency(t, err)
			if d.Reference != "db" || d.Object != p.String() || d.Version != 1 || d.Class != c.class || d.Reason != string(c.reason) {
				t.Fatalf("got %+v", d)
			}
		})
	}
	// The recorded identity, when equal, is kept.
	var calls []string
	m := fakeMeta{kv: map[string]classify.Answer{p.String(): ok(kvBody(created))}, calls: &calls}
	if got, err := CheckPinned(t.Context(), m, []Pin{{Reference: "db", Path: p, Version: 1, Recorded: created}}); err != nil || !got[0].Status.Created.Equal(created) {
		t.Fatalf("a recorded identity the answer repeats: %+v, %v", got, err)
	}
	// A request error refuses, naming the reference.
	_, err := CheckPinned(t.Context(), fakeMeta{kv: map[string]classify.Answer{}, calls: &calls}, []Pin{{Reference: "db", Path: p, Version: 1}})
	if err == nil || !strings.Contains(err.Error(), "db") {
		t.Fatalf("a failed request: %v", err)
	}
}

// fakeReader returns its value and creation time for any pinned read, and records the reads.
type fakeReader struct {
	created map[string]time.Time
	err     error
	calls   *[]string
}

func (f fakeReader) ReadGeneration(_ context.Context, p provider.GenerationPath, version int64) (provider.Value, time.Time, error) {
	*f.calls = append(*f.calls, p.String()+"@"+strconv.FormatInt(version, 10))
	if f.err != nil {
		return provider.Value{}, time.Time{}, f.err
	}
	v, err := provider.NewValue(provider.KindString, depSecret)
	return v, f.created[p.String()], err
}

func TestReadPinned(t *testing.T) {
	created := depDate.Add(-time.Hour).Add(5 * time.Microsecond)
	a, b := genPath(t), genPath(t)
	pins := []Pinned{{Pin: Pin{Reference: "a", Path: a, Version: 3}, Status: classify.Result{Created: created}}, {Pin: Pin{Reference: "b", Path: b, Version: 1}, Status: classify.Result{Created: created}}}
	var calls []string
	got, err := ReadPinned(t.Context(), fakeReader{created: map[string]time.Time{a.String(): created, b.String(): created}, calls: &calls}, pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !slices.Equal(calls, []string{a.String() + "@3", b.String() + "@1"}) {
		t.Fatalf("%d values, reads %q", len(got), calls)
	}
	if v, err := got[0].Decode(); err != nil || v != depSecret {
		t.Fatalf("value 0: %v", err)
	}

	// A creation time other than step 3's: the path was deleted and written again.
	calls = nil
	_, err = ReadPinned(t.Context(), fakeReader{created: map[string]time.Time{a.String(): created, b.String(): created.Add(time.Microsecond)}, calls: &calls}, pins)
	d := asDependency(t, err)
	if d.Reference != "b" || d.Object != b.String() || d.Version != 1 || d.Reason != ReasonCreatedChanged {
		t.Fatalf("got %+v", d)
	}

	// A failed read refuses, naming the reference and wrapping the provider's error.
	_, err = ReadPinned(t.Context(), fakeReader{err: provider.ErrDenied, calls: &calls}, pins)
	noValue(t, err)
	if !errors.Is(err, provider.ErrDenied) || !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("a failed read: %v", err)
	}
}

// fakeEncrypter encrypts under the versions in turn (the last repeating), recording each call. Its
// key is "bw-artifact" unless set.
type fakeEncrypter struct {
	key      string
	versions []int
	err      error
	calls    *[]string
}

func (f fakeEncrypter) ArtifactKey() string {
	if f.key == "" {
		return "bw-artifact"
	}
	return f.key
}

func (f fakeEncrypter) EncryptArtifact(_ context.Context, plaintext []byte) (provider.Ciphertext, error) {
	n := strings.Count(strings.Join(*f.calls, ","), "encrypt")
	*f.calls = append(*f.calls, "encrypt")
	if f.err != nil {
		return "", f.err
	}
	v := f.versions[min(n, len(f.versions)-1)]
	return provider.Ciphertext(fmt.Sprintf("vault:v%d:%s", v, base64.StdEncoding.EncodeToString(plaintext))), nil
}

func artifacts(n int) []Materialized {
	out := make([]Materialized, n)
	for i := range out {
		s := "machine:\n  token: " + depSecret + strconv.Itoa(i) + "\n"
		out[i] = Materialized{s: &s}
	}
	return out
}

func TestEncryptArtifacts(t *testing.T) {
	v1, v2 := depDate.Add(-time.Hour), depDate.Add(-time.Second)
	var calls []string
	second := ok(transitBody(1, v1, v2))
	second.Date = dateHeader(depDate.Add(time.Second))
	m := fakeMeta{transit: []classify.Answer{ok(transitBody(1, v1, v2)), second}, calls: &calls}
	enc := fakeEncrypter{versions: []int{1, 2}, calls: &calls}
	got, err := EncryptArtifacts(t.Context(), m, enc, artifacts(3))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"transit bw-artifact", "encrypt", "encrypt", "encrypt", "transit bw-artifact"}) {
		t.Fatalf("requests %q", calls)
	}
	// Each artifact carries the second read's whole classification of its version: the status
	// publication seeds (DM §5.2).
	want := []struct {
		version int64
		created time.Time
	}{{1, v1}, {2, v2}, {2, v2}}
	for i, w := range want {
		e := got[i]
		s := classify.Result{Class: classify.Retained, Reason: classify.None, Created: w.created, Date: depDate.Add(time.Second)}
		if e.Key != "bw-artifact" || e.Version != w.version || e.Status != s || !strings.HasPrefix(string(e.Ciphertext), "vault:v"+strconv.FormatInt(w.version, 10)+":") {
			t.Fatalf("artifact %d: key %s version %d status %+v", i, e.Key, e.Version, e.Status)
		}
	}

	// The key read and recorded is the one the encrypter encrypts under.
	calls = nil
	m = fakeMeta{transit: []classify.Answer{ok(transitBody(1, v1)), ok(transitBody(1, v1))}, calls: &calls}
	got, err = EncryptArtifacts(t.Context(), m, fakeEncrypter{key: "bw-artifact-2", versions: []int{1}, calls: &calls}, artifacts(1))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"transit bw-artifact-2", "encrypt", "transit bw-artifact-2"}) || got[0].Key != "bw-artifact-2" {
		t.Fatalf("requests %q, key %s", calls, got[0].Key)
	}

	// An entry for a version not used does not make the answer unreadable, before or after.
	other := ok([]byte(`{"data":{"keys":{"1":null,"2":` + strconv.FormatInt(v2.Unix(), 10) + `},"latest_version":2,"min_available_version":0,"min_decryption_version":1,"soft_deleted":false}}`))
	calls = nil
	got, err = EncryptArtifacts(t.Context(), fakeMeta{transit: []classify.Answer{other, other}, calls: &calls}, fakeEncrypter{versions: []int{2}, calls: &calls}, artifacts(1))
	if err != nil {
		t.Fatalf("a malformed entry for an unused version: %v", err)
	}
	if got[0].Version != 2 || !got[0].Status.Created.Equal(v2) {
		t.Fatalf("version %d created %v", got[0].Version, got[0].Status.Created)
	}
}

func TestEncryptArtifactsRefuses(t *testing.T) {
	before := depDate.Add(-time.Hour)
	noDate := ok(transitBody(1, before))
	noDate.Date = ""
	badDate := ok(transitBody(1, before))
	badDate.Date = "yesterday"
	fractionalDate := ok(transitBody(1, before))
	fractionalDate.Date = "Sun, 04 Oct 2026 12:00:30.500 GMT"
	nullTime := ok([]byte(`{"data":{"keys":{"1":null},"latest_version":1,"min_available_version":0,"min_decryption_version":1,"soft_deleted":false}}`))
	for _, c := range []struct {
		name          string
		first, second classify.Answer
		versions      []int
		encrypts      int // the encryptions made before the refusal
		class         classify.Class
		reason        string
	}{
		{"first read without a Date", noDate, ok(transitBody(1, before)), []int{1}, 0, "", ReasonNoDate},
		{"first read with an unreadable Date", badDate, ok(transitBody(1, before)), []int{1}, 0, "", ReasonNoDate},
		{"first read with a fractional Date", fractionalDate, ok(transitBody(1, before)), []int{1}, 0, "", ReasonNoDate},
		{"first read with a null creation time for the used version", nullTime, nullTime, []int{1}, 1, "", ReasonNotInFirstRead},
		{"version created in the Date's second", ok(transitBody(1, depDate)), ok(transitBody(1, depDate)), []int{1}, 1, "", ReasonCreatedNotBeforeDate},
		{"version created after the Date", ok(transitBody(1, depDate.Add(time.Second))), ok(transitBody(1, depDate.Add(time.Second))), []int{1}, 1, "", ReasonCreatedNotBeforeDate},
		{"version the first read lacks", ok(transitBody(1, before)), ok(transitBody(1, before, before)), []int{2}, 1, "", ReasonNotInFirstRead},
		{"first read denied", classify.Answer{Status: http.StatusForbidden, Date: dateHeader(depDate)}, ok(transitBody(1, before)), []int{1}, 0, classify.Unknown, string(classify.Denied)},
		{"first read unreachable", classify.Answer{Unreachable: true}, ok(transitBody(1, before)), []int{1}, 0, classify.Unknown, string(classify.Unreachable)},
		{"first read unreadable", ok([]byte(`{"data":[]}`)), ok(transitBody(1, before)), []int{1}, 0, classify.Unknown, string(classify.Unreadable)},
		{"recreated between the reads", ok(transitBody(1, before)), ok(transitBody(1, before.Add(time.Second))), []int{1}, 1, classify.Unknown, string(classify.IdentityMismatch)},
		{"below the decryption floor after", ok(transitBody(1, before, before)), ok(transitBody(2, before, before)), []int{1}, 1, classify.Blocked, string(classify.BelowDecryptionFloor)},
		{"second read unavailable", ok(transitBody(1, before)), classify.Answer{Status: http.StatusServiceUnavailable}, []int{1}, 1, classify.Unknown, string(classify.Unavailable)},
		{"second read unreachable", ok(transitBody(1, before)), classify.Answer{Unreachable: true}, []int{1}, 1, classify.Unknown, string(classify.Unreachable)},
	} {
		t.Run(c.name, func(t *testing.T) {
			var calls []string
			m := fakeMeta{transit: []classify.Answer{c.first, c.second}, calls: &calls}
			_, err := EncryptArtifacts(t.Context(), m, fakeEncrypter{versions: c.versions, calls: &calls}, artifacts(1))
			d := asDependency(t, err)
			if d.Object != "bw-artifact" || d.Reference != "" || d.Class != c.class || d.Reason != c.reason {
				t.Fatalf("got %+v", d)
			}
			if n := strings.Count(strings.Join(calls, ","), "encrypt"); n != c.encrypts {
				t.Fatalf("%d encryptions before the refusal, want %d (%q)", n, c.encrypts, calls)
			}
		})
	}

	var calls []string
	m := fakeMeta{transit: []classify.Answer{ok(transitBody(1, before)), ok(transitBody(1, before))}, calls: &calls}
	if _, err := EncryptArtifacts(t.Context(), m, fakeEncrypter{err: provider.ErrDenied, calls: &calls}, artifacts(1)); !errors.Is(err, provider.ErrDenied) {
		t.Fatalf("a failed encryption: %v", err)
	} else {
		noValue(t, err)
	}
	calls = nil
	bad := fakeEncrypterFunc(func([]byte) (provider.Ciphertext, error) { return "vault:" + depSecret, nil })
	m = fakeMeta{transit: []classify.Answer{ok(transitBody(1, before)), ok(transitBody(1, before))}, calls: &calls}
	if _, err := EncryptArtifacts(t.Context(), m, bad, artifacts(1)); err == nil {
		t.Fatal("a ciphertext with no key version was accepted")
	} else {
		noValue(t, err)
	}
}

type fakeEncrypterFunc func([]byte) (provider.Ciphertext, error)

func (f fakeEncrypterFunc) EncryptArtifact(_ context.Context, p []byte) (provider.Ciphertext, error) {
	return f(p)
}

func (fakeEncrypterFunc) ArtifactKey() string { return "bw-artifact" }

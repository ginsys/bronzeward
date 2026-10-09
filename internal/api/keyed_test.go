package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// fakeIngester's digest is HMAC-SHA-256 keyed by the version, answering latest for version 0. It
// records each version asked for; err fails every digest, errAt the digests under one version.
type fakeIngester struct {
	mu       sync.Mutex
	latest   int
	err      error
	errAt    map[int]error
	versions []int
	onCreate func(ctx context.Context, n int) error
	calls    int
	created  []string
	envelope []byte
	// staged maps each staging ciphertext to its envelope; decryptErr fails every decryption.
	// onDecrypt, when set, runs before each decryption without the lock held; onEncrypt runs
	// before each staging encryption and fails it by returning an error.
	staged     map[provider.Ciphertext][]byte
	decryptErr error
	decrypts   int
	onDecrypt  func(ctx context.Context)
	onEncrypt  func(ctx context.Context) error
	// access is the cluster's Talos access TalosAccess answers, or accessErr; accessReads
	// records the clusters asked for.
	access      provider.TalosAccess
	accessErr   error
	accessReads []string
}

func (f *fakeIngester) Digest(_ context.Context, in []byte, v int) (provider.Digest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versions = append(f.versions, v)
	if f.err != nil {
		return provider.Digest{}, f.err
	}
	if v == 0 {
		v = f.latest
	}
	if err := f.errAt[v]; err != nil {
		return provider.Digest{}, err
	}
	m := hmac.New(sha256.New, []byte(strconv.Itoa(v)))
	m.Write(in)
	return provider.Digest{Key: "bw-digest", Version: v, Sum: [32]byte(m.Sum(nil))}, nil
}

func (f *fakeIngester) asked() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.versions...)
}

// CreateGeneration records each path it creates. onCreate, when set, runs before the n-th create
// (from 1) without the lock held and fails it by returning an error.
func (f *fakeIngester) CreateGeneration(ctx context.Context, p provider.GenerationPath, _ provider.Value) (provider.Generation, error) {
	f.mu.Lock()
	f.calls++
	n, hook := f.calls, f.onCreate
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, n); err != nil {
			return provider.Generation{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, p.String())
	return provider.Generation{Path: p, Version: 1}, nil
}

func (f *fakeIngester) paths() (calls int, created []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]string(nil), f.created...)
}

// fakeCiphertext stands for a transit ciphertext; it is derived from the plaintext and holds none.
func fakeCiphertext(key string, plaintext []byte) provider.Ciphertext {
	sum := sha256.Sum256(plaintext)
	return provider.Ciphertext("vault:v1:" + key + ":" + fmt.Sprintf("%x", sum))
}

func (f *fakeIngester) EncryptBaseline(_ context.Context, plaintext []byte) (provider.Ciphertext, error) {
	return fakeCiphertext("baseline", plaintext), nil
}

// EncryptStaging records the last envelope it encrypted, and keeps each one for DecryptStaging.
func (f *fakeIngester) EncryptStaging(ctx context.Context, envelope []byte) (provider.Ciphertext, error) {
	f.mu.Lock()
	hook := f.onEncrypt
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return "", err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.envelope = append([]byte(nil), envelope...)
	ct := fakeCiphertext("staging", envelope)
	if f.staged == nil {
		f.staged = map[provider.Ciphertext][]byte{}
	}
	f.staged[ct] = f.envelope
	return ct, nil
}

// DecryptStaging answers the envelope EncryptStaging encrypted to ct, or decryptErr, and counts
// the calls.
func (f *fakeIngester) DecryptStaging(ctx context.Context, ct provider.Ciphertext) ([]byte, error) {
	f.mu.Lock()
	hook := f.onDecrypt
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decrypts++
	if f.decryptErr != nil {
		return nil, f.decryptErr
	}
	p, ok := f.staged[ct]
	if !ok {
		return nil, errors.New("fake: not a staging ciphertext of this provider")
	}
	return append([]byte(nil), p...), nil
}

// TalosAccess answers f.access, or f.accessErr, and counts the reads.
func (f *fakeIngester) TalosAccess(_ context.Context, cluster string) (provider.TalosAccess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accessReads = append(f.accessReads, cluster)
	if f.accessErr != nil {
		return provider.TalosAccess{}, f.accessErr
	}
	return f.access, nil
}

// docInput is a body carrying unextracted input in its document member.
type docInput struct {
	Document ingest.Unresolved `json:"document"`
	Note     string            `json:"note"`
}

func (d *docInput) check(*API) error {
	if d.Document.Size() == 0 {
		return errors.New("document is required")
	}
	return nil
}
func (d *docInput) keyedDigest(ctx context.Context, h ingest.HMAC, material []byte, version int) (provider.Digest, error) {
	return ingest.Fingerprint(ctx, h, material, d.Document, version)
}

func keyedRoute() *route {
	return &route{method: http.MethodPost, pattern: "/test-documents", roles: []auth.Role{auth.Author}, keyed: "document",
		action: "test.document", input: func() input { return &docInput{} },
		effect: func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
			return result{status: http.StatusCreated, body: map[string]string{"request": q.id}}, nil
		}}
}

func postDoc(tok, k, body string) call {
	return call{method: "POST", path: prefix + "/test-documents", token: tok, key: k, body: body}
}

// PA §7.1: a route whose body carries unextracted input is fingerprinted with the digest key, the
// record names the key and its version, and a retry is compared under that version, not the
// latest, so a rotation does not turn it into idempotency-key-reused.
func TestKeyedFingerprint(t *testing.T) {
	f := &fakeIngester{latest: 1}
	e := newEnvWith(t, deps{ing: f}, options{extra: []*route{keyedRoute()}})
	tok := e.human("h-author")
	body := `{"document":"machine:\n  token: ` + "keyed-secret-1" + `\n","note":"a"}`
	if rec := e.do(e.api, postDoc(tok, key, body)); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var fkey string
	var fp []byte
	if err := e.db.QueryRow(`SELECT fingerprint_key, fingerprint FROM idempotency_record`).Scan(&fkey, &fp); err != nil {
		t.Fatal(err)
	}
	if fkey != "transit/bw-digest@v1" {
		t.Fatalf("fingerprint_key %q", fkey)
	}
	// The HMAC covers the request without its document, then the document, length-prefixed.
	doc := "machine:\n  token: keyed-secret-1\n"
	in := material(&request{route: keyedRoute()}, []byte(`{"note":"a"}`))
	in = binary.BigEndian.AppendUint64(in, uint64(len(doc)))
	want := hmac.New(sha256.New, []byte("1"))
	want.Write(append(in, doc...))
	if !hmac.Equal(fp, want.Sum(nil)) {
		t.Fatal("the stored fingerprint is not the HMAC of the request without its document, then the document")
	}
	if rec := e.do(e.api, postDoc(tok, key, body)); rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	for _, other := range []string{
		`{"document":"machine:\n  token: keyed-secret-2\n","note":"a"}`, // another document
		`{"document":"machine:\n  token: keyed-secret-1\n","note":"b"}`, // another note
	} {
		wantProblem(t, e.do(e.api, postDoc(tok, key, other)), http.StatusUnprocessableEntity, "idempotency-key-reused")
	}
	// The digest key rotates to v2: the retry is recomputed under v1 and replays.
	f.mu.Lock()
	f.latest, f.versions = 2, nil
	f.mu.Unlock()
	if rec := e.do(e.api, postDoc(tok, key, body)); rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay after rotation: %d %s; digests asked %v", rec.Code, rec.Body, f.asked())
	}
	if got := f.asked(); len(got) < 2 || got[len(got)-1] != 1 {
		t.Fatalf("digests asked %v; want the latest, then v1", got)
	}
	// v1 can no longer be computed: the retry cannot be compared, and is 422 all the same.
	f.mu.Lock()
	f.errAt = map[int]error{1: provider.ErrDenied}
	f.mu.Unlock()
	wantProblem(t, e.do(e.api, postDoc(tok, key, body)), http.StatusUnprocessableEntity, "idempotency-key-reused")
	// ...unless the provider is unavailable, which is no answer: 503.
	f.mu.Lock()
	f.errAt = map[int]error{1: provider.ErrUnavailable}
	f.mu.Unlock()
	wantProblem(t, e.do(e.api, postDoc(tok, key, body)), http.StatusServiceUnavailable, "dependency-unavailable")
	if acts, records := rows(t, e); acts != 1 || records != 1 {
		t.Fatalf("acts %d, records %d; want only the first request's", acts, records)
	}
}

// A fingerprint the provider cannot compute stops the request before anything is written; a
// missing document is the request's fault; without a provider the route cannot run.
func TestKeyedFingerprintRefusals(t *testing.T) {
	f := &fakeIngester{latest: 1, err: provider.ErrUnavailable}
	e := newEnvWith(t, deps{ing: f}, options{extra: []*route{keyedRoute()}})
	tok := e.human("h-author")
	wantProblem(t, e.do(e.api, postDoc(tok, key, `{"document":"a: 1\n","note":"a"}`)), http.StatusServiceUnavailable, "dependency-unavailable")
	wantProblem(t, e.do(e.api, postDoc(tok, key, `{"note":"a"}`)), http.StatusBadRequest, "invalid-request")
	none := newEnv(t, options{extra: []*route{keyedRoute()}})
	wantProblem(t, none.do(none.api, postDoc(none.human("h-author"), key, `{"document":"a: 1\n","note":"a"}`)),
		http.StatusServiceUnavailable, "dependency-unavailable")
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d; want none", acts, records)
	}
}

// result.afterCommit runs once, after this request's own COMMIT: not for an attempt that rolled
// back, not for a commit that failed, not on a replay; and for a lost COMMIT reply that the
// read-back shows committed.
func TestAfterCommit(t *testing.T) {
	hooked := func(ran *int) *route {
		return testRoute(func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
			return result{status: http.StatusCreated, body: map[string]string{"request": q.id}, afterCommit: func() { *ran++ }}, nil
		})
	}
	var ran int
	e := newEnv(t, options{extra: []*route{hooked(&ran)}, beforeCommit: deadlock(1)})
	tok := e.human("h-author")
	if rec := e.do(e.api, post(tok, key, `{}`)); rec.Code != http.StatusCreated || ran != 1 {
		t.Fatalf("%d %s, afterCommit ran %d times; want once", rec.Code, rec.Body, ran)
	}
	if rec := e.do(e.api, post(tok, key, `{}`)); rec.Header().Get("Idempotent-Replayed") != "true" || ran != 1 {
		t.Fatalf("replay ran afterCommit: %d", ran)
	}
	var rejected int
	r := newEnv(t, options{extra: []*route{hooked(&rejected)}, commit: func(tx *sql.Tx) error {
		_ = tx.Rollback()
		return &pgconn.PgError{Code: "23503", Message: "deferred foreign key violated at COMMIT (test)"}
	}})
	r.do(r.api, post(r.human("h-author"), key, `{}`))
	if rejected != 0 {
		t.Fatalf("a rejected COMMIT ran afterCommit %d times", rejected)
	}
	var lost int
	l := newEnv(t, options{extra: []*route{hooked(&lost)}, commit: func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("connection reset after COMMIT (test)")
	}})
	if rec := l.do(l.api, post(l.human("h-author"), key, `{}`)); rec.Code != http.StatusCreated || lost != 1 {
		t.Fatalf("a lost reply read back as committed: %d, afterCommit ran %d times", rec.Code, lost)
	}
}

// The canonical body a keyed route's fingerprint covers leaves the document out: the HMAC covers
// the document once, after the rest (§7.1).
func TestDecodeBodyOmits(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"note":"a","document":"a: 1\n"}`))
	r.Header.Set("Content-Type", "application/json")
	var in docInput
	canon, err := decodeBody(r, &in, "document")
	if err != nil || string(canon) != `{"note":"a"}` || in.Document.Size() == 0 {
		t.Fatalf("canon %s, err %v, document %d bytes", canon, err, in.Document.Size())
	}
	// The document is left to its own type (compilation §2.1); the other members are still
	// checked for U+0000 here.
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"note":"a\u0000","document":"a: 1\n"}`))
	r.Header.Set("Content-Type", "application/json")
	if _, err := decodeBody(r, &docInput{}, "document"); err == nil || !strings.Contains(err.Error(), "U+0000") {
		t.Fatalf("U+0000 in another member: %v", err)
	}
}

// A bodyless route accepts only an empty body: a read that fails before its first byte (a
// truncated request, say) is refused, not taken for no body, so it cannot commit a removal.
func TestDecodeBodyNoBodyReadFailure(t *testing.T) {
	r := httptest.NewRequest("DELETE", "/", strings.NewReader(""))
	if canon, err := decodeBody(r, &noBody{}, ""); err != nil || len(canon) != 0 {
		t.Fatalf("empty body: canon %q, err %v", canon, err)
	}
	r = httptest.NewRequest("DELETE", "/", iotest.ErrReader(errors.New("connection reset")))
	if _, err := decodeBody(r, &noBody{}, ""); err == nil {
		t.Fatal("a failed read was taken for an empty body")
	}
}

// An effect whose claim creation found a later epoch than the process's own answers 503.
func TestEpochSuperseded(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(func(context.Context, *API, *sql.Tx, *request) (result, error) {
		return result{}, fmt.Errorf("claim: %w", staging.ErrEpochSuperseded)
	})}})
	wantProblem(t, e.do(e.api, post(e.human("h-author"), key, `{}`)), http.StatusServiceUnavailable, "epoch-superseded")
}

func TestRecordedVersion(t *testing.T) {
	for _, c := range []struct {
		recorded, current string
		want              int
	}{
		{"transit/bw-digest@v1", "transit/bw-digest@v2", 1},
		{"transit/bw-digest@v12", "transit/bw-digest@v2", 12},
		{"transit/other@v1", "transit/bw-digest@v2", 0}, // another key
		{"", "transit/bw-digest@v2", 0},                 // a SHA-256 record
		{"transit/bw-digest@v0", "transit/bw-digest@v2", 0},
		{"transit/bw-digest@v01", "transit/bw-digest@v2", 0},
		{"transit/bw-digest@v-1", "transit/bw-digest@v2", 0},
		{"kv/bw-digest@v1", "kv/bw-digest@v2", 0},
	} {
		if v, ok := recordedVersion(c.recorded, c.current); v != c.want || ok != (c.want != 0) {
			t.Errorf("recordedVersion(%q, %q) = %d %t; want %d", c.recorded, c.current, v, ok, c.want)
		}
	}
}

// PA §9.4: the codes the ingestion routes answer have titles.
func TestIngestionProblemTitles(t *testing.T) {
	for _, c := range []string{"epoch-superseded", "precondition-failed", "validation-failed"} {
		if titles[c] == "" {
			t.Errorf("%s has no title", c)
		}
	}
}

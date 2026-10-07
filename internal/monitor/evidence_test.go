package monitor

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/provider"
)

// request is one request a provider stand-in received.
type request struct{ method, path, token string }

// standIn answers each monitored object's metadata path with its answer and records every request.
type standIn struct {
	mu       sync.Mutex
	answers  map[string]classify.Answer
	requests []request
}

func (s *standIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, request{r.Method, r.URL.Path, r.Header.Get("X-Vault-Token")})
	a, ok := s.answers[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(a.Status)
	w.Write(a.Body.Bytes())
}

// tokenFile writes a synthetic token to a 0600 file and reads it as the provider package does.
func tokenFile(t *testing.T, value string) provider.Token {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := provider.ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// Dependency monitor §10.1 item 5: a pass through the real metadata client asks the provider for
// each monitored dependency by name, with GET only and the metadata identity's token on every
// request, and classifies both retained from those answers. Control: the stand-in answers 404 to
// any other path, which would classify unknown.
func TestPassMetadataIdentity(t *testing.T) {
	f := seed(t)
	kvPath, keyPath := "/v1/secret/metadata/"+f.kv, "/v1/transit/keys/"+f.key
	s := &standIn{answers: map[string]classify.Answer{kvPath: kvAnswer(t, f, nil), keyPath: transitAnswer(t, f)}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	meta, err := provider.NewMetadata(srv.URL, tokenFile(t, "synthetic-metadata-token"))
	if err != nil {
		t.Fatal(err)
	}
	l := &logs{}
	pass(t, New(f.db, meta, Defaults(), l.logf, io.Discard))
	for _, dep := range []string{f.depKV, f.depKey} {
		if st := statusOf(t, f.db, dep); st.class != "retained" {
			t.Fatalf("%s classified %s/%s, want retained", dep, st.class, st.reason.String)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var paths []string
	for _, q := range s.requests {
		if q.method != http.MethodGet || q.token != "synthetic-metadata-token" {
			t.Errorf("sent %s %s with token %q, want GET with the metadata identity's", q.method, q.path, q.token)
		}
		paths = append(paths, q.path)
	}
	slices.Sort(paths)
	if want := []string{kvPath, keyPath}; !slices.Equal(paths, want) {
		t.Fatalf("asked %q, want %q", paths, want)
	}
}

// waitForLockWaits polls until n sessions of this database wait on a lock.
func waitForLockWaits(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d sessions waited on a lock within 10s", n)
}

// lostResult is the classification of the fixture's KV version destroyed.
func lostResult(t *testing.T, f *fixture) classify.Result {
	t.Helper()
	created, err := time.Parse(time.RFC3339Nano, f.kvCreated)
	if err != nil {
		t.Fatal(err)
	}
	r := classify.Classify(classify.Dependency{Provider: classify.KV, Object: f.kv, Version: 1, Created: created},
		kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }))
	if r.Class != classify.Lost {
		t.Fatalf("observed %s/%s, want lost", r.Class, r.Reason)
	}
	return r
}

// Dependency monitor §10.1 item 6: two instances that both observed one transition and both reach
// step 5 raise one alert between them. The advisory lock normally keeps the second from
// classifying at all, so both recordings are driven past it here: each instance's step 5 starts
// while a third session holds the status row, and both are waiting before it lets go. The one
// granted the row lock first records the change and its alert; the other then reads the class
// already recorded and raises nothing. Control: reading the status without the row lock, both read
// retained before either records, and two lost alerts are raised.
func TestRecordOneAlertAcrossInstances(t *testing.T) {
	f := seed(t)
	p := &fake{}
	m1, _ := monitorFor(f, p, Defaults())
	m2, _ := monitorFor(f, p, Defaults())
	d := dependency{id: f.depKV, provider: "kv", object: f.kv, version: 1, created: f.kvCreated}
	r := lostResult(t, f)
	holder, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM dependency_status WHERE id = $1 FOR UPDATE`, d.id); err != nil {
		t.Fatal(err)
	}
	from := time.Now()
	done := make(chan error, 2)
	for _, m := range []*Monitor{m1, m2} {
		go func() {
			conn, err := f.db.Conn(context.Background())
			if err != nil {
				done <- err
				return
			}
			defer conn.Close()
			_, err = m.record(context.Background(), conn, d, from, r)
			done <- err
		}()
	}
	waitForLockWaits(t, f.db, 2)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := kinds(alerts(t, f.db, d.id)); !slices.Equal(got, []string{"lost"}) {
		t.Fatalf("alerts %v, want one lost", got)
	}
	if s := statusOf(t, f.db, d.id); s.class != "lost" {
		t.Fatalf("status %+v", s)
	}
}

// pausing is one instance's view of a shared provider: its first KV request takes the provider's
// answer at that moment, signals paused, and returns it only once gate is closed, as an instance
// that asked before a loss and stalled before recording.
type pausing struct {
	*fake
	once   sync.Once
	paused chan struct{}
	gate   chan struct{}
}

func newPausing(p *fake) *pausing {
	return &pausing{fake: p, paused: make(chan struct{}), gate: make(chan struct{})}
}

func (p *pausing) KV(ctx context.Context, path provider.GenerationPath) (classify.Answer, error) {
	a, err := p.fake.KV(ctx, path)
	first := false
	p.once.Do(func() { first = true })
	if first {
		close(p.paused)
		select {
		case <-p.gate:
		case <-time.After(10 * time.Second):
		}
	}
	return a, err
}

// Dependency monitor §10.1 item 14: two instances classify one dependency while the provider
// loses it. The first asks before the loss, holds retained and pauses before recording; the
// second, asking after the loss, finds the dependency locked and skips it. The first then records
// retained, and the next pass records lost: the status ends lost with one lost alert. Control:
// without the advisory lock, the second records lost, the first's older retained overwrites it,
// and the next pass raises a second lost alert.
func TestOverlappingMonitors(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), transitAnswer(t, f))
	first := newPausing(p)
	m1, _ := monitorFor(f, first.fake, Defaults())
	m1.meta = first
	m2, _ := monitorFor(f, p, Defaults())
	done := make(chan error, 1)
	go func() { done <- m1.Pass(context.Background()) }()
	select {
	case <-first.paused:
	case <-time.After(10 * time.Second):
		t.Fatal("the first instance never asked")
	}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	pass(t, m2)
	close(first.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	pass(t, m2)
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" {
		t.Fatalf("status %+v, want lost", s)
	}
	if got := kinds(alerts(t, f.db, f.depKV)); !slices.Equal(got, []string{"lost"}) {
		t.Fatalf("alerts %v, want one lost", got)
	}
}

// Dependency monitor §10.1 item 14: the first instance, paused past the idle-session timeout while
// it holds retained from before the loss, has its session ended with its lock; the second then
// classifies and records lost, and the first's record is refused, so the status stays lost with
// one lost alert after the next pass. Control: recording on a new session once the first ended,
// the older retained overwrites lost and the next pass raises a second lost alert.
func TestOverlappingMonitorIdleSession(t *testing.T) {
	f := seed(t)
	p := &fake{}
	p.set(kvAnswer(t, f, nil), transitAnswer(t, f))
	first := newPausing(p)
	tm := Defaults()
	tm.IdleSession = 300 * time.Millisecond
	m1, _ := monitorFor(f, first.fake, tm)
	m1.meta = first
	m2, _ := monitorFor(f, p, Defaults())
	done := make(chan error, 1)
	go func() { done <- m1.Pass(context.Background()) }()
	select {
	case <-first.paused:
	case <-time.After(10 * time.Second):
		t.Fatal("the first instance never asked")
	}
	p.set(kvAnswer(t, f, func(v map[string]any) { v["destroyed"] = true }), transitAnswer(t, f))
	deadline := time.Now().Add(10 * time.Second)
	for advisoryLocks(t, f.db) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the paused session kept its lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	pass(t, m2)
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" {
		t.Fatalf("the second instance recorded %+v, want lost", s)
	}
	close(first.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	pass(t, m2)
	if s := statusOf(t, f.db, f.depKV); s.class != "lost" {
		t.Fatalf("status %+v, want lost", s)
	}
	if got := kinds(alerts(t, f.db, f.depKV)); !slices.Equal(got, []string{"lost"}) {
		t.Fatalf("alerts %v, want one lost", got)
	}
}

// Dependency monitor §10.1 item 14: a provider request that times out, through the real metadata
// client, is recorded unknown by step 5 and raises regression after retained. Control: treating
// the timeout as an exit, the status stays retained and nothing is raised.
func TestPassRequestTimeout(t *testing.T) {
	f := seed(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	meta, err := provider.NewMetadata(srv.URL, tokenFile(t, "synthetic-metadata-token"))
	if err != nil {
		t.Fatal(err)
	}
	tm := Defaults()
	tm.Request = 200 * time.Millisecond
	l := &logs{}
	pass(t, New(f.db, meta, tm, l.logf, io.Discard))
	for _, dep := range []string{f.depKV, f.depKey} {
		if s := statusOf(t, f.db, dep); s.class != "unknown" || s.reason.String != "unreachable" {
			t.Fatalf("%s %+v, want unknown/unreachable", dep, s)
		}
		if got := kinds(alerts(t, f.db, dep)); !slices.Equal(got, []string{"regression"}) {
			t.Fatalf("%s alerts %v, want regression", dep, got)
		}
	}
}

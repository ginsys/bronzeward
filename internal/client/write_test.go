package client

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fake is a server for one write: GET /server, /identity and /recovery answer as a revision 2
// server in epoch, and post answers the nth POST, from 1. seen lists every request.
type fake struct {
	mu       sync.Mutex
	epoch    string
	recovery func(n int) string // the epoch the nth GET /recovery shows; nil: epoch
	post     func(n int, w http.ResponseWriter, r *http.Request)
	server   string // GET /server's answer; "" for the default
	seen     []*http.Request
	posts    int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	r.Body = io.NopCloser(bytes.NewReader(must(io.ReadAll(r.Body))))
	f.seen = append(f.seen, r)
	epoch, n := f.epoch, 0
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/recovery" {
		n = len(slices.DeleteFunc(slices.Clone(f.seen), func(q *http.Request) bool { return q.URL.Path != "/api/v1/recovery" }))
		if f.recovery != nil {
			epoch = f.recovery(n)
		}
	}
	if r.Method == http.MethodPost {
		f.posts++
		n = f.posts
	}
	f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", epoch)
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/server":
		fmt.Fprint(w, cmp.Or(f.server, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal","maxTokenLifetimeSeconds":300}`))
	case "GET /api/v1/identity":
		fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH","service":"ci","roles":["author"],"epoch":"`+epoch+`"}`)
	case "GET /api/v1/recovery":
		fmt.Fprint(w, `{"recoveryMode":false,"epoch":"`+epoch+`","scopes":[]}`)
	case "GET /api/v1/clusters":
		fmt.Fprint(w, `{"items":[]}`)
	default:
		f.post(n, w, r)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// requests lists method and path of the requests seen, after the first skip.
func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s []string
	for _, r := range f.seen {
		s = append(s, r.Method+" "+r.URL.Path)
	}
	return s
}

func (f *fake) postsSeen() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(f.seen), func(r *http.Request) bool { return r.Method != http.MethodPost })
}

func problemAnswer(w http.ResponseWriter, status int, code, extra string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"urn:bronzeward:problem:%s","title":"BWSYNTH","status":%d,"instance":"req_BWSYNTH"%s}`, code, status, extra)
}

func created(w http.ResponseWriter) {
	w.WriteHeader(http.StatusCreated)
	fmt.Fprint(w, `{"id":"cl_BWSYNTH"}`)
}

// drop closes the connection without an answer, after the request arrived.
func drop(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

// writeClient is an automation client of f in a context that has seen epoch, with the waits
// before repeats recorded instead of slept.
func writeClient(t *testing.T, f *fake, epoch string) (*Client, *bytes.Buffer, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("bwt_BWSYNTH.BWSYNTH-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var notices bytes.Buffer
	c, err := Open(&Context{Name: "lab", Server: srv.URL, Credential: "automation", LastEpoch: epoch}, TokenInput{File: tok}, &notices, nil)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	sleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { sleep = time.Sleep })
	return c, &notices, &waits
}

func clusterWrite() *Write {
	return &Write{Method: http.MethodPost, Path: "/clusters", Body: []byte(`{"name":"lab-a"}`), HumanOnly: true,
		Check: "cluster list", Read: func(*Client) (string, error) { return "BWSYNTH shows", nil }}
}

// TestPrincipalAfterRenewal checks that a renewal during the context-use read GET /server, which
// adopts a sign-in another process stored for another subject, refuses an operator's repeat before
// it is sent, and that a first write's block names the principal it was sent as (T10, §8.4 item 3).
func TestPrincipalAfterRenewal(t *testing.T) {
	jwt := func(sub string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.example.invalid","sub":"`+sub+`"}`)) + ".BWSYNTH-sig"
	}
	first, other := jwt("h-author"), jwt("h-other")
	for _, repeat := range []bool{true, false} {
		f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { drop(w) }}
		c, notices, _ := writeClient(t, f, "ep_BWSYNTH-1")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/server" && r.Header.Get("Authorization") == "Bearer "+first {
				problemAnswer(w, http.StatusUnauthorized, "unauthenticated", "")
				return
			}
			f.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		t.Setenv("BRONZEWARD_TOKEN_FILE", "")
		tokens := filepath.Join(t.TempDir(), "tokens")
		// Another process signed in as h-other meanwhile: the renewal adopts its stored pair.
		if err := WriteTokens(tokens, &Tokens{AccessToken: other, RefreshToken: "BWSYNTH-rt2"}); err != nil {
			t.Fatal(err)
		}
		c.Context.Server, c.Context.Credential = srv.URL, "oidc"
		c.Context.OIDC = &OIDC{Issuer: "https://idp.example.invalid", TokenFile: tokens}
		c.signIn = &signIn{store: openStore(c.Context, true), tokens: &Tokens{AccessToken: first, RefreshToken: "BWSYNTH-rt"}}
		c.token, c.service = first, ""
		w := clusterWrite()
		if repeat {
			w.Repeat = &Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: "ep_BWSYNTH-1", Principal: "https://idp.example.invalid|h-author"}
		}
		_, err := c.Send(w)
		var u *Unknown
		switch {
		case !strings.Contains(notices.String(), "a new sign-in"):
			t.Errorf("repeat %v: notices %q; want the renewal to adopt the other sign-in", repeat, notices)
		case repeat && (!errors.As(err, new(RefusedRepeat)) || len(f.postsSeen()) != 0):
			t.Errorf("repeat: %v after %d posts; want the repeat refused before sending, as h-other is not the given principal", err, len(f.postsSeen()))
		case !repeat && (!errors.As(err, &u) || u.Principal != "https://idp.example.invalid|h-other"):
			t.Errorf("first write: %v; want an outcome-unknown block naming h-other, the principal it was sent as", err)
		}
	}
}

func TestKey(t *testing.T) {
	a, b := newKey(), newKey()
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if len(a) != 22 || err != nil || len(raw) != 16 || !KeyShape.MatchString(a) || a == b {
		t.Errorf("keys %q and %q: want two different 22-character base64url keys of 128 bits (%v)", a, b, err)
	}
}

// TestWriteHeaders checks the header set of a write (§8.1, §8.4 item 3) and the warning an
// automation token gets before a human-only route.
func TestWriteHeaders(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
	c, notices, _ := writeClient(t, f, "ep_BWSYNTH-1")
	w := clusterWrite()
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	w.IfMatch = `"BWSYNTH-etag"`
	w.HumanOnly = false
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	posts := f.postsSeen()
	if len(posts) != 2 {
		t.Fatalf("%d posts", len(posts))
	}
	for i, r := range posts {
		want := map[string]string{"Authorization": "Bearer bwt_BWSYNTH.BWSYNTH-secret", "Accept": "application/json",
			"Content-Type": "application/json", "Bronzeward-Expected-Epoch": "ep_BWSYNTH-1"}
		if i == 1 {
			want["If-Match"] = `"BWSYNTH-etag"`
		}
		for k, v := range want {
			if got := r.Header.Values(k); len(got) != 1 || got[0] != v {
				t.Errorf("post %d: %s %q, want exactly %q", i, k, got, v)
			}
		}
		if key := r.Header.Values("Idempotency-Key"); len(key) != 1 || len(key[0]) != 22 {
			t.Errorf("post %d: Idempotency-Key %q", i, key)
		}
		if i == 0 && r.Header.Get("If-Match") != "" {
			t.Error("If-Match sent without one given")
		}
		if b := must(io.ReadAll(r.Body)); string(b) != `{"name":"lab-a"}` {
			t.Errorf("post %d: body %s", i, b)
		}
	}
	if posts[0].Header.Get("Idempotency-Key") == posts[1].Header.Get("Idempotency-Key") {
		t.Error("two writes shared a key")
	}
	if n := strings.Count(notices.String(), "is for a person signed in, not an automation token"); n != 1 {
		t.Errorf("the human-only warning was shown %d times, want once:\n%s", n, notices)
	}
}

// TestRepeatBounds: an ambiguous write is repeated three times with the same key and epoch, after
// 1, 2 and 4 s, each repeat after a confirming read; then its outcome is unknown.
func TestRepeatBounds(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) {
		problemAnswer(w, http.StatusInternalServerError, "internal-error", "")
	}}
	c, _, waits := writeClient(t, f, "ep_BWSYNTH-1")
	_, err := c.Send(clusterWrite())
	var u *Unknown
	if !errors.As(err, &u) || u.SetAside || u.Principal != "svc_BWSYNTH" || u.Epoch != "ep_BWSYNTH-1" || u.Shows != "BWSYNTH shows" {
		t.Fatalf("err %v, want an unknown outcome for svc_BWSYNTH in ep_BWSYNTH-1", err)
	}
	if !slices.Equal(*waits, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}) {
		t.Errorf("waits %v", *waits)
	}
	posts := f.postsSeen()
	for _, r := range posts {
		if r.Header.Get("Idempotency-Key") != u.Key || r.Header.Get("Bronzeward-Expected-Epoch") != "ep_BWSYNTH-1" {
			t.Errorf("a repeat changed its key or epoch: %v", r.Header)
		}
	}
	want := []string{"GET /api/v1/identity", "GET /api/v1/server", "POST /api/v1/clusters"}
	for range 3 {
		want = append(want, "GET /api/v1/recovery", "POST /api/v1/clusters")
	}
	if got := f.requests(); len(posts) != 4 || !slices.Equal(got, want) {
		t.Errorf("requests %v, want %v", got, want)
	}
}

// TestOutcomes covers how each answer ends a write.
func TestOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		post     func(n int, w http.ResponseWriter, r *http.Request)
		recovery func(n int) string
		posts    int
		unknown  bool // the outcome is unknown or set aside, exit 3
		setAside bool
		says     string
		repeat   bool // an operator's T10 repeat of an earlier process's ambiguous attempt
	}{
		{name: "a repeat answered 503 every time is unknown", repeat: true, posts: 4, unknown: true, says: "the one this command repeats",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
			}},
		{name: "a confirming read without an epoch after a 503 sets the write aside, nothing committed", posts: 1, unknown: true, setAside: true,
			says:     "No attempt of it can have committed anything",
			recovery: func(int) string { return "" },
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
			}},
		{name: "a confirming read without an epoch after a dropped attempt sets it aside, maybe committed", posts: 1, unknown: true, setAside: true,
			says:     "it may already have been committed",
			recovery: func(int) string { return "" },
			post:     func(_ int, w http.ResponseWriter, _ *http.Request) { drop(w) }},
		{name: "a repeat refused for its input says nothing of the attempt it repeats", repeat: true, posts: 1, unknown: true,
			says: "answered 422 BWSYNTH (validation-failed) (instance req_BWSYNTH)) and did nothing; the original outcome is still unknown",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusUnprocessableEntity, "validation-failed", "")
			}},
		{name: "a repeat answered with the key's replayed refusal shows that refusal (§8.4 item 2)", repeat: true, posts: 1,
			says: "This is the stored answer to an earlier attempt with this key: that attempt was refused.",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Idempotent-Replayed", "true")
				problemAnswer(w, http.StatusUnprocessableEntity, "validation-failed", "")
			}},
		{name: "a 403 to a repeat says nothing of the attempt it repeats", repeat: true, posts: 1, unknown: true, says: "403 BWSYNTH (forbidden)",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusForbidden, "forbidden", `,"roles":["author"]`)
			}},
		{name: "every attempt 503 is not done", posts: 4, says: "Not done: none of the 4 attempts",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
			}},
		{name: "a 503 after a dropped attempt is unknown (AMB-09)", posts: 4, unknown: true, says: "an attempt may have committed",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
			}},
		{name: "a 5xx that is not a problem is repeated (AMB-03's replay)", posts: 2, says: "already done earlier; showing that result",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.Header().Set("Idempotent-Replayed", "true")
				created(w)
			}},
		{name: "a new epoch at the confirming read sets the write aside (AMB-05)", posts: 1, unknown: true, setAside: true,
			says:     "the epoch is now ep_BWSYNTH-2, not the reference epoch",
			recovery: func(int) string { return "ep_BWSYNTH-2" },
			post:     func(_ int, w http.ResponseWriter, _ *http.Request) { drop(w) }},
		{name: "an earlier-epoch record sets the write aside (AMB-07)", posts: 2, unknown: true, setAside: true,
			says: "the key's record is from an earlier epoch (ep_BWSYNTH-1)",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusConflict, "conflict", `,"request":"req_BWSYNTH","epoch":"ep_BWSYNTH-1"`)
			}},
		{name: "epoch-mismatch sets the write aside (AMB-12)", posts: 2, unknown: true, setAside: true, says: "the epoch is now ep_BWSYNTH-2",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusPreconditionFailed, "epoch-mismatch", `,"epoch":"ep_BWSYNTH-2"`)
			}},
		{name: "a 3xx is neither followed nor repeated", posts: 1, unknown: true, says: `redirecting to "https://BWSYNTH.example.invalid/x"`,
			post: func(_ int, w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://BWSYNTH.example.invalid/x", http.StatusTemporaryRedirect)
			}},
		{name: "a refusal is this send's own", posts: 1, says: "409 BWSYNTH (conflict)",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusConflict, "conflict", `,"cluster":"cl_BWSYNTH"`)
			}},
		{name: "a refusal after a dropped attempt is the repeat's own answer", posts: 2, says: "422 BWSYNTH (validation-failed)",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusUnprocessableEntity, "validation-failed", "")
			}},
		{name: "a 403 after a dropped attempt says nothing of it", posts: 2, unknown: true, says: "403 BWSYNTH (forbidden)",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusForbidden, "forbidden", `,"roles":["author"]`)
			}},
		{name: "identity-revoked after a dropped attempt says nothing of it", posts: 2, unknown: true, says: "403 BWSYNTH (identity-revoked)",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusForbidden, "identity-revoked", "")
			}},
		{name: "a 4xx that is not a problem after a dropped attempt says nothing of it", posts: 2, unknown: true, says: "429 Too Many Requests, not a Bronzeward problem",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					drop(w)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusTooManyRequests)
			}},
		{name: "a key reused on a repeat is unknown", posts: 2, unknown: true, says: "this send did nothing",
			post: func(n int, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					problemAnswer(w, http.StatusInternalServerError, "internal-error", "")
					return
				}
				problemAnswer(w, http.StatusUnprocessableEntity, "idempotency-key-reused", "")
			}},
		{name: "epoch-superseded is not repeated", posts: 1, says: "(epoch-superseded)",
			post: func(_ int, w http.ResponseWriter, _ *http.Request) {
				problemAnswer(w, http.StatusServiceUnavailable, "epoch-superseded", "")
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{epoch: "ep_BWSYNTH-1", post: tc.post, recovery: tc.recovery}
			c, notices, _ := writeClient(t, f, "ep_BWSYNTH-1")
			w := clusterWrite()
			if tc.repeat {
				w.Repeat = &Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: "ep_BWSYNTH-1", Principal: "svc_BWSYNTH"}
			}
			_, err := c.Send(w)
			var u *Unknown
			got := notices.String()
			if err != nil {
				got += err.Error()
			}
			if errors.As(err, &u) != tc.unknown || tc.unknown && u.SetAside != tc.setAside || !strings.Contains(got, tc.says) {
				t.Errorf("err %v, notices %s; want unknown %t, set aside %t, saying %q", err, notices, tc.unknown, tc.setAside, tc.says)
			}
			if n := len(f.postsSeen()); n != tc.posts {
				t.Errorf("%d posts, want %d", n, tc.posts)
			}
		})
	}
}

// postsOnly sends POSTs through post and every other request through rest.
type postsOnly struct{ post, rest http.RoundTripper }

func (p postsOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodPost {
		return p.post.RoundTrip(r)
	}
	return p.rest.RoundTrip(r)
}

// TestNotSent (AMB-01): a write whose every connection is refused before any byte goes out is
// repeated with the same key and then reported not done, never of unknown outcome. The refusal
// comes from a real transport's dial, so it checks the "sent" marking (httptrace GotConn).
func TestNotSent(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
	c, _, waits := writeClient(t, f, "ep_BWSYNTH-1")
	refused := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("BWSYNTH connection refused")
	}}
	c.hc.Transport = postsOnly{post: refused, rest: c.hc.Transport}
	_, err := c.Send(clusterWrite())
	var u *Unknown
	if err == nil || errors.As(err, &u) || !strings.Contains(err.Error(), "Not done: none of the 4 attempts") {
		t.Fatalf("err %v; want not done after 4 attempts", err)
	}
	if len(*waits) != 3 || len(f.postsSeen()) != 0 {
		t.Errorf("waits %v, posts seen %d; want 3 waits and none received", *waits, len(f.postsSeen()))
	}
}

// TestTrustAfterDrop: a certificate refusal on a repeat says nothing of the dropped attempt
// before it, so the outcome stays unknown.
func TestTrustAfterDrop(t *testing.T) {
	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(untrusted.Close)
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { drop(w) }}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	plain, n := c.hc.Transport, 0
	c.hc.Transport = postsOnly{rest: plain, post: roundTrip(func(r *http.Request) (*http.Response, error) {
		if n++; n == 1 {
			return plain.RoundTrip(r)
		}
		r.URL.Scheme, r.URL.Host = "https", strings.TrimPrefix(untrusted.URL, "https://")
		return (&http.Transport{}).RoundTrip(r)
	})}
	_, err := c.Send(clusterWrite())
	var u *Unknown
	var tr *trust
	if !errors.As(err, &u) || u.SetAside || !strings.Contains(err.Error(), "an attempt may have committed") || n != 2 {
		t.Errorf("err %v after %d posts; want an unknown outcome after the refused repeat", err, n)
	}
	if errors.As(err, &tr) {
		t.Errorf("the trust error was returned as the outcome: %v", err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestFirstSendEpoch: a context that kept an earlier epoch sends its first write under the epoch
// the context-use read showed, not the kept one (§8.4 item 3), though its principal needs no read.
func TestFirstSendEpoch(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-2", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	c.service = "svc_BWSYNTH"
	if _, err := c.Send(clusterWrite()); err != nil {
		t.Fatal(err)
	}
	if p := f.postsSeen(); len(p) != 1 || p[0].Header.Get("Bronzeward-Expected-Epoch") != "ep_BWSYNTH-2" {
		t.Errorf("sent %v; want one POST expecting ep_BWSYNTH-2", f.requests())
	}
	if got := f.requests(); !slices.Equal(got, []string{"GET /api/v1/server", "POST /api/v1/clusters"}) {
		t.Errorf("requests %v", got)
	}
}

// TestRepeatRefusals: a T10 repeat under another principal sends no write and nothing after the
// identity read; one in another epoch sends only item 3's reads.
func TestRepeatRefusals(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	w := clusterWrite()
	w.Repeat = &Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: "ep_BWSYNTH-1", Principal: "svc_other"}
	var refused RefusedRepeat
	if _, err := c.Send(w); !errors.As(err, &refused) || !strings.Contains(err.Error(), "not the given --expect-principal svc_other") {
		t.Errorf("another principal: %v", err)
	}
	if got := f.requests(); !slices.Equal(got, []string{"GET /api/v1/identity"}) {
		t.Errorf("another principal sent %v", got)
	}
	f.mu.Lock()
	f.seen, f.epoch = nil, "ep_BWSYNTH-2"
	f.mu.Unlock()
	w.Repeat = &Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: "ep_BWSYNTH-1", Principal: "svc_BWSYNTH"}
	if _, err := c.Send(w); !errors.As(err, &refused) || !strings.Contains(err.Error(), "the epoch is now ep_BWSYNTH-2, not the given --expect-epoch ep_BWSYNTH-1") {
		t.Errorf("another epoch: %v", err)
	}
	if got := f.requests(); !slices.Equal(got, []string{"GET /api/v1/recovery"}) {
		t.Errorf("another epoch sent %v", got)
	}
	// The confirming read shows the repeat's epoch, but the context-use read after it shows another.
	f.mu.Lock()
	f.seen, f.recovery = nil, func(int) string { return "ep_BWSYNTH-1" }
	f.mu.Unlock()
	if _, err := c.Send(w); !errors.As(err, &refused) || !strings.Contains(err.Error(), "the epoch is now ep_BWSYNTH-2, not the given --expect-epoch ep_BWSYNTH-1") {
		t.Errorf("an epoch the context-use read shows: %v", err)
	}
	if got := f.requests(); !slices.Equal(got, []string{"GET /api/v1/recovery", "GET /api/v1/server"}) {
		t.Errorf("an epoch the context-use read shows sent %v", got)
	}
	f.mu.Lock()
	f.seen, f.recovery = nil, nil
	f.mu.Unlock()
	w.Repeat.Epoch = "ep_BWSYNTH-2"
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	if p := f.postsSeen(); len(p) != 1 || p[0].Header.Get("Idempotency-Key") != "BWSYNTH-aaaaaaaaaaaaaaa" ||
		p[0].Header.Get("Bronzeward-Expected-Epoch") != "ep_BWSYNTH-2" {
		t.Errorf("the repeat sent %v", f.requests())
	}
}

// TestBlock checks T10's block: every value, the read, and the flags as one shell word each.
func TestBlock(t *testing.T) {
	u := &Unknown{Why: "BWSYNTH why", Key: "BWSYNTH-aaaaaaaaaaaaaaa", Route: "POST /api/v1/drafts/drf_x/fragments/a", Where: "lab (https://bw.example.test)",
		Principal: "https://idp.example.test|h-o'brien", Epoch: "ep_BWSYNTH-1", IfMatch: `"etag-1"`, Shows: "BWSYNTH shows", Check: "bronzewardctl draft show drf_x"}
	want := `lab (https://bw.example.test): POST /api/v1/drafts/drf_x/fragments/a: outcome unknown: BWSYNTH why.
  key:       BWSYNTH-aaaaaaaaaaaaaaa
  request:   POST /api/v1/drafts/drf_x/fragments/a
  context:   lab (https://bw.example.test)
  principal: https://idp.example.test|h-o'brien
  epoch:     ep_BWSYNTH-1
  if-match:  "etag-1"
  read:      BWSYNTH shows; read it again with: bronzewardctl draft show drf_x
To repeat exactly this request, give the same input and: --idempotency-key BWSYNTH-aaaaaaaaaaaaaaa --expect-epoch 'ep_BWSYNTH-1' --expect-principal 'https://idp.example.test|h-o'\''brien' --if-match '"etag-1"'`
	if got := u.Error(); got != want {
		t.Errorf("block:\n%s\nwant:\n%s", got, want)
	}
	u.SetAside, u.Ambiguous, u.IfMatch = true, true, ""
	if got := u.Error(); !strings.Contains(got, "set aside, not repeated: BWSYNTH why. Its outcome is unknown: it may already have been committed.") ||
		strings.Contains(got, "if-match") {
		t.Errorf("set-aside block:\n%s", got)
	}
	u.Ambiguous = false
	if got := u.Error(); !strings.Contains(got, "set aside, not repeated: BWSYNTH why. No attempt of it can have committed anything.") ||
		strings.Contains(got, "may already have been committed") {
		t.Errorf("set-aside block without an ambiguous attempt:\n%s", got)
	}
}

// TestETagState checks T11's store: a write's answer keeps its draft's ETag, which another server,
// another epoch or a dropped context does not see.
func TestETagState(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"BWSYNTH-etag"`)
		created(w)
	}}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	w := clusterWrite()
	w.Draft, w.IfMatch = "drf_BWSYNTH", `"BWSYNTH-etag-0"`
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	x := *c.Context
	if et, err := ETag(&x, "drf_BWSYNTH"); et != `"BWSYNTH-etag"` || err != nil {
		t.Errorf("kept %q, %v", et, err)
	}
	for _, other := range []Context{{Name: x.Name, Server: "https://other.example.test", LastEpoch: x.LastEpoch},
		{Name: x.Name, Server: x.Server, LastEpoch: "ep_BWSYNTH-2"}} {
		if et, _ := ETag(&other, "drf_BWSYNTH"); et != "" {
			t.Errorf("%s in %s sees %q", other.Server, other.LastEpoch, et)
		}
	}
	// The same name added again for another server: removing the old context, or a command of the
	// old one crossing an epoch, leaves the new server's ETags alone (client-contracts.md §8.3).
	again := Context{Name: x.Name, Server: "https://other.example.test", LastEpoch: "ep_BWSYNTH-9"}
	if err := KeepETag(&again, "drf_BWSYNTH-other", `"BWSYNTH-etag-other"`); err != nil {
		t.Fatal(err)
	}
	if et, _ := ETag(&x, "drf_BWSYNTH"); et != `"BWSYNTH-etag"` {
		t.Errorf("keeping another server's ETag replaced this server's: %q", et)
	}
	crossed := x
	crossed.LastEpoch = "ep_BWSYNTH-2"
	if err := dropEarlier(&crossed); err != nil {
		t.Fatal(err)
	}
	if et, _ := ETag(&x, "drf_BWSYNTH"); et != "" {
		t.Errorf("a command crossing the epoch left %q", et)
	}
	if et, _ := ETag(&again, "drf_BWSYNTH-other"); et != `"BWSYNTH-etag-other"` {
		t.Errorf("crossing the old server's epoch dropped the new server's: %q", et)
	}
	// Kept again at the context's own epoch, so only DropState can remove it.
	if err := KeepETag(&x, "drf_BWSYNTH", `"BWSYNTH-etag"`); err != nil {
		t.Fatal(err)
	}
	if err := DropState(x.Name, x.Server); err != nil {
		t.Fatal(err)
	}
	if et, _ := ETag(&x, "drf_BWSYNTH"); et != "" {
		t.Errorf("a dropped context still has %q", et)
	}
	if et, _ := ETag(&again, "drf_BWSYNTH-other"); et != `"BWSYNTH-etag-other"` {
		t.Errorf("dropping the old server's entry dropped the new server's: %q", et)
	}
	// Forgetting the entry's last ETag writes it with none, which reads back with no map: keeping
	// the next one at the same epoch must still work.
	if err := KeepETag(&again, "drf_BWSYNTH-other", ""); err != nil {
		t.Fatal(err)
	}
	if err := KeepETag(&again, "drf_BWSYNTH-next", `"BWSYNTH-etag-next"`); err != nil {
		t.Fatal(err)
	}
	if et, _ := ETag(&again, "drf_BWSYNTH-next"); et != `"BWSYNTH-etag-next"` {
		t.Errorf("after the last ETag was forgotten, the next was kept as %q", et)
	}
}

// TestRepeatCheckServesOneSend: a passed CheckRepeat covers the next Send of the write only. A
// second Send of the same write confirms the epoch again and refuses once it moved (§8.4 item 3).
func TestRepeatCheckServesOneSend(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-1", recovery: func(n int) string {
		if n == 1 { // n counts this read
			return "ep_BWSYNTH-1"
		}
		return "ep_BWSYNTH-2"
	}, post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	w := clusterWrite()
	w.Repeat = &Repeat{Key: "BWSYNTH-aaaaaaaaaaaaaaa", Epoch: "ep_BWSYNTH-1", Principal: "svc_BWSYNTH"}
	if err := c.CheckRepeat(w); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	var refused RefusedRepeat
	if _, err := c.Send(w); !errors.As(err, &refused) {
		t.Errorf("a second send after the epoch moved: %v, want a refused repeat", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	recoveries := 0
	for _, q := range f.seen {
		if q.URL.Path == "/api/v1/recovery" {
			recoveries++
		}
	}
	if recoveries != 2 || f.posts != 1 {
		t.Errorf("%d recovery reads and %d sends, want 2 and 1", recoveries, f.posts)
	}
}

// TestDeleteNotReplayedByTransport:Go's transport replays an http.NoBody request that carries
// Idempotency-Key on a reused connection the server closed (Request.isReplayable). A body-less
// DELETE must not be one, or that replay skips the recovery read before every repeat (§8.4 item 3).
func TestDeleteNotReplayedByTransport(t *testing.T) {
	var deletes atomic.Int32
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) {
		if deletes.Add(1) == 1 {
			drop(w)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	path := "/drafts/drf_BWSYNTH/profiles/workers"
	w := &Write{Method: http.MethodDelete, Path: path, Check: "draft show", Read: func(*Client) (string, error) { return "BWSYNTH shows", nil }}
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /api/v1/identity", "GET /api/v1/server", "DELETE /api/v1" + path, "GET /api/v1/recovery", "DELETE /api/v1" + path}
	if got := f.requests(); !slices.Equal(got, want) {
		t.Errorf("requests %v, want %v: a DELETE sent again with no recovery read is the transport's replay", got, want)
	}
	for _, r := range f.seen {
		if r.Method == http.MethodDelete && (r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.Header.Get("Content-Type") != "") {
			t.Errorf("a DELETE carried a body: length %d, encoding %v, type %q", r.ContentLength, r.TransferEncoding, r.Header.Get("Content-Type"))
		}
	}
}

// TestETagAcrossEpochs: a command that crosses to a new epoch drops the ETags kept before it, and
// keeps the one its own write's answer brought at the new epoch.
func TestETagAcrossEpochs(t *testing.T) {
	f := &fake{epoch: "ep_BWSYNTH-2", post: func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"BWSYNTH-etag-2"`)
		created(w)
	}}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	if err := UpdateConfig(func(cfg *Config) error { cfg.Contexts = []Context{*c.Context}; return nil }); err != nil {
		t.Fatal(err)
	}
	old := *c.Context
	if err := KeepETag(&old, "drf_BWSYNTH-old", `"BWSYNTH-etag-1"`); err != nil {
		t.Fatal(err)
	}
	w := clusterWrite()
	w.Draft, w.IfMatch = "drf_BWSYNTH", `"BWSYNTH-etag-1"`
	if _, err := c.Send(w); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	x := *c.Context
	if et, err := ETag(&x, "drf_BWSYNTH"); et != `"BWSYNTH-etag-2"` || err != nil {
		t.Errorf("after the epoch change kept %q, %v; want the write's ETag", et, err)
	}
	if et, _ := ETag(&old, "drf_BWSYNTH-old"); et != "" {
		t.Errorf("the earlier epoch's ETag is still kept: %q", et)
	}
	// A command that crosses an epoch without keeping an ETag of its own drops the earlier ones.
	kept := *c.Context
	if err := KeepETag(&kept, "drf_BWSYNTH", `"BWSYNTH-etag-2"`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.epoch = "ep_BWSYNTH-3"
	f.mu.Unlock()
	if _, err := c.Send(clusterWrite()); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if et, _ := ETag(&kept, "drf_BWSYNTH"); et != "" {
		t.Errorf("a command that crossed to ep_BWSYNTH-3 left %q kept at ep_BWSYNTH-2", et)
	}
}

// TestInProgress checks a fragment write's in-progress 409 (§8.4 item 2, AMB-02): after an
// ambiguous attempt, a 409 conflict with ingestion and no draft is repeated with the same key
// until it answers otherwise, counting as no repeat, up to the wait bound, which the served claim
// lease raises; then the outcome is unknown. A 409 with ingestion and draft is a refusal.
func TestInProgress(t *testing.T) {
	inProgress := func(w http.ResponseWriter) {
		problemAnswer(w, http.StatusConflict, "conflict", `,"ingestion":"ing_BWSYNTH"`)
	}
	for _, tc := range []struct {
		name   string
		server string
		wait   time.Duration
		answer func(n int, w http.ResponseWriter)
		posts  int
		waits  []time.Duration
		says   string // "" for success
	}{
		{name: "waited for, then saved", wait: time.Minute, posts: 5, waits: []time.Duration{time.Second, time.Second, 2 * time.Second, 4 * time.Second},
			answer: func(n int, w http.ResponseWriter) {
				switch n {
				case 1:
					drop(w)
				case 5:
					created(w)
				default:
					inProgress(w)
				}
			}},
		{name: "still ingesting at the bound is unknown", wait: 3 * time.Second, posts: 4, waits: []time.Duration{time.Second, time.Second, 2 * time.Second},
			says: "outcome unknown: the first request with this key was still ingesting (ingestion ing_BWSYNTH) after 3s",
			answer: func(n int, w http.ResponseWriter) {
				if n == 1 {
					drop(w)
					return
				}
				inProgress(w)
			}},
		{name: "the served lease raises the bound", wait: time.Second, posts: 4, waits: []time.Duration{time.Second, time.Second, time.Second},
			server: `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal","ingestionClaim":{"leaseSeconds":2,"absoluteExpirySeconds":600}}`,
			says:   "after 2s",
			answer: func(n int, w http.ResponseWriter) {
				if n == 1 {
					drop(w)
					return
				}
				inProgress(w)
			}},
		{name: "a 409 naming the draft is a refusal, not waited for", wait: time.Minute, posts: 2, waits: []time.Duration{time.Second},
			says: "409 BWSYNTH (conflict)",
			answer: func(n int, w http.ResponseWriter) {
				if n == 1 {
					drop(w)
					return
				}
				problemAnswer(w, http.StatusConflict, "conflict", `,"ingestion":"ing_BWSYNTH","draft":"drf_BWSYNTH"`)
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{epoch: "ep_BWSYNTH-1", server: tc.server, post: func(n int, w http.ResponseWriter, _ *http.Request) { tc.answer(n, w) }}
			c, notices, waits := writeClient(t, f, "ep_BWSYNTH-1")
			w := clusterWrite()
			w.Ingests, w.Wait = true, tc.wait
			_, err := c.Send(w)
			var u *Unknown
			switch {
			case tc.says == "" && err != nil:
				t.Errorf("%v; want saved", err)
			case tc.says != "" && (err == nil || !strings.Contains(err.Error(), tc.says)):
				t.Errorf("%v; want it to say %q", err, tc.says)
			case strings.Contains(tc.says, "outcome unknown") && !errors.As(err, &u):
				t.Errorf("%T; want *Unknown", err)
			}
			if f.posts != tc.posts || !slices.Equal(*waits, tc.waits) {
				t.Errorf("%d posts after waits %v; want %d after %v", f.posts, *waits, tc.posts, tc.waits)
			}
			keys := map[string]bool{}
			for _, r := range f.seen {
				if r.Method == http.MethodPost {
					keys[r.Header.Get("Idempotency-Key")] = true
				}
			}
			if len(keys) != 1 {
				t.Errorf("%d keys sent; want one", len(keys))
			}
			if tc.posts > 2 && !strings.Contains(notices.String(), "still ingesting (ingestion ing_BWSYNTH)") {
				t.Errorf("notices %q", notices.String())
			}
		})
	}
}

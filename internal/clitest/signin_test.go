package clitest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/client"
)

// provider adds a token endpoint to the fixture issuer, which has none (client-contracts.md G19),
// so renewal can be tested: answer redeems a refresh token, and calls counts the redemptions.
type provider struct {
	mu     sync.Mutex
	answer func(rt string) (int, any)
	calls  atomic.Int32
}

func (p *provider) set(answer func(rt string) (int, any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = answer
	p.calls.Store(0)
}

// wrap serves the issuer's discovery with a token_endpoint added, the token endpoint, and the
// issuer's other paths unchanged.
func (p *provider) wrap(iss *issuer.Issuer) http.Handler {
	inner := iss.Handler()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		var d map[string]any
		json.Unmarshal(rec.Body.Bytes(), &d)
		d["token_endpoint"] = iss.URL + "/token"
		writeJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p.calls.Add(1)
		p.mu.Lock()
		answer := p.answer
		p.mu.Unlock()
		if answer == nil || r.PostForm.Get("grant_type") != "refresh_token" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		status, body := answer(r.PostForm.Get("refresh_token"))
		writeJSON(w, status, body)
	})
	mux.Handle("/", inner)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// issued is a token endpoint answer: access token at, refresh token rt.
func issued(at, rt string) (int, any) {
	return http.StatusOK, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 300, "refresh_token": rt}
}

// jwtPattern finds a JWT; refreshPattern the synthetic refresh tokens these tests store.
var jwtPattern, refreshPattern = regexp.MustCompile(`eyJ[\w-]+\.eyJ[\w-]+\.`), regexp.MustCompile(`BWSYNTH-rt-\d`)

// signedIn runs bronzewardctl as f.ctl does and also fails on any JWT or refresh token in its
// output (§9.2 item 4).
func (f *fixture) signedIn(args ...string) result {
	f.t.Helper()
	r := f.ctl(nil, "", args...)
	if jwtPattern.MatchString(r.out+r.err) || refreshPattern.MatchString(r.out+r.err) {
		f.t.Errorf("bronzewardctl %v printed a sign-in token:\nstdout: %s\nstderr: %s", args, r.out, r.err)
	}
	return r
}

// mint is a fixture human's token, f.Fatal on an error.
func (f *fixture) mint(human, defect string) string {
	f.t.Helper()
	tok, err := f.iss.Mint(human, defect, time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

// viewerToken is a viewer's token for sub, issued age ago and valid for left.
func (f *fixture) viewerToken(sub string, age, left time.Duration) string {
	f.t.Helper()
	now := time.Now()
	tok, err := f.iss.Sign(map[string]any{"iss": f.iss.URL, "aud": "bronzeward", "sub": sub, "groups": []string{"bw-viewers"},
		"iat": now.Add(-age).Unix(), "nbf": now.Add(-age).Unix(), "exp": now.Add(left).Unix(), "jti": issuer.JTI()})
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

// store writes a token file as bronzewardctl would.
func (f *fixture) store(path, at, rt string) {
	f.t.Helper()
	b, _ := json.Marshal(client.Tokens{AccessToken: at, RefreshToken: rt})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// hold takes the renewal lock at path as bronzewardctl does, until the returned func.
func hold(t *testing.T, path string) func() {
	t.Helper()
	l, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(l.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return func() { l.Close() }
}

// waiting returns once n processes are blocked on the flock of path, as /proc/locks lists them. A
// process blocked there has read the token file already, so no sleep decides the ordering.
func waiting(t *testing.T, path string, n int) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ino := ":" + strconv.FormatUint(fi.Sys().(*syscall.Stat_t).Ino, 10) + " "
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		b, err := os.ReadFile("/proc/locks")
		if err != nil {
			t.Fatal(err)
		}
		blocked := 0
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "-> FLOCK") && strings.Contains(l, ino) {
				blocked++
			}
		}
		if blocked >= n {
			return
		}
	}
	t.Fatalf("%d processes did not block on %s within 10 s", n, path)
}

func (f *fixture) stored(path string) *client.Tokens {
	f.t.Helper()
	t, err := client.ReadTokens(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return t
}

// revoke records the identity revocation of human, as h-recovery.
func (f *fixture) revoke(human string) {
	f.t.Helper()
	body := `{"iss":"` + f.iss.URL + `","sub":"` + human + `","reason":"BWSYNTH revoked for the sign-in cases"}`
	q, _ := http.NewRequest(http.MethodPost, f.base+"/api/v1/identity-revocations", strings.NewReader(body))
	q.Header.Set("Authorization", "Bearer "+f.mint("h-recovery", ""))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", "k-revoke-"+human+"-0123456")
	f.do(q, http.StatusCreated)
}

// humanFile is the token file of the shared cases' oidc context "human".
func (f *fixture) humanFile() string { return filepath.Join(f.home, "human.json") }

func init() {
	reading := "bronzewardctl's own reading of the token, not the server's reason: "
	idToken := "The identity provider sent an ID token, or an access token without jti; the client or the provider's configuration is at fault."
	sharedCases["AUTH-02"] = func(t *testing.T, f *fixture, in *injector) {
		in.set(nil, nil)
		// A first accepted read keeps the server's maximum lifetime for the over-lifetime reading.
		f.store(f.humanFile(), f.mint("h-viewer", ""), "")
		f.want(f.signedIn("--context", "human", "whoami"), 0, "Roles", "viewer",
			"Token (bronzewardctl's reading)", "subject h-viewer, issuer "+f.iss.URL)
		for defect, says := range map[string]string{
			"expired":          "Your sign-in expired: sign in again.",
			"wrong-issuer":     "The token is from " + f.iss.URL + "/other; this context expects " + f.iss.URL + ".",
			"wrong-audience":   "The token is not issued for bronzeward; the identity provider needs an audience mapping.",
			"over-lifetime":    "The token lives longer than this server accepts",
			"alg-none":         "Not accepted by human (" + in.URL + "): sign in again.",
			"groups-not-array": "Not accepted by human (" + in.URL + "): sign in again.",
			"id-token-nonce":   idToken,
			"id-token-at-hash": idToken,
			"no-jti":           idToken,
		} {
			f.store(f.humanFile(), f.mint("h-viewer", defect), "")
			in.set(nil, nil)
			f.want(f.signedIn("--context", "human", "whoami"), 1, "answered 401", "(unauthenticated)", reading+says)
			if seen := in.requests(); len(seen) != 1 {
				t.Errorf("%s: the injector saw %v, want GET /server once and no renewal", defect, seen)
			}
		}
	}
	sharedCases["AUTH-03"] = func(t *testing.T, f *fixture, in *injector) {
		in.set(nil, nil)
		tok, err := f.iss.MintWithoutGroup("h-viewer", "bw-viewers", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		f.store(f.humanFile(), tok, "")
		f.want(f.signedIn("--context", "human", "whoami"), 1, "answered 403", "(forbidden)", "Needs one of the roles [viewer",
			"You are signed in as h-viewer.")
	}
	// revoked runs AUTH-05 and AUTH-10: the stored sign-in is cleared and no new one is tried.
	revoked := func(t *testing.T, f *fixture, in *injector, tok string) {
		f.store(f.humanFile(), tok, "")
		in.set(nil, nil)
		f.want(f.signedIn("--context", "human", "whoami"), 1, "(identity-revoked)", "contact a recovery administrator",
			"The stored sign-in was cleared; signing in again does not restore access.")
		if _, err := os.Stat(f.humanFile()); !os.IsNotExist(err) {
			t.Errorf("the token file is still there: %v", err)
		}
		in.set(nil, nil)
		f.want(f.signedIn("--context", "human", "whoami"), 1, "not signed in", "does not exist")
		if seen := in.requests(); len(seen) != 0 {
			t.Errorf("the injector saw %v after the sign-in was cleared", seen)
		}
	}
	sharedCases["AUTH-05"] = func(t *testing.T, f *fixture, in *injector) { revoked(t, f, in, f.mint("h-author", "")) }
	sharedCases["AUTH-10"] = func(t *testing.T, f *fixture, in *injector) {
		revoked(t, f, in, f.viewerToken("h-denied", 0, issuer.Lifetime))
	}
}

// TestSignIn checks an oidc context's token file, renewal (§9.5, decision T6 of #254) and logout.
func TestSignIn(t *testing.T) {
	f := start(t, "viewer")
	file := filepath.Join(f.home, "signin.json")
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", f.base, "--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli",
		"--audience", "bronzeward", "--oidc-token-file", file), 0, "credential oidc")
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", f.base, "--automation-token-file", f.tokenFile), 0)
	whoami := func() result { return f.signedIn("--context", "human", "whoami") }

	t.Run("token file", func(t *testing.T) {
		f.want(whoami(), 1, "not signed in: the token file "+file+" does not exist")
		f.store(file, f.mint("h-viewer", ""), "")
		f.want(f.signedIn("--context", "human", "--token-file", f.tokenFile, "whoami"), 1, "this context signs in")
		os.Chmod(file, 0o644)
		f.want(whoami(), 1, "make it owner-only")
		os.Chmod(file, 0o600)
		f.want(whoami(), 0, "subject h-viewer")
		// An automation token in the file is the operator's: never used, replaced or removed.
		os.WriteFile(file, []byte(f.token+"\n"), 0o600)
		f.want(whoami(), 1, "holds an automation token")
		f.want(f.signedIn("--context", "human", "logout"), 1, "holds an automation token")
		if b, _ := os.ReadFile(file); string(b) != f.token+"\n" {
			t.Error("the automation token file was changed")
		}
	})

	t.Run("renewal before expiry", func(t *testing.T) {
		f.store(file, f.viewerToken("h-viewer", 270*time.Second, 30*time.Second), "BWSYNTH-rt-1")
		fresh := f.mint("h-viewer", "")
		f.idp.set(func(rt string) (int, any) {
			if rt != "BWSYNTH-rt-1" {
				return http.StatusBadRequest, map[string]string{"error": "invalid_grant"}
			}
			return issued(fresh, "BWSYNTH-rt-2")
		})
		f.want(whoami(), 0, "subject h-viewer")
		if s := f.stored(file); f.idp.calls.Load() != 1 || s.AccessToken != fresh || s.RefreshToken != "BWSYNTH-rt-2" {
			t.Errorf("%d redemptions; the file holds the renewed pair: %v", f.idp.calls.Load(), s.AccessToken == fresh && s.RefreshToken == "BWSYNTH-rt-2")
		}
	})

	t.Run("renewal once after a 401", func(t *testing.T) {
		f.store(file, f.mint("h-viewer", "unknown-kid"), "BWSYNTH-rt-3")
		f.idp.set(func(string) (int, any) { return issued(f.mint("h-viewer", ""), "BWSYNTH-rt-4") })
		f.want(whoami(), 0, "subject h-viewer")
		if n := f.idp.calls.Load(); n != 1 {
			t.Errorf("%d redemptions, want 1", n)
		}
		f.store(file, f.mint("h-viewer", "unknown-kid"), "BWSYNTH-rt-3")
		f.idp.set(func(string) (int, any) { return issued(f.mint("h-viewer", "unknown-kid"), "BWSYNTH-rt-4") })
		f.want(whoami(), 1, "answered 401", "Not accepted by human")
		if n := f.idp.calls.Load(); n != 1 {
			t.Errorf("%d redemptions after a renewed token was refused too, want 1", n)
		}
	})

	t.Run("renewal refused", func(t *testing.T) {
		expired := f.mint("h-viewer", "expired")
		f.store(file, expired, "BWSYNTH-rt-5")
		f.idp.set(func(string) (int, any) { return http.StatusBadRequest, map[string]string{"error": "invalid_grant"} })
		f.want(whoami(), 1, "refused the renewal: 400 Bad Request invalid_grant", "the current token stays in use until the server refuses it",
			"answered 401", "Your sign-in expired: sign in again.")
		if s := f.stored(file); f.idp.calls.Load() != 1 || s.AccessToken != expired || s.RefreshToken != "BWSYNTH-rt-5" {
			t.Errorf("%d redemptions, want 1; the stored sign-in changed", f.idp.calls.Load())
		}
	})

	t.Run("refresh token not rotated", func(t *testing.T) {
		f.store(file, f.viewerToken("h-viewer", 270*time.Second, 30*time.Second), "BWSYNTH-rt-6")
		f.idp.set(func(rt string) (int, any) { return issued(f.mint("h-viewer", ""), rt) })
		f.want(whoami(), 0, "did not rotate the refresh token, so none is kept")
		if s := f.stored(file); s.RefreshToken != "" {
			t.Error("the unrotated refresh token was kept")
		}
	})

	t.Run("new subject", func(t *testing.T) {
		f.store(file, f.viewerToken("h-viewer", 270*time.Second, 30*time.Second), "BWSYNTH-rt-7")
		f.idp.set(func(string) (int, any) { return issued(f.viewerToken("h-other", 0, issuer.Lifetime), "BWSYNTH-rt-8") })
		f.want(whoami(), 0, "a new sign-in: the renewed token is for h-other at "+f.iss.URL+", not h-viewer at "+f.iss.URL, "subject h-other")
	})

	// T6: two processes renewing together redeem the refresh token once; the second uses the
	// pair the first stored. The test holds the renewal lock until both have read the expiring
	// token, so without the lock or the re-read under it both would redeem.
	t.Run("concurrent renewal", func(t *testing.T) {
		f.store(file, f.viewerToken("h-viewer", 270*time.Second, 30*time.Second), "BWSYNTH-rt-9")
		var used atomic.Bool
		f.idp.set(func(rt string) (int, any) {
			if rt != "BWSYNTH-rt-9" || used.Swap(true) {
				return http.StatusBadRequest, map[string]string{"error": "invalid_grant"}
			}
			time.Sleep(500 * time.Millisecond)
			return issued(f.mint("h-viewer", ""), "BWSYNTH-rt-0")
		})
		unlock := hold(t, file+".lock")
		var wg sync.WaitGroup
		results := make([]result, 2)
		for i := range results {
			wg.Go(func() { results[i] = whoami() })
		}
		waiting(t, file+".lock", 2)
		unlock()
		wg.Wait()
		for _, r := range results {
			f.want(r, 0, "subject h-viewer")
		}
		if n := f.idp.calls.Load(); n != 1 {
			t.Errorf("%d redemptions, want 1", n)
		}
	})

	// A logout while another process renews waits for the renewal, then removes the renewed file
	// and states the renewed token's validity.
	t.Run("logout during a renewal", func(t *testing.T) {
		old := f.viewerToken("h-viewer", 270*time.Second, 30*time.Second)
		f.store(file, old, "BWSYNTH-rt-10")
		f.idp.set(func(string) (int, any) {
			time.Sleep(time.Second)
			return issued(f.mint("h-viewer", ""), "BWSYNTH-rt-11")
		})
		var wg sync.WaitGroup
		var renewing result
		wg.Go(func() { renewing = whoami() })
		deadline := time.Now().Add(10 * time.Second)
		for f.idp.calls.Load() == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the renewal did not reach the token endpoint within 10 s")
			}
			time.Sleep(10 * time.Millisecond)
		}
		out := f.signedIn("--context", "human", "logout")
		f.want(out, 0, "Signed out of human")
		if exp := client.ReadClaims(old).ExpiresAt().UTC().Format("2006-01-02 15:04:05 MST"); strings.Contains(out.out, exp) {
			t.Errorf("logout states the replaced token's expiry %s: %s", exp, out.out)
		}
		wg.Wait()
		f.want(renewing, 0, "subject h-viewer")
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the token file is back after the logout: %v", err)
		}
	})

	// A renewal that finds the file removed while it waited for the lock refuses: it redeems
	// nothing and writes nothing back.
	t.Run("removed while a renewal waits", func(t *testing.T) {
		f.store(file, f.viewerToken("h-viewer", 270*time.Second, 30*time.Second), "BWSYNTH-rt-12")
		f.idp.set(func(string) (int, any) { return issued(f.mint("h-viewer", ""), "BWSYNTH-rt-13") })
		unlock := hold(t, file+".lock")
		var wg sync.WaitGroup
		var renewing result
		wg.Go(func() { renewing = whoami() })
		waiting(t, file+".lock", 1)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		unlock()
		wg.Wait()
		f.want(renewing, 0, "subject h-viewer", "the stored sign-in was removed meanwhile")
		if n := f.idp.calls.Load(); n != 0 {
			t.Errorf("%d redemptions, want 0", n)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the renewal wrote the removed token file back: %v", err)
		}
	})

	t.Run("logout", func(t *testing.T) {
		tok := f.mint("h-viewer", "")
		f.store(file, tok, "BWSYNTH-rt-1")
		until := client.ReadClaims(tok).ExpiresAt().UTC().Format("2006-01-02 15:04:05 MST")
		f.want(f.signedIn("--context", "human", "logout"), 0, "Signed out of human", "the token file "+file+" is removed",
			"The access token already issued stays valid at the server until "+until+", plus the server's 60 s leeway.",
			"The refresh token is not revoked at the identity provider")
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the token file is still there: %v", err)
		}
		f.store(file, f.mint("h-viewer", "expired"), "")
		r := f.signedIn("--context", "human", "logout")
		f.want(r, 0, "already issued expired at ", "the server accepts it for at most 60 s after that")
		if strings.Contains(r.out, "refresh token") {
			t.Errorf("logout names a refresh token none was stored for:\n%s", r.out)
		}
		os.WriteFile(file, []byte("{}"), 0o600)
		f.want(f.signedIn("--context", "human", "logout"), 0, "the token file "+file+" was unreadable and is removed")
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the unreadable token file is still there: %v", err)
		}
		os.WriteFile(file, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n"), 0o600)
		f.want(f.signedIn("--context", "human", "logout"), 1, "holds neither bronzewardctl's tokens nor an automation token")
		if _, err := os.Stat(file); err != nil {
			t.Errorf("logout removed a file that is not bronzewardctl's: %v", err)
		}
		for _, foreign := range []string{`{"type":"service_account","private_key":"BWSYNTH-key"}`, `null`, `{"accessToken":""} {}`} {
			os.WriteFile(file, []byte(foreign), 0o600)
			f.want(f.signedIn("--context", "human", "logout"), 1, "holds neither bronzewardctl's tokens nor an automation token")
			if b, err := os.ReadFile(file); err != nil || string(b) != foreign {
				t.Errorf("logout changed the foreign file %s: %q, %v", foreign, b, err)
			}
		}
		os.Remove(file)
		f.want(f.signedIn("--context", "human", "logout"), 0, "Not signed in to human")
		f.want(f.signedIn("--context", "lab", "logout"), 1, "an automation context has no sign-in")
		if _, err := os.Stat(f.tokenFile); err != nil {
			t.Errorf("the automation token file: %v", err)
		}
	})
}

// TestSignInLeakScan is the sign-in leak scan's positive control.
func TestSignInLeakScan(t *testing.T) {
	key, _ := issuer.NewKey()
	iss, _ := issuer.New("http://127.0.0.1:1", "bronzeward", key)
	tok, err := iss.Mint("h-viewer", "", time.Now())
	if err != nil || !jwtPattern.MatchString("x "+tok+" y") || !refreshPattern.MatchString("BWSYNTH-rt-1") || jwtPattern.MatchString("eyJhbGciOi") {
		t.Fatalf("the sign-in leak scan misses a token or flags a header alone: %v", err)
	}
}

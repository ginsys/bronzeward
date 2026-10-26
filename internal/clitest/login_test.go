package clitest

import (
	"bufio"
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/client"
)

// scan fails on any JWT, refresh token or the fixture's automation token in r (§9.2 item 4).
func (f *fixture) scan(r result, args []string) result {
	f.t.Helper()
	if jwtPattern.MatchString(r.out+r.err) || refreshPattern.MatchString(r.out+r.err) {
		f.t.Errorf("bronzewardctl %v printed a sign-in token:\nstdout: %s\nstderr: %s", args, r.out, r.err)
	}
	for _, where := range r.leaked(f.token, "", args) {
		f.t.Errorf("bronzewardctl %v: %s holds a token", args, where)
	}
	return r
}

// interact runs bronzewardctl as the operator would: act sees each line it prints on standard
// error, so it can play the browser or the second device. The run is killed after a minute.
func (f *fixture) interact(env []string, act func(line string), args ...string) result {
	f.t.Helper()
	cmd := f.command(env, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	stderr, err := cmd.StderrPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		f.t.Fatalf("bronzewardctl %v: %v", args, err)
	}
	kill := time.AfterFunc(time.Minute, func() { cmd.Process.Kill() })
	defer kill.Stop()
	var errb strings.Builder
	for s := bufio.NewScanner(stderr); s.Scan(); {
		errb.WriteString(s.Text() + "\n")
		act(s.Text())
	}
	r := result{}
	var exit *exec.ExitError
	if err := cmd.Wait(); errors.As(err, &exit) {
		r.code = exit.ExitCode()
	} else if err != nil {
		f.t.Errorf("bronzewardctl %v: %v", args, err)
		r.code = -1
	}
	r.out, r.err = out.String(), errb.String()
	return f.scan(r, args)
}

// browser opens the authorization address bronzewardctl prints, as the operator's browser does;
// the provider's redirect then reaches bronzewardctl's loopback listener.
func (f *fixture) browser(line string) {
	u := strings.TrimSpace(line)
	if !strings.HasPrefix(u, f.iss.URL+"/authorize?") {
		return
	}
	r, err := http.Get(u)
	if err != nil {
		f.t.Errorf("the browser: %v", err)
		return
	}
	r.Body.Close()
}

// keyring starts a private session bus with gnome-keyring's Secret Service, its login keyring
// unlocked with a synthetic password, and returns the environment that names it. Nothing reaches
// the operator's own session bus or keyring. Skipped where either daemon is not installed.
func keyring(t *testing.T) []string {
	for _, b := range []string{"dbus-daemon", "dbus-send", "gnome-keyring-daemon"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skipf("%s is not installed", b)
		}
	}
	// Not t.TempDir: its long name pushes the keyring's control socket past the 108-byte limit
	// on a Unix socket path.
	d, err := os.MkdirTemp("", "bwkr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	home, run := filepath.Join(d, "home"), filepath.Join(d, "run")
	for _, p := range []string{home, run} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	addr := "unix:path=" + filepath.Join(d, "bus")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"XDG_RUNTIME_DIR=" + run, "DBUS_SESSION_BUS_ADDRESS=" + addr}
	daemon := func(stdin string, name string, args ...string) {
		var log bytes.Buffer
		cmd := exec.Command(name, args...)
		cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, strings.NewReader(stdin), &log, &log
		if err := cmd.Start(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Cleanup(func() {
			cmd.Process.Kill()
			cmd.Wait()
			if t.Failed() {
				t.Logf("%s:\n%s", name, log.String())
			}
		})
	}
	daemon("", "dbus-daemon", "--session", "--nofork", "--address="+addr)
	owned := func() bool {
		cmd := exec.Command("dbus-send", "--session", "--print-reply", "--dest=org.freedesktop.DBus", "/org/freedesktop/DBus",
			"org.freedesktop.DBus.NameHasOwner", "string:org.freedesktop.secrets")
		cmd.Env = env
		b, err := cmd.Output()
		return err == nil && strings.Contains(string(b), "boolean true")
	}
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(d, "bus")); err == nil {
			break
		} else if i == 50 {
			t.Fatal("the private session bus did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	daemon("BWSYNTH-keyring", "gnome-keyring-daemon", "--unlock", "--components=secrets", "--foreground")
	for i := 0; !owned(); i++ {
		if i == 50 {
			t.Fatal("gnome-keyring-daemon did not take the Secret Service name")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return []string{"DBUS_SESSION_BUS_ADDRESS=" + addr}
}

// TestLogin checks bronzewardctl login and the stores it keeps a sign-in in (§9.2, decisions
// T3-T5 and D2 of #254) against the fixture provider.
func TestLogin(t *testing.T) {
	f := start(t, "viewer")
	file := filepath.Join(f.home, "login.json")
	add := func(env []string, name string, extra ...string) {
		args := append([]string{"context", "add", name, "--server", f.base, "--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli",
			"--audience", "bronzeward"}, extra...)
		f.want(f.ctl(env, "", args...), 0, "credential oidc")
	}
	add(nil, "file", "--oidc-token-file", file)
	add(nil, "memory")
	f.idp.revoking(func() (int, any) { return http.StatusOK, map[string]any{} })
	defer f.idp.revoking(nil)
	login := func(env []string, ctx string, extra ...string) result {
		return f.interact(env, f.browser, append([]string{"--context", ctx, "login"}, extra...)...)
	}

	t.Run("loopback", func(t *testing.T) {
		f.idp.login(nil, "")
		f.want(login(nil, "file"), 0, "Sign in to file", "Signed in to file", "as h-viewer at "+f.iss.URL, "kept in the token file "+file)
		q := f.idp.asked()
		redirect, _ := url.Parse(q.Get("redirect_uri"))
		if q.Get("response_type") != "code" || q.Get("client_id") != "BWSYNTH-cli" || q.Get("code_challenge_method") != "S256" ||
			q.Get("code_challenge") == "" || len(q.Get("state")) < 32 || len(q.Get("nonce")) < 32 || q.Get("scope") != "openid offline_access" ||
			redirect == nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.Path != "/callback" {
			t.Errorf("the authorization request: %v", q)
		}
		if s := f.stored(file); f.idp.exchange.Load() != 1 || s.RefreshToken != "BWSYNTH-rt-1" {
			t.Errorf("%d exchanges; the file holds the refresh token: %v", f.idp.exchange.Load(), s.RefreshToken == "BWSYNTH-rt-1")
		}
		f.want(f.signedIn("--context", "file", "whoami"), 0, "subject h-viewer")
		// The sign-in is written under the renewal lock, after a renewal or clear holding it.
		f.idp.login(nil, "")
		unlock := hold(t, file+".lock")
		var r result
		var wg sync.WaitGroup
		wg.Go(func() { r = login(nil, "file") })
		waiting(t, file+".lock", 1)
		unlock()
		wg.Wait()
		f.want(r, 0, "Signed in to file")
		// --no-input forbids the wait for the operator.
		f.want(f.ctl(nil, "", "--context", "file", "--no-input", "login"), 1, "which --no-input forbids")
		// A token file that is not this client's is refused before the sign-in and left as it is.
		for _, other := range []string{"bwt_BWSYNTH-automation\n", "kind: Config\nclusters: []\n", `{"theirs":"BWSYNTH"}`} {
			if err := os.WriteFile(file, []byte(other), 0o600); err != nil {
				t.Fatal(err)
			}
			f.idp.login(nil, "")
			n := f.idp.exchange.Load()
			f.want(login(nil, "file"), 1, "token file "+file)
			if b, _ := os.ReadFile(file); string(b) != other || f.idp.exchange.Load() != n {
				t.Errorf("the file now holds %q after %d exchanges, want it unchanged with none", b, f.idp.exchange.Load()-n)
			}
		}
		// A sign-in that cannot be kept, here because another program's file replaced the token file
		// during the sign-in, has its refresh token revoked, and says so when the provider refuses.
		replaced := func() result {
			os.Remove(file)
			f.idp.login(nil, "")
			unlock := hold(t, file+".lock")
			var r result
			var wg sync.WaitGroup
			wg.Go(func() { r = login(nil, "file") })
			waiting(t, file+".lock", 1)
			if err := os.WriteFile(file, []byte("kind: Config\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			unlock()
			wg.Wait()
			if b, _ := os.ReadFile(file); string(b) != "kind: Config\n" {
				t.Errorf("the replaced file now holds %q", b)
			}
			return r
		}
		f.want(replaced(), 1, "holds neither bronzewardctl's tokens", "its refresh token is revoked at the identity provider")
		if r := f.idp.revocations(); len(r) != 1 || r[0].Get("token") != "BWSYNTH-rt-1" {
			t.Errorf("revoked %v, want the new refresh token", r)
		}
		f.idp.revoking(func() (int, any) { return http.StatusBadRequest, map[string]string{"error": "invalid_request"} })
		f.want(replaced(), 1, "holds neither bronzewardctl's tokens", "its refresh token is not revoked, so it stays usable",
			"refused the revocation")
		f.idp.revoking(func() (int, any) { return http.StatusOK, map[string]any{} })
		os.Remove(file)
	})

	// A context edited by hand is refused before its store is reached.
	t.Run("hand-edited", func(t *testing.T) {
		f.edit(func(c *client.Config) {
			c.Contexts = append(c.Contexts,
				client.Context{Name: "a/b", Server: f.base, Credential: "oidc", OIDC: &client.OIDC{Issuer: f.iss.URL, ClientID: "BWSYNTH-cli"}},
				client.Context{Name: "bare", Server: f.base, Credential: "oidc"})
		})
		defer f.edit(func(c *client.Config) {
			c.Contexts = slices.DeleteFunc(c.Contexts, func(x client.Context) bool { return x.Name == "a/b" || x.Name == "bare" })
		})
		f.want(f.ctl(nil, "", "--context", "a/b", "logout"), 1, "without spaces or slashes")
		f.want(f.ctl(nil, "", "--context", "bare", "logout"), 1, "an oidc context needs its issuer and client ID")
		f.want(f.ctl(nil, "", "--context", "bare", "login"), 1, "an oidc context needs its issuer and client ID")
	})

	// Keys on plain http off loopback would let an on-path host sign the ID token.
	t.Run("plain-http keys", func(t *testing.T) {
		f.idp.login(nil, "")
		f.idp.mu.Lock()
		f.idp.discovery = "keys"
		f.idp.mu.Unlock()
		r := login(nil, "file")
		f.idp.mu.Lock()
		f.idp.discovery = ""
		f.idp.mu.Unlock()
		f.want(r, 1, "the identity provider's jwks_uri", "must be https")
	})

	t.Run("another state", func(t *testing.T) {
		f.idp.login(func(v url.Values) url.Values { v.Set("state", "BWSYNTH-state"); return v }, "")
		f.want(login(nil, "file"), 1, "carries another state", "nothing was exchanged")
		if n := f.idp.exchange.Load(); n != 0 {
			t.Errorf("%d exchanges, want none", n)
		}
	})

	t.Run("refused", func(t *testing.T) {
		f.idp.login(func(v url.Values) url.Values { v.Del("code"); v.Set("error", "access_denied"); return v }, "")
		f.want(login(nil, "file"), 1, "refused the sign-in: access_denied")
		// Only an RFC 6749 error code is shown, never control characters the answer carries.
		f.idp.login(func(v url.Values) url.Values { v.Del("code"); v.Set("error", "\x1b[2Jdenied"); return v }, "")
		r := login(nil, "file")
		f.want(r, 1, "refused the sign-in: an error code that is not shown")
		if strings.ContainsRune(r.out+r.err, '\x1b') {
			t.Errorf("login showed a control character from the sign-in answer:\n%s%s", r.out, r.err)
		}
	})

	t.Run("another nonce", func(t *testing.T) {
		f.idp.login(nil, "BWSYNTH-nonce")
		f.want(login(nil, "file"), 1, "the ID token carries another nonce")
	})

	t.Run("device", func(t *testing.T) {
		f.idp.login(nil, "")
		os.Remove(file)
		approve := func(line string) {
			if strings.Contains(line, "enter the code BWSY-NTH1") {
				f.idp.approved.Store(true)
			}
		}
		host := strings.TrimPrefix(f.iss.URL, "http://")
		f.want(f.interact(nil, approve, "--context", "file", "login", "--device"), 0, "open "+f.iss.URL+"/activate?client=cli on any device",
			"Check that the page's host is: "+strings.ToUpper(host), "Signed in to file", "kept in the token file "+file)
		f.want(f.signedIn("--context", "file", "whoami"), 0, "subject h-viewer")
		// A verification page on plain http off loopback is refused before it is shown.
		f.idp.mu.Lock()
		f.idp.verification = "http://idp.example.invalid/activate"
		f.idp.mu.Unlock()
		r := f.ctl(nil, "", "--context", "file", "login", "--device")
		f.idp.mu.Lock()
		f.idp.verification = ""
		f.idp.mu.Unlock()
		f.want(r, 1, "the identity provider's verification_uri", "must be https")
	})

	t.Run("logout revokes", func(t *testing.T) {
		f.idp.login(nil, "")
		f.want(f.signedIn("--context", "file", "logout"), 0, "the token file "+file+" is removed",
			"The refresh token is revoked at the identity provider.")
		if r := f.idp.revocations(); len(r) != 1 || r[0].Get("token") != "BWSYNTH-rt-1" {
			t.Errorf("revoked %v, want the stored refresh token", r)
		}
	})

	// D2 (a): with no store, each command signs in for itself, which --no-input refuses; with no
	// desktop, as here, with the device grant (D4 (a)).
	t.Run("memory", func(t *testing.T) {
		f.idp.login(nil, "")
		f.want(f.signedIn("--context", "memory", "--no-input", "whoami"), 1,
			"the sign-in is kept in this process's memory only, since no D-Bus session bus is named", "--no-input forbids")
		approve := func(line string) {
			if strings.Contains(line, "enter the code BWSY-NTH1") {
				f.idp.approved.Store(true)
			}
		}
		f.want(f.interact(nil, approve, "--context", "memory", "whoami"), 0, "To sign in to memory", "subject h-viewer")
		f.want(login(nil, "memory"), 0, "kept in this process's memory only", "each later command signs in for itself")
		unreached := "A sign-in another session kept in the system credential store is not reached, so it is neither removed nor revoked."
		f.want(f.signedIn("--context", "memory", "logout"), 0, "Not signed in to memory (", "the sign-in is kept in this process's memory only",
			"so none is stored", unreached)
		f.want(f.ctl(nil, "", "context", "remove", "memory"), 0, `Context "memory" removed.`,
			"The system credential store is not reached, since no D-Bus session bus is named", "so a sign-in another session kept there is not removed.")
		// A named store that cannot be reached fails the removal of its entry.
		add(nil, "unreachable")
		bus := []string{"DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(t.TempDir(), "missing")}
		f.want(f.ctl(bus, "", "context", "remove", "unreachable"), 1, `Context "unreachable" removed.`,
			`removing the stored sign-in of context "unreachable" from the system credential store`)
	})

	// T3/T4: the system credential store, a private Secret Service here.
	t.Run("system credential store", func(t *testing.T) {
		env := keyring(t)
		f.idp.login(nil, "")
		ctl := func(args ...string) result { return f.scan(f.ctl(env, "", args...), args) }
		add(env, "store")
		f.want(login(env, "store"), 0, "kept in the system credential store")
		f.want(ctl("--context", "store", "whoami"), 0, "subject h-viewer")
		f.want(ctl("--context", "store", "logout"), 0, "the sign-in is removed from the system credential store",
			"The refresh token is revoked at the identity provider.")
		if r := f.idp.revocations(); len(r) != 1 || r[0].Get("token") != "BWSYNTH-rt-1" {
			t.Errorf("revoked %v, want the stored refresh token", r)
		}
		f.want(ctl("--context", "store", "logout"), 0, "Not signed in to store", "nothing is stored in the system credential store")
		f.want(ctl("--context", "store", "whoami"), 1, "nothing is stored in the system credential store")
		// Removing the context removes its entry: a context of the same name starts signed out.
		f.want(login(env, "store"), 0, "kept in the system credential store")
		f.want(ctl("context", "remove", "store"), 0, `Context "store" removed.`)
		add(env, "store")
		f.want(ctl("--context", "store", "whoami"), 1, "nothing is stored in the system credential store")
		// context remove deletes the entry under the renewal lock, after a renewal holding it.
		f.want(login(env, "store"), 0, "kept in the system credential store")
		lock := filepath.Join(f.home, ".local", "state", "bronzeward", "store.renewal.lock")
		unlock := hold(t, lock)
		var r result
		var wg sync.WaitGroup
		wg.Go(func() { r = ctl("context", "remove", "store") })
		waiting(t, lock, 1)
		unlock()
		wg.Wait()
		f.want(r, 0, `Context "store" removed.`)
		add(env, "store")
		f.want(ctl("--context", "store", "whoami"), 1, "nothing is stored in the system credential store")
		// An entry left under a context edited to automation is never read for it.
		f.want(login(env, "store"), 0, "kept in the system credential store")
		f.edit(func(c *client.Config) {
			if x := c.Find("store"); x != nil {
				x.Credential, x.OIDC = "automation", nil
			}
		})
		f.want(login(env, "store"), 1, "an automation context has no sign-in")
	})
}

// edit changes the contexts file by hand, as an operator may.
func (f *fixture) edit(change func(*client.Config)) {
	f.t.Helper()
	path := filepath.Join(f.home, "bronzeward", "config.yaml")
	b, err := os.ReadFile(path)
	var c client.Config
	if err == nil {
		err = yaml.Unmarshal(b, &c)
	}
	if err != nil {
		f.t.Fatalf("contexts file: %v", err)
	}
	change(&c)
	if b, err = yaml.Marshal(&c); err == nil {
		err = os.WriteFile(path, b, 0o600)
	}
	if err != nil {
		f.t.Fatal(err)
	}
}

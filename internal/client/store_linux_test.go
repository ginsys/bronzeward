package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

// TestKeyringWritesNotAbandoned checks that a credential-store write slower than within's bound is
// waited for, not left running by within: its own bound is the Secret Service calls' deadline
// (TestSecretServiceBounded).
func TestKeyringWritesNotAbandoned(t *testing.T) {
	bound, set, del := storeBound, keyringSetCall, keyringDeleteCall
	t.Cleanup(func() { storeBound, keyringSetCall, keyringDeleteCall = bound, set, del })
	storeBound = 10 * time.Millisecond
	var wrote atomic.Int32
	slow := func() { time.Sleep(100 * time.Millisecond); wrote.Add(1) }
	keyringSetCall = func(string, string, string) error { slow(); return nil }
	keyringDeleteCall = func(string, string) error { slow(); return nil }
	if err := keyringSet("BWSYNTH-context", "{}"); err != nil || wrote.Load() != 1 {
		t.Errorf("keyringSet returned %v before its write finished (%d writes)", err, wrote.Load())
	}
	if err := keyringDelete("BWSYNTH-context"); err != nil || wrote.Load() != 2 {
		t.Errorf("keyringDelete returned %v before its deletion finished (%d writes)", err, wrote.Load())
	}
}

// TestKeyringLockedRefused checks that the renewal lock is never held across the credential
// store's unlock prompt: the read that may prompt runs before the lock, and under the lock only the
// calls that refuse a locked store run, so a store locked by then is refused with nothing written
// or deleted, and the lock released.
func TestKeyringLockedRefused(t *testing.T) {
	get, getHeld, set, del := keyringGetCall, keyringGetHeldCall, keyringSetCall, keyringDeleteCall
	t.Cleanup(func() { keyringGetCall, keyringGetHeldCall, keyringSetCall, keyringDeleteCall = get, getHeld, set, del })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	lockFile := filepath.Join(home, "state", "bronzeward", "store.renewal.lock")
	// free reports whether the renewal lock is free, without waiting for it.
	free := func() bool {
		if err := os.MkdirAll(filepath.Dir(lockFile), 0o700); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
	}
	// The stored sign-in, kept by the stubs below while the store is unlocked.
	b, err := json.Marshal(entry{Server: x.Server, Issuer: x.OIDC.Issuer, Tokens: Tokens{AccessToken: "BWSYNTH-at", RefreshToken: "BWSYNTH-rt"}})
	if err != nil {
		t.Fatal(err)
	}
	var isLocked atomic.Bool
	var reads, promptedHeld, heldReads, writes atomic.Int32
	keyringGetCall = func(string, string) (string, error) {
		reads.Add(1)
		if !free() {
			promptedHeld.Add(1)
		}
		return string(b), nil
	}
	// refuse is what each call made under the lock answers on a locked store, with nothing done.
	refuse := func(did *atomic.Int32) error {
		if free() {
			t.Error("a call meant for under the renewal lock ran without it")
		}
		if isLocked.Load() {
			return errStoreLocked
		}
		did.Add(1)
		return nil
	}
	keyringGetHeldCall = func(string, string) (string, error) { return string(b), refuse(&heldReads) }
	keyringSetCall = func(string, string, string) error { return refuse(&writes) }
	keyringDeleteCall = func(string, string) error { return refuse(&writes) }
	s := openStore(&x, false)

	// The control: an unlocked store is written under the lock, read first outside it.
	unlock, err := s.lock()
	if err == nil {
		err = s.save(&Tokens{AccessToken: "BWSYNTH-at"})
		unlock()
	}
	if err != nil || writes.Load() != 1 || reads.Load() != 1 || promptedHeld.Load() != 0 {
		t.Fatalf("an unlocked store: %v, %d writes, %d reads, %d under the lock; want one write, one read before the lock",
			err, writes.Load(), reads.Load(), promptedHeld.Load())
	}

	// From here the store is locked again once the lock is held: the read before it still answers.
	isLocked.Store(true)
	writes.Store(0)
	// held runs f under the lock, which must be released afterwards.
	held := func(f func() error) error {
		err := f()
		if !free() {
			t.Error("the renewal lock is still held after the call returned")
		}
		return err
	}
	if err := held(func() error {
		unlock, err := s.lock()
		if err != nil {
			return err
		}
		defer unlock()
		return s.save(&Tokens{AccessToken: "BWSYNTH-at2"})
	}); !errors.Is(err, errStoreLocked) {
		t.Errorf("a sign-in kept in a locked store: %v; want errStoreLocked", err)
	}
	if err := held(func() error { _, err := s.clear(nil); return err }); !errors.Is(err, errStoreLocked) {
		t.Errorf("clear on a locked store: %v, want errStoreLocked", err)
	}
	if err := held(func() error { _, err := ClearStored(&x, false); return err }); err == nil || !errors.Is(err, errStoreLocked) ||
		!strings.Contains(err.Error(), `removing the stored sign-in of context "store" from the system credential store`) {
		t.Errorf("context remove on a locked store: %v, want the removal named and errStoreLocked", err)
	}
	c := &Client{Context: &x, Notices: io.Discard,
		signIn: &signIn{store: s, tokens: &Tokens{AccessToken: "BWSYNTH-at", RefreshToken: "BWSYNTH-rt"}}}
	// A renewal that read the store with a prompt, or reached the identity provider, fails otherwise.
	if err := held(c.renew); !errors.Is(err, errStoreLocked) {
		t.Errorf("a renewal on a locked store: %v, want errStoreLocked before the identity provider is asked", err)
	}
	if writes.Load() != 0 || heldReads.Load() != 0 || promptedHeld.Load() != 0 {
		t.Errorf("%d writes or deletions and %d reads on a locked store, %d prompting reads under the lock; want none",
			writes.Load(), heldReads.Load(), promptedHeld.Load())
	}
	// A store whose read before the lock fails: context remove names the removal too.
	keyringGetCall = func(string, string) (string, error) { return "", errors.New("BWSYNTH-no-answer") }
	if _, err := ClearStored(&x, false); err == nil || !strings.Contains(err.Error(),
		`removing the stored sign-in of context "store" from the system credential store: BWSYNTH-no-answer`) {
		t.Errorf("context remove on a store that does not answer: %v, want the removal named", err)
	}
}

// TestKeyringErrorWithLineFeedIsShownEscaped: the credential store's error text is not the
// client's. With a line feed in it, the error that reaches the terminal through Shown, the sink,
// holds no line feed of it, where the error's own text does.
func TestKeyringErrorWithLineFeedIsShownEscaped(t *testing.T) {
	get := keyringGetCall
	t.Cleanup(func() { keyringGetCall = get })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	keyringGetCall = func(string, string) (string, error) { return "", errors.New("BWSYNTH-no-answer\nFORGED") }
	_, err := ClearStored(&x, false)
	if err == nil || !strings.Contains(err.Error(), "BWSYNTH-no-answer\nFORGED") {
		t.Fatalf("context remove on a store that fails: %v, want its error with the line feed (the control)", err)
	}
	if shown := Shown(x.fail(err)); strings.Contains(shown, "\n") || !strings.Contains(shown, `BWSYNTH-no-answer\x0aFORGED`) {
		t.Errorf("the error as shown: %q, want the line feed escaped", shown)
	}
}

// TestNoInputNeverPrompts checks that with --no-input no call to the credential store can show its
// unlock prompt, before the renewal lock as under it: a locked store is refused instead.
func TestNoInputNeverPrompts(t *testing.T) {
	get, getHeld, set, del := keyringGetCall, keyringGetHeldCall, keyringSetCall, keyringDeleteCall
	t.Cleanup(func() { keyringGetCall, keyringGetHeldCall, keyringSetCall, keyringDeleteCall = get, getHeld, set, del })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	var prompting, refused, writes atomic.Int32
	keyringGetCall = func(string, string) (string, error) { prompting.Add(1); return "", errStoreLocked }
	keyringGetHeldCall = func(string, string) (string, error) { refused.Add(1); return "", errStoreLocked }
	keyringSetCall = func(string, string, string) error { writes.Add(1); return nil }
	keyringDeleteCall = func(string, string) error { writes.Add(1); return nil }

	// The control: without --no-input the store is read with go-keyring's prompting call.
	if _, err := ClearStored(&x, false); err == nil || prompting.Load() != 1 {
		t.Fatalf("a command without --no-input: %v after %d prompting reads; want one", err, prompting.Load())
	}
	prompting.Store(0)
	refused.Store(0)
	if _, err := openSignIn(&x, true, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "the system credential store is locked") || !strings.Contains(err.Error(), "--no-input forbids") {
		t.Errorf("a command with --no-input on a locked store: %v, want it refused, naming the locked store", err)
	}
	if _, err := SignOut(&x, true); !errors.Is(err, errStoreLocked) {
		t.Errorf("logout --no-input on a locked store: %v, want errStoreLocked", err)
	}
	if _, err := ClearStored(&x, true); !errors.Is(err, errStoreLocked) {
		t.Errorf("context remove --no-input on a locked store: %v, want errStoreLocked", err)
	}
	if prompting.Load() != 0 || refused.Load() != 3 || writes.Load() != 0 {
		t.Errorf("with --no-input: %d prompting reads, %d refused reads, %d writes or deletions; want 0, 3, 0",
			prompting.Load(), refused.Load(), writes.Load())
	}

	// A context that lost its oidc section still has its stored entry removed, with or without
	// --no-input: the read before the lock does not decode the entry.
	b, err := json.Marshal(entry{Server: x.Server, Issuer: x.OIDC.Issuer, Tokens: Tokens{AccessToken: "BWSYNTH-at"}})
	if err != nil {
		t.Fatal(err)
	}
	keyringGetCall = func(string, string) (string, error) { return string(b), nil }
	keyringGetHeldCall = keyringGetCall
	bare := Context{Name: x.Name, Server: x.Server, Credential: "oidc"}
	for _, noInput := range []bool{false, true} {
		writes.Store(0)
		if _, err := ClearStored(&bare, noInput); err != nil || writes.Load() != 1 {
			t.Errorf("context remove (--no-input %v) without an oidc section: %v, %d deletions; want the entry removed",
				noInput, err, writes.Load())
		}
	}
}

// TestSecretServiceNeverPrompts checks the calls made under the renewal lock against gnome-keyring
// on a private session bus: they read and write what go-keyring does, in the collection it uses,
// and a locked collection refuses each of them at once, with nothing written or removed.
func TestSecretServiceNeverPrompts(t *testing.T) {
	secretServiceBus(t)
	const svc = "BWSYNTH-service"
	if err := ssSet(svc, "a", "BWSYNTH-a"); err != nil {
		t.Fatalf("ssSet: %v", err)
	}
	if v, err := keyring.Get(svc, "a"); err != nil || v != "BWSYNTH-a" {
		t.Fatalf("go-keyring read %q, %v of ssSet's entry", v, err)
	}
	if err := keyring.Set(svc, "b", "BWSYNTH-b"); err != nil {
		t.Fatal(err)
	}
	if v, err := ssGet(svc, "b"); err != nil || v != "BWSYNTH-b" {
		t.Fatalf("ssGet read %q, %v of go-keyring's entry", v, err)
	}
	if err := ssDelete(svc, "b"); err != nil {
		t.Fatalf("ssDelete: %v", err)
	}
	if _, err := keyring.Get(svc, "b"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("go-keyring after ssDelete: %v, want ErrNotFound", err)
	}
	if _, err := ssGet(svc, "b"); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("ssGet after ssDelete: %v, want ErrNotFound", err)
	}

	secretServiceLock(t, true)
	for name, call := range map[string]func() error{
		"ssSet":    func() error { return ssSet(svc, "a", "BWSYNTH-a2") },
		"ssDelete": func() error { return ssDelete(svc, "a") },
		"ssGet":    func() error { _, err := ssGet(svc, "a"); return err },
	} {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if !errors.Is(err, errStoreLocked) {
				t.Errorf("%s on a locked collection: %v, want errStoreLocked", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s on a locked collection is still waiting after 10 s", name)
		}
	}
	secretServiceLock(t, false)
	if v, err := keyring.Get(svc, "a"); err != nil || v != "BWSYNTH-a" {
		t.Errorf("after the refusals the entry reads %q, %v; want it unchanged", v, err)
	}
}

// secretServiceBus starts a private session bus with gnome-keyring's Secret Service, its login
// keyring unlocked with a synthetic password, and names it in DBUS_SESSION_BUS_ADDRESS. Nothing
// reaches the operator's own session bus or keyring. Skipped where either daemon is not installed,
// or failed with BW_REQUIRE_KEYRING=1 (CI's go-db job), so that job cannot skip it.
func secretServiceBus(t *testing.T) {
	for _, b := range []string{"dbus-daemon", "gnome-keyring-daemon"} {
		if _, err := exec.LookPath(b); err != nil {
			if os.Getenv("BW_REQUIRE_KEYRING") == "1" {
				t.Fatalf("%s is not installed, and BW_REQUIRE_KEYRING=1", b)
			}
			t.Skipf("%s is not installed", b)
		}
	}
	// Not t.TempDir: its long name pushes the keyring's control socket past the 108-byte limit on
	// a Unix socket path.
	d, err := os.MkdirTemp("", "bwss")
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
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(d, "bus")); err == nil {
			break
		} else if i == 50 {
			t.Fatal("the private session bus did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	daemon("BWSYNTH-keyring", "gnome-keyring-daemon", "--unlock", "--components=secrets", "--foreground")
	for i := 0; ; i++ {
		c, err := dbus.ConnectSessionBus()
		var owned bool
		if err == nil {
			err = c.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, ssName).Store(&owned)
			c.Close()
		}
		if owned {
			return
		} else if i == 50 {
			t.Fatalf("gnome-keyring-daemon did not take the Secret Service name: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// secretServiceLock locks the login collection, or unlocks it with the synthetic password through
// gnome-keyring's own interface, since the Secret Service's Unlock would prompt.
func secretServiceLock(t *testing.T, lock bool) {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	o := c.Object(ssName, ssPath)
	if lock {
		var locked []dbus.ObjectPath
		var prompt dbus.ObjectPath
		if err := o.Call(ssIface+"Service.Lock", 0, []dbus.ObjectPath{ssLogin}).Store(&locked, &prompt); err != nil || prompt != "/" {
			t.Fatalf("locking the collection: %v, prompt %q", err, prompt)
		}
		return
	}
	session, err := ssSession(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Call("org.gnome.keyring.InternalUnsupportedGuiltRiddenInterface.UnlockWithMasterPassword", 0,
		ssLogin, ss.NewSecret(session, "BWSYNTH-keyring")).Err; err != nil {
		t.Fatalf("unlocking the collection: %v", err)
	}
}

// TestClearStored checks context remove's removal of a credential-store entry (ginsys/bronzeward#389
// J1, Y2, Y3) against a stand-in Secret Service.
func TestClearStored(t *testing.T) {
	bound, offered, get, getHeld, del := storeBound, secretServiceOffered, keyringGetCall, keyringGetHeldCall, keyringDeleteCall
	t.Cleanup(func() {
		storeBound, secretServiceOffered, keyringGetCall, keyringGetHeldCall, keyringDeleteCall = bound, offered, get, getHeld, del
	})
	secretServiceOffered = func() (bool, error) { return true, nil }
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	var deleted atomic.Int32
	keyringDeleteCall = func(string, string) error { deleted.Add(1); return nil }
	stored := func(found bool) {
		keyringGetCall = func(string, string) (string, error) {
			if !found {
				return "", keyring.ErrNotFound
			}
			return "{}", nil
		}
		keyringGetHeldCall = keyringGetCall
	}
	remove := func() error {
		t.Helper()
		deleted.Store(0)
		note, err := ClearStored(&x, false)
		if note != "" {
			t.Errorf("a note with the store reached: %q", note)
		}
		return err
	}

	stored(true)
	if err := remove(); err != nil || deleted.Load() != 1 {
		t.Errorf("a stored entry: %v, %d deletions, want one", err, deleted.Load())
	}
	stored(false)
	if err := remove(); err != nil || deleted.Load() != 0 {
		t.Errorf("nothing stored: %v, %d deletions, want none", err, deleted.Load())
	}

	// J1: a context added again under the name owns the entry, whatever it holds.
	stored(true)
	if err := UpdateConfig(func(c *Config) error { c.Contexts = append(c.Contexts, x); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil || deleted.Load() != 0 {
		t.Errorf("a context added again: %v, %d deletions, want none", err, deleted.Load())
	}
	if err := UpdateConfig(func(c *Config) error { c.Contexts = nil; return nil }); err != nil {
		t.Fatal(err)
	}

	// Y2: a locked store fails the removal within the bound, and nothing is deleted.
	storeBound = 50 * time.Millisecond
	keyringGetCall = func(string, string) (string, error) { time.Sleep(time.Second); return "{}", nil }
	start := time.Now()
	err := remove()
	if err == nil || !strings.Contains(err.Error(), `removing the stored sign-in of context "store" from the system credential store`) ||
		time.Since(start) > 500*time.Millisecond || deleted.Load() != 0 {
		t.Errorf("a locked store: %v after %v, %d deletions; want a refusal within the bound and none", err, time.Since(start), deleted.Load())
	}
	storeBound = bound

	// Y3: a renewal lock that cannot be taken says which removal failed.
	stored(true)
	state := filepath.Join(home, "state-file")
	if err := os.WriteFile(state, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", state)
	if err := remove(); err == nil || !strings.Contains(err.Error(), `removing the stored sign-in of context "store" from the system credential store`) ||
		deleted.Load() != 0 {
		t.Errorf("no renewal lock: %v, %d deletions; want the removal named and none", err, deleted.Load())
	}
}

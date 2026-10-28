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

// TestKeyringWritesUnbounded checks that a credential-store write slower than within's bound is
// waited for: one given up on would land after its caller released the renewal lock.
func TestKeyringWritesUnbounded(t *testing.T) {
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
	s := openStore(&x)

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
	if err := held(func() error { _, err := ClearStored(&x); return err }); err == nil || !errors.Is(err, errStoreLocked) ||
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
	if _, err := ClearStored(&x); err == nil || !strings.Contains(err.Error(),
		`removing the stored sign-in of context "store" from the system credential store: BWSYNTH-no-answer`) {
		t.Errorf("context remove on a store that does not answer: %v, want the removal named", err)
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
// reaches the operator's own session bus or keyring. Skipped where either daemon is not installed.
func secretServiceBus(t *testing.T) {
	for _, b := range []string{"dbus-daemon", "gnome-keyring-daemon"} {
		if _, err := exec.LookPath(b); err != nil {
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

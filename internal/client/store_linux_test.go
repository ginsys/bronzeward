package client

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
	ss "github.com/zalando/go-keyring/secret_service"
)

// The credential-store tests that use only the seam are in store_seam_test.go. Those here need the
// Secret Service.

// standInStoreOffered makes the Secret Service count as offered on the named session bus, for the
// tests that replace the store's calls (store_seam_test.go).
func standInStoreOffered(t *testing.T) {
	offered := secretServiceOffered
	t.Cleanup(func() { secretServiceOffered = offered })
	secretServiceOffered = func() (bool, error) { return true, nil }
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

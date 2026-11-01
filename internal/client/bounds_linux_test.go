package client

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

// TestRenewalLockBounded checks that a command waiting on a renewal lock another process holds
// past lockBound fails with an error naming the lock, and changes nothing (ginsys/bronzeward#437).
func TestRenewalLockBounded(t *testing.T) {
	bound := lockBound
	t.Cleanup(func() { lockBound = bound })
	lockBound = 300 * time.Millisecond
	p := filepath.Join(t.TempDir(), "tokens")
	if err := WriteTokens(p, &Tokens{AccessToken: "BWSYNTH-access", RefreshToken: "BWSYNTH-refresh"}); err != nil {
		t.Fatal(err)
	}
	// flock(2) locks are per open file, so this one conflicts with renewalLock's as another
	// process's would.
	held, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := ClearTokens(p, nil); done <- err }()
	select {
	case err := <-done:
		want := "another bronzewardctl process holds the renewal lock " + p + ".lock"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ClearTokens under a held lock: %v; want an error containing %q", err, want)
		}
		if d := time.Since(start); d < lockBound {
			t.Errorf("ClearTokens gave up after %v, before the bound %v", d, lockBound)
		}
	case <-time.After(10 * time.Second):
		held.Close() // lets the waiting call end, so the test does not leak it
		t.Fatal("ClearTokens is still waiting on the held lock after 10 s")
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("the token file after the refusal: %v; want it kept", err)
	}
	held.Close()
	if _, err := ClearTokens(p, nil); err != nil {
		t.Errorf("ClearTokens once the lock is released: %v", err)
	}
}

// TestRenewalLockLateRelease checks that a lock a given-up wait takes after its bound is released
// at once: a later holder gets it, and no other process finds it held.
func TestRenewalLockLateRelease(t *testing.T) {
	bound, late := lockBound, lateReleased
	t.Cleanup(func() { lockBound, lateReleased = bound, late })
	released := make(chan struct{})
	lockBound, lateReleased = 100*time.Millisecond, func() { close(released) }
	l := filepath.Join(t.TempDir(), "late.lock")
	held, err := os.OpenFile(l, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := renewalLock(l); err == nil {
		t.Fatal("renewalLock under a held lock returned no error")
	}
	// The given-up flock still waits; releasing the holder lets it take the lock, then close it.
	// The probe follows that release, so a probe that took the lock first cannot hide a leak.
	held.Close()
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("the given-up wait did not take and release the lock within 10 s")
	}
	other, err := os.OpenFile(l, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the lock taken after the bound is still held: %v", err)
	}
}

// TestSecretServiceBounded checks each side of the mutating call against a stand-in Secret
// Service that never answers one call: a call before CreateItem or Item.Delete that gets no answer
// within ssBound fails with nothing sent to write or remove; CreateItem or Item.Delete itself fails
// with its outcome named unknown.
func TestSecretServiceBounded(t *testing.T) {
	stub := stubSecretService(t)
	bound := ssBound
	t.Cleanup(func() { ssBound = bound })
	ssBound = 300 * time.Millisecond
	for _, c := range []struct {
		hang, want, notSent string
		call                func() error
	}{
		{"OpenSession", "nothing was written or removed", "CreateItem", func() error { return ssSet("BWSYNTH-service", "a", "BWSYNTH-a") }},
		{"SearchItems", "nothing was written or removed", "Delete", func() error { return ssDelete("BWSYNTH-service", "a") }},
		{"CreateItem", "the sign-in may still be written after this command ends", "", func() error { return ssSet("BWSYNTH-service", "a", "BWSYNTH-a") }},
		{"Delete", "the sign-in may still be removed after this command ends", "", func() error { return ssDelete("BWSYNTH-service", "a") }},
	} {
		t.Run(c.hang, func(t *testing.T) {
			stub.reset(c.hang)
			done := make(chan error, 1)
			go func() { done <- c.call() }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("with %s unanswered: %v; want an error containing %q", c.hang, err, c.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("with %s unanswered the call is still waiting after 10 s", c.hang)
			}
			if got := stub.calls(); !slices.Contains(got, c.hang) || c.notSent != "" && slices.Contains(got, c.notSent) {
				t.Errorf("the stand-in saw %v; want %s and no %q", got, c.hang, c.notSent)
			}
		})
	}
}

// secretStub is a stand-in Secret Service with the calls ssSet and ssDelete make, one of which,
// hang, it never answers. It has no Collections property, so they use the default alias.
type secretStub struct {
	mu      sync.Mutex
	hang    string
	seen    []string
	release chan struct{}
}

func (s *secretStub) reset(hang string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hang, s.seen = hang, nil
}

func (s *secretStub) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

func (s *secretStub) answer(name string) {
	s.mu.Lock()
	s.seen = append(s.seen, name)
	hang := s.hang == name
	s.mu.Unlock()
	if hang {
		<-s.release
	}
}

type stubService struct{ *secretStub }

func (s stubService) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.answer("Unlock")
	return objects, "/", nil
}

func (s stubService) OpenSession(string, dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	s.answer("OpenSession")
	return dbus.MakeVariant(""), "/org/freedesktop/secrets/session/s1", nil
}

type stubCollection struct{ *secretStub }

func (s stubCollection) SearchItems(map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	s.answer("SearchItems")
	return []dbus.ObjectPath{stubItem}, nil
}

func (s stubCollection) CreateItem(map[string]dbus.Variant, ss.Secret, bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s.answer("CreateItem")
	return stubItem, "/", nil
}

type stubItemObject struct{ *secretStub }

func (s stubItemObject) Delete() (dbus.ObjectPath, *dbus.Error) {
	s.answer("Delete")
	return "/", nil
}

const stubItem = dbus.ObjectPath("/org/freedesktop/secrets/collection/login/1")

// stubSecretService starts a private session bus with the stand-in on it and names the bus in
// DBUS_SESSION_BUS_ADDRESS. Nothing reaches the operator's own session bus or keyring. Skipped
// where dbus-daemon is not installed.
func stubSecretService(t *testing.T) *secretStub {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon is not installed")
	}
	d := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	out, err := d.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Process.Kill(); d.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(addr))
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	s := &secretStub{release: make(chan struct{})}
	t.Cleanup(func() { close(s.release); c.Close() })
	for _, e := range []struct {
		v     any
		path  dbus.ObjectPath
		iface string
	}{{stubService{s}, ssPath, "Service"}, {stubCollection{s}, ssAlias, "Collection"}, {stubItemObject{s}, stubItem, "Item"}} {
		if err := c.Export(e.v, e.path, ssIface+e.iface); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := c.RequestName(ssName, dbus.NameFlagDoNotQueue); err != nil || r != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("taking the Secret Service name on the private bus: %v, %v", r, err)
	}
	return s
}

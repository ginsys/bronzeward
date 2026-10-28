package client

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
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
// store's unlock prompt: the read that may prompt runs before the lock, and a store locked once
// the lock is held is refused with nothing written or deleted, and the lock released.
func TestKeyringLockedRefused(t *testing.T) {
	locked, get, set, del := keyringLocked, keyringGetCall, keyringSetCall, keyringDeleteCall
	t.Cleanup(func() { keyringLocked, keyringGetCall, keyringSetCall, keyringDeleteCall = locked, get, set, del })
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
	var isLocked atomic.Bool
	var lockErr error
	var reads, heldAtRead, writes atomic.Int32
	keyringGetCall = func(string, string) (string, error) {
		reads.Add(1)
		if !free() {
			heldAtRead.Add(1)
		}
		return "", keyring.ErrNotFound
	}
	keyringLocked = func() (bool, error) { return isLocked.Load(), lockErr }
	keyringSetCall = func(string, string, string) error { writes.Add(1); return nil }
	keyringDeleteCall = func(string, string) error { writes.Add(1); return nil }
	s := openStore(&x)

	// The control: an unlocked store is written under the lock, read first outside it.
	unlock, err := s.lock()
	if err == nil {
		err = s.save(&Tokens{AccessToken: "BWSYNTH-at"})
		unlock()
	}
	if err != nil || writes.Load() != 1 || reads.Load() != 1 || heldAtRead.Load() != 0 {
		t.Fatalf("an unlocked store: %v, %d writes, %d reads, %d under the lock; want one write, one read before the lock",
			err, writes.Load(), reads.Load(), heldAtRead.Load())
	}

	isLocked.Store(true)
	writes.Store(0)
	// refused takes the lock where a refusal is wanted, releasing it if it is given.
	refused := func() error {
		unlock, err := s.lock()
		if err == nil {
			unlock()
			return errors.New("the lock was given")
		}
		if !free() {
			return errors.New("refused, but the lock is still held")
		}
		return err
	}
	if err := refused(); !errors.Is(err, errStoreLocked) {
		t.Errorf("a locked store: %v; want errStoreLocked and the lock released", err)
	}
	if _, err := s.clear(nil); !errors.Is(err, errStoreLocked) {
		t.Errorf("clear on a locked store: %v, want errStoreLocked", err)
	}
	if _, err := ClearStored(&x); err == nil || !errors.Is(err, errStoreLocked) ||
		!strings.Contains(err.Error(), `removing the stored sign-in of context "store" from the system credential store`) {
		t.Errorf("context remove on a locked store: %v, want the removal named and errStoreLocked", err)
	}
	// A store that cannot say whether it is locked is refused too.
	isLocked.Store(false)
	lockErr = errors.New("BWSYNTH-no-answer")
	if err := refused(); err == nil || err.Error() != "BWSYNTH-no-answer" {
		t.Errorf("a store with no answer: %v; want its error and the lock released", err)
	}
	if writes.Load() != 0 || heldAtRead.Load() != 0 {
		t.Errorf("%d writes or deletions on a locked store, %d reads under the lock; want none", writes.Load(), heldAtRead.Load())
	}
}

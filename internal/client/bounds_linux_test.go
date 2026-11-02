package client

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

// TestRenewalLockBounded checks that a command waiting on a renewal lock another process holds
// past lockBound fails with an error naming the lock, and keeps the stored sign-in
// (ginsys/bronzeward#437).
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
		// The message promises only that the stored sign-in is unchanged: context remove reaches it
		// after the context is removed (ClearStored).
		want := "another bronzewardctl process holds the renewal lock " + p + ".lock and did not release it within four minutes, so the stored sign-in was not changed"
		if err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("ClearTokens under a held lock: %v; want an error ending in %q", err, want)
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
	released := make(chan *os.File, 1)
	lockBound, lateReleased = 100*time.Millisecond, func(f *os.File) { released <- f }
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
	// The probe follows that release, so a probe that took the lock first cannot hide a leak, and
	// the file stays reachable until after the probe, so its finalizer cannot close it either.
	held.Close()
	select {
	case f := <-released:
		defer runtime.KeepAlive(f)
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

// TestSecretServiceDialBounded checks that a session bus whose dial gets no answer fails the
// operation within ssBound, with nothing written. The bus is a TCP listener whose accept queue is
// full, so the kernel drops each later connection request and a dial waits.
func TestSecretServiceDialBounded(t *testing.T) {
	bound := ssBound
	t.Cleanup(func() { ssBound = bound })
	ssBound = 300 * time.Millisecond
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(sa.(*syscall.SockaddrInet4).Port))
	// With a backlog of 0, one connection that nobody accepts fills the queue.
	filler, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer filler.Close()
	if probe, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		probe.Close()
		t.Skip("the kernel accepted a second connection on a full accept queue")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "tcp:host=127.0.0.1,port="+strconv.Itoa(sa.(*syscall.SockaddrInet4).Port))
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- ssSet("BWSYNTH-service", "a", "BWSYNTH-a") }()
	select {
	case err := <-done:
		if want := "nothing was written or removed"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("with a dial that gets no answer: %v; want an error containing %q", err, want)
		}
		if d := time.Since(start); d < ssBound {
			t.Errorf("ssSet gave up after %v, before the bound %v", d, ssBound)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("with a dial that gets no answer the call is still waiting after 10 s")
	}
}

// TestSecretServiceStoppedReader checks that a session bus that stops reading during CreateItem
// fails the operation within ssBound, with its outcome named unknown. A relay in front of the
// stand-in's bus stops reading from the client after 64 KiB, so the 1 MiB value fills the socket
// and the write waits. The value is kept that small because the deadline does not cut short the
// marshalling before the write: an 8 MiB value took over 10 s under -race on a loaded host.
func TestSecretServiceStoppedReader(t *testing.T) {
	stub := stubSecretService(t)
	stub.reset("")
	bound := ssBound
	t.Cleanup(func() { ssBound = bound })
	ssBound = 300 * time.Millisecond
	network, target, ok := ssTransport(os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	if !ok {
		t.Fatalf("the private bus address %q names no unix or tcp transport", os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	}
	p := filepath.Join(t.TempDir(), "bus")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var open []net.Conn
	t.Cleanup(func() {
		l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range open {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			b, err := net.Dial(network, target)
			if err != nil {
				c.Close()
				continue
			}
			mu.Lock()
			open = append(open, c, b)
			mu.Unlock()
			go io.Copy(c, b)
			go io.Copy(b, io.LimitReader(c, 64<<10))
		}
	}()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+p)
	done := make(chan error, 1)
	go func() { done <- ssSet("BWSYNTH-service", "a", "BWSYNTH-"+strings.Repeat("x", 1<<20)) }()
	select {
	case err := <-done:
		if want := "the sign-in may still be written after this command ends"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("with a bus that stopped reading: %v; want an error containing %q", err, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("with a bus that stopped reading the call is still waiting after 10 s")
	}
	if got := stub.calls(); slices.Contains(got, "CreateItem") {
		t.Errorf("the stand-in saw %v; want no CreateItem, which the relay never passed on whole", got)
	}
}

// TestSecretServiceDismissWaitsForCompleted checks that a refusal that dismissed a prompt returns
// only after the service's answer to the dismissal, its Completed signal, was read. The D-Bus
// library panics, and ends the test binary, when its connection is closed while it decodes a
// message, and gnome-keyring sends that signal about 30 ms after a dismissal that asked for no
// reply (ginsys/bronzeward#543). The stand-in holds the signal behind a gate, so a refusal that
// closes its connection at once returns before the gate opens. ssDismissWait is far longer than the
// bound after the gate, so only the signal itself, not the wait running out, can end the refusal.
func TestSecretServiceDismissWaitsForCompleted(t *testing.T) {
	stub := stubSecretService(t)
	stub.reset("")
	wait := ssDismissWait
	t.Cleanup(func() { ssDismissWait = wait })
	ssDismissWait = time.Minute
	stub.mu.Lock()
	stub.prompt, stub.gate = true, make(chan struct{})
	stub.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := ssGet("BWSYNTH-service", "a"); done <- err }()
	select {
	case <-stub.dismissed:
	case <-time.After(10 * time.Second):
		t.Fatal("the stand-in saw no dismissal within 10 s")
	}
	select {
	case err := <-done:
		t.Fatalf("the refusal returned %v before the service answered the dismissal", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(stub.gate)
	select {
	case err := <-done:
		if !errors.Is(err, errStoreLocked) {
			t.Errorf("after the Completed signal: %v, want errStoreLocked", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the refusal is still waiting 10 s after the Completed signal")
	}
}

// TestSecretServiceDismissNoAnswer checks that a service that never answers a dismissal costs the
// refusal ssDismissWait and no more.
func TestSecretServiceDismissNoAnswer(t *testing.T) {
	stub := stubSecretService(t)
	stub.reset("")
	stub.mu.Lock()
	stub.prompt = true
	stub.mu.Unlock()
	wait := ssDismissWait
	t.Cleanup(func() { ssDismissWait = wait })
	ssDismissWait = 100 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := ssGet("BWSYNTH-service", "a"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, errStoreLocked) {
			t.Errorf("with no answer to the dismissal: %v, want errStoreLocked", err)
		}
		if d := time.Since(start); d < ssDismissWait {
			t.Errorf("the refusal returned after %v, before ssDismissWait %v", d, ssDismissWait)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("with no answer to the dismissal the refusal is still waiting after 10 s")
	}
}

// secretStub is a stand-in Secret Service with the calls ssSet and ssDelete make, one of which,
// hang, it never answers. It has no Collections property, so they use the default alias.
type secretStub struct {
	mu      sync.Mutex
	hang    string
	seen    []string
	release chan struct{}
	// prompt, when set, makes Unlock answer with a prompt that Dismiss then answers, as
	// gnome-keyring does, with a Completed signal to the caller alone, once gate is closed. A nil
	// gate sends none. dismissed is closed by the first Dismiss.
	prompt    bool
	gate      chan struct{}
	dismissed chan struct{}
	conn      *dbus.Conn
}

const stubPrompt = dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1")

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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompt {
		return nil, stubPrompt, nil
	}
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

type stubPromptObject struct{ *secretStub }

func (s stubPromptObject) Dismiss(sender dbus.Sender) *dbus.Error {
	s.answer("Dismiss")
	s.mu.Lock()
	gate, dismissed := s.gate, s.dismissed
	s.mu.Unlock()
	close(dismissed)
	if gate == nil {
		return nil
	}
	select {
	case <-gate:
	case <-s.release:
		return nil
	}
	err := s.conn.Send(&dbus.Message{Type: dbus.TypeSignal, Headers: map[dbus.HeaderField]dbus.Variant{
		dbus.FieldPath:        dbus.MakeVariant(stubPrompt),
		dbus.FieldInterface:   dbus.MakeVariant(ssIface + "Prompt"),
		dbus.FieldMember:      dbus.MakeVariant("Completed"),
		dbus.FieldDestination: dbus.MakeVariant(string(sender)),
		dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(true, dbus.MakeVariant(""))),
	}, Body: []any{true, dbus.MakeVariant("")}}, nil)
	if err.Err != nil {
		return dbus.MakeFailedError(err.Err)
	}
	return nil
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
	s := &secretStub{release: make(chan struct{}), dismissed: make(chan struct{}), conn: c}
	t.Cleanup(func() { close(s.release); c.Close() })
	for _, e := range []struct {
		v     any
		path  dbus.ObjectPath
		iface string
	}{{stubService{s}, ssPath, "Service"}, {stubCollection{s}, ssAlias, "Collection"}, {stubItemObject{s}, stubItem, "Item"}, {stubPromptObject{s}, stubPrompt, "Prompt"}} {
		if err := c.Export(e.v, e.path, ssIface+e.iface); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := c.RequestName(ssName, dbus.NameFlagDoNotQueue); err != nil || r != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("taking the Secret Service name on the private bus: %v, %v", r, err)
	}
	return s
}

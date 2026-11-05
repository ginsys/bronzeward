package client

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// These tests replace the security tool with securityRun's stand-in, so none reaches a Keychain.
// The real one is tested in store_darwin_keychain_test.go (ginsys/bronzeward#628).

// standInStoreOffered is the Linux tests' stand-in for an offered Secret Service. The Keychain needs
// none: the security tool is there on every Mac, and the tests that call this replace its calls.
func standInStoreOffered(*testing.T) {}

// toolCall is one run of the security tool.
type toolCall struct {
	stdin string
	args  []string
}

// fakeSecurity replaces securityRun with answer, which gets the call and the deadline's context. It
// returns the calls made, in order.
func fakeSecurity(t *testing.T, answer func(ctx context.Context, c toolCall) ([]byte, error)) func() []toolCall {
	t.Helper()
	run := securityRun
	t.Cleanup(func() { securityRun = run })
	var mu sync.Mutex
	var calls []toolCall
	securityRun = func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
		c := toolCall{stdin, args}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		return answer(ctx, c)
	}
	return func() []toolCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]toolCall(nil), calls...)
	}
}

// unlocked answers every call as the tool does for an unlocked keychain with no item.
func unlocked(ctx context.Context, c toolCall) ([]byte, error) {
	if c.args[0] == "find-generic-password" || c.args[0] == "delete-generic-password" {
		return []byte("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.\n"), errors.New("exit status 44")
	}
	return []byte("Keychain \"login.keychain-db\" lock-on-sleep timeout=300s\n"), nil
}

// hang answers after the call's deadline, as the real runner does: with context.DeadlineExceeded.
func hang(ctx context.Context, _ toolCall) ([]byte, error) {
	<-ctx.Done()
	return nil, context.DeadlineExceeded
}

// TestSecurityErrors checks how the tool's failures map to the store's errors (client-contracts.md
// section 9.2 item 3): a missing item, a keychain that needs the operator, a slow tool, any other.
func TestSecurityErrors(t *testing.T) {
	other := errors.New("exit status 1")
	for _, c := range []struct {
		name string
		out  string
		err  error
		want func(error) bool
	}{
		{"no failure", "", nil, func(e error) bool { return e == nil }},
		{"not found", "The specified item could not be found in the keychain.", other, func(e error) bool { return errors.Is(e, keyring.ErrNotFound) }},
		{"no interaction", "User interaction is not allowed.", other, func(e error) bool { return errors.Is(e, errStoreLocked) }},
		{"slow", "", context.DeadlineExceeded, func(e error) bool { return errors.Is(e, errStoreSlow) }},
		{"other", "BWSYNTH-tool said\nFORGED", other, func(e error) bool {
			return e != nil && !errors.Is(e, keyring.ErrNotFound) && !errors.Is(e, errStoreLocked) && !errors.Is(e, errStoreSlow) &&
				!strings.Contains(e.Error(), "\n") && strings.Contains(e.Error(), `BWSYNTH-tool said\x0aFORGED`)
		}},
	} {
		if err := securityFailed([]byte(c.out), c.err); !c.want(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// TestSecurityUnavailable checks that a missing security tool means memory only.
func TestSecurityUnavailable(t *testing.T) {
	tool := securityTool
	t.Cleanup(func() { securityTool = tool })
	securityTool = filepath.Join(t.TempDir(), "missing")
	if why := keyringUnavailable(); !strings.Contains(why, "security tool") || !strings.Contains(why, "not installed") {
		t.Errorf("a missing tool: %q, want it named", why)
	}
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	if s := openStore(&x, false); s.none == "" || !strings.Contains(s.kept(), "this process's memory only") {
		t.Errorf("with no tool the sign-in is kept in %q, want memory only", s.kept())
	}
}

// TestSecurityWriteKeepsSecretOutOfArguments checks item 4: the secret reaches the tool on its
// standard input, in go-keyring's format, and in no argument.
func TestSecurityWriteKeepsSecretOutOfArguments(t *testing.T) {
	calls := fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) { return nil, nil })
	const secret = `{"accessToken":"BWSYNTH-secret'quote"}`
	if err := keyringSet("BWSYNTH-context", secret); err != nil {
		t.Fatal(err)
	}
	var wrote bool
	for _, c := range calls() {
		for _, a := range c.args {
			if strings.Contains(a, "BWSYNTH") && a != "BWSYNTH-context" || strings.Contains(a, base64.StdEncoding.EncodeToString([]byte(secret))) {
				t.Errorf("the secret is in the argument %q of %v", a, c.args)
			}
		}
		if len(c.args) == 1 && c.args[0] == "-i" {
			wrote = true
			want := prefixBase64 + base64.StdEncoding.EncodeToString([]byte(secret))
			if !strings.HasPrefix(c.stdin, "add-generic-password -U -s 'bronzewardctl' -a 'BWSYNTH-context' -w '") ||
				!strings.Contains(c.stdin, want) || !strings.HasSuffix(c.stdin, "\n") {
				t.Errorf("the command on standard input: %q, want go-keyring's format", c.stdin)
			}
		}
	}
	if !wrote {
		t.Error("no security -i call was made")
	}
}

// TestSecurityWriteReportedOnOutput checks that a security -i command that failed, with the tool
// exiting 0 as it can, is no success.
func TestSecurityWriteReportedOnOutput(t *testing.T) {
	fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) {
		if c.args[0] == "-i" {
			return []byte("security: SecKeychainItemCreateFromContent (BWSYNTH): The keychain is full.\n"), nil
		}
		return nil, nil
	})
	err := keyringSet("BWSYNTH-context", "{}")
	if err == nil || !strings.Contains(err.Error(), "The keychain is full.") {
		t.Errorf("a write the tool reported as failed: %v, want it shown", err)
	}
}

// TestSecurityEntryTooBig checks that an entry over the tool's line limit is refused before the
// tool runs, with the limit named, and that one just under it is written.
func TestSecurityEntryTooBig(t *testing.T) {
	calls := fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) { return nil, nil })
	fits := strings.Repeat("a", 2900)
	if err := keyringSet("BWSYNTH-context", fits); err != nil {
		t.Errorf("a 2900-byte entry: %v, want it written", err)
	}
	n := len(calls())
	err := keyringSet("BWSYNTH-context", strings.Repeat("a", 3100))
	if !errors.Is(err, errEntryTooBig) || !strings.Contains(err.Error(), "4096") {
		t.Errorf("a 3100-byte entry: %v, want errEntryTooBig naming the limit", err)
	}
	if len(calls()) != n {
		t.Errorf("the tool ran %d more times for an entry it cannot take; want none", len(calls())-n)
	}
	if err := keyringSet("BWSYNTH-con\ntext", "{}"); err == nil || len(calls()) != n {
		t.Errorf("a name with a line feed: %v after %d more calls; want it refused before the tool", err, len(calls())-n)
	}
}

// TestSecurityReadsGoKeyringFormats checks that the entries go-keyring wrote are read: base64, the
// older hex form, and a plain value.
func TestSecurityReadsGoKeyringFormats(t *testing.T) {
	for name, out := range map[string]string{
		"base64": prefixBase64 + base64.StdEncoding.EncodeToString([]byte("BWSYNTH-v")) + "\n",
		"hex":    prefixHex + hex.EncodeToString([]byte("BWSYNTH-v")) + "\n",
		"plain":  "BWSYNTH-v\n",
	} {
		calls := fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) {
			if c.args[0] == "find-generic-password" {
				return []byte(out), nil
			}
			return nil, nil
		})
		v, found, err := keyringGet("BWSYNTH-context")
		if err != nil || !found || v != "BWSYNTH-v" {
			t.Errorf("%s: %q, %v, %v; want BWSYNTH-v", name, v, found, err)
		}
		if c := calls()[0]; strings.Join(c.args, " ") != "find-generic-password -s bronzewardctl -a BWSYNTH-context -w" {
			t.Errorf("%s: the read ran %v", name, c.args)
		}
	}
}

// TestSecurityNotFound checks that a missing item reads as none and that removing a missing item
// is no failure, as on Linux.
func TestSecurityNotFound(t *testing.T) {
	fakeSecurity(t, unlocked)
	if v, found, err := keyringGet("BWSYNTH-context"); err != nil || found || v != "" {
		t.Errorf("a missing item: %q, %v, %v; want none and no error", v, found, err)
	}
	if v, found, err := keyringGetHeld("BWSYNTH-context"); err != nil || found || v != "" {
		t.Errorf("a missing item under the lock: %q, %v, %v; want none and no error", v, found, err)
	}
	if err := keyringDelete("BWSYNTH-context"); err != nil {
		t.Errorf("removing a missing item: %v, want none", err)
	}
}

// TestSecurityLockedRefusedWithoutPrompt checks that under the renewal lock, and for a write, a
// locked keychain is asked about first and refused, so no call that could show a password dialog
// runs (item 3).
func TestSecurityLockedRefusedWithoutPrompt(t *testing.T) {
	calls := fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) {
		if c.args[0] == "show-keychain-info" {
			return []byte("User interaction is not allowed.\n"), errors.New("exit status 36")
		}
		return nil, nil
	})
	if _, _, err := keyringGetHeld("BWSYNTH-context"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a held read on a locked keychain: %v, want errStoreLocked", err)
	}
	if err := keyringSet("BWSYNTH-context", "{}"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a write on a locked keychain: %v, want errStoreLocked", err)
	}
	if err := keyringDelete("BWSYNTH-context"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a removal on a locked keychain: %v, want errStoreLocked", err)
	}
	for _, c := range calls() {
		if c.args[0] != "show-keychain-info" {
			t.Errorf("%v ran on a locked keychain; want only the question about it", c.args)
		}
	}
}

// TestSecurityDeadlines checks the tool is killed at its deadline: a question that gets no answer
// under the lock means a locked keychain, a write that gets none says it may still be carried out,
// and a read says the store is slow. The runner returns at the deadline, not after the tool.
func TestSecurityDeadlines(t *testing.T) {
	bound, held := securityBound, securityHeldBound
	t.Cleanup(func() { securityBound, securityHeldBound = bound, held })
	securityBound, securityHeldBound = 100*time.Millisecond, 100*time.Millisecond

	fakeSecurity(t, hang)
	start := time.Now()
	if _, _, err := keyringGetHeld("BWSYNTH-context"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a held read with no answer: %v, want errStoreLocked", err)
	}
	if err := keyringSet("BWSYNTH-context", "{}"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a write whose question gets no answer: %v, want errStoreLocked", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the calls took %v, want them stopped at their deadline", time.Since(start))
	}

	// The keychain answers the question, then the write hangs.
	fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) {
		if c.args[0] == "show-keychain-info" {
			return nil, nil
		}
		return hang(ctx, c)
	})
	if err := keyringSet("BWSYNTH-context", "{}"); err == nil || !strings.Contains(err.Error(), "may still be written") {
		t.Errorf("a write with no answer: %v, want it to say it may still be written", err)
	}
	if err := keyringDelete("BWSYNTH-context"); err == nil || !strings.Contains(err.Error(), "may still be removed") {
		t.Errorf("a removal with no answer: %v, want it to say it may still be removed", err)
	}
	if _, _, err := keyringGet("BWSYNTH-context"); !errors.Is(err, errStoreSlow) {
		t.Errorf("a read with no answer: %v, want errStoreSlow", err)
	}
}

// fakeTool makes securityTool a shell script with the given body, so that the real runner starts a
// real process.
func fakeTool(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := securityTool
	t.Cleanup(func() { securityTool = tool })
	securityTool = path
}

// TestSecurityRefusesNamesItWouldMisread checks that a context name the security -i parser reads as
// syntax is refused before the tool runs, by a read and a write alike, that a removal of it succeeds
// since nothing was kept, and that other punctuation is kept.
func TestSecurityRefusesNamesItWouldMisread(t *testing.T) {
	calls := fakeSecurity(t, unlocked)
	for _, name := range []string{`team\prod`, "it's", `say"hi"`, "tab\tname", "line\nfeed", "nul\x00name"} {
		if _, _, err := keyringGet(name); err == nil || !strings.Contains(err.Error(), "Remove the context and add it again") {
			t.Errorf("a read of %q: %v, want the refusal with the way out", name, err)
		}
		if err := keyringSet(name, "{}"); err == nil || !strings.Contains(err.Error(), "Remove the context and add it again") {
			t.Errorf("a write of %q: %v, want the refusal with the way out", name, err)
		}
		if err := keyringDelete(name); err != nil {
			t.Errorf("a removal of %q: %v, want none", name, err)
		}
	}
	if n := len(calls()); n != 0 {
		t.Errorf("the tool ran %d times for names it cannot take; want none", n)
	}
	for _, name := range []string{"team-prod", "prod.example:8443", "ünï"} {
		if err := keyringSet(name, "{}"); err != nil {
			t.Errorf("a write of %q: %v, want it kept", name, err)
		}
	}
}

// TestSecurityEntryThatDoesNotDecode checks that an entry with a go-keyring prefix that does not
// decode is refused without a byte of it in the error.
func TestSecurityEntryThatDoesNotDecode(t *testing.T) {
	for _, out := range []string{prefixHex + "zzBWSYNTH", prefixBase64 + "!!!BWSYNTH"} {
		fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) {
			if c.args[0] == "find-generic-password" {
				return []byte(out + "\n"), nil
			}
			return nil, nil
		})
		_, _, err := keyringGet("BWSYNTH-context")
		if !errors.Is(err, errEntryUnreadable) || strings.Contains(err.Error(), "BWSYNTH") || strings.Contains(err.Error(), "U+") {
			t.Errorf("%q: %v, want errEntryUnreadable with none of the entry", out, err)
		}
	}
}

// TestSecurityErrorNeverCarriesStandardOutput checks, with the real runner, that a tool which prints
// the secret and then fails shows its standard error only.
func TestSecurityErrorNeverCarriesStandardOutput(t *testing.T) {
	fakeTool(t, "echo BWSYNTH-secret-on-stdout\necho 'tool broke' >&2\nexit 1\n")
	_, err := securityFind(context.Background(), keyringService, "BWSYNTH-context")
	if err == nil || strings.Contains(err.Error(), "BWSYNTH-secret-on-stdout") || !strings.Contains(err.Error(), "tool broke") {
		t.Errorf("a failed read: %v, want the standard error and none of the standard output", err)
	}
	fakeTool(t, "echo BWSYNTH-secret-on-stdout\n")
	if v, err := securityFind(context.Background(), keyringService, "BWSYNTH-context"); err != nil || v != "BWSYNTH-secret-on-stdout" {
		t.Errorf("a read that worked: %q, %v, want the standard output", v, err)
	}
}

// TestSecurityStopsTheToolAtTheDeadline checks, with the real runner and a real process, that the
// tool is killed and reaped when the deadline passes, and that the call returns by then: a runner
// that did not kill it, or did not wait, fails here.
func TestSecurityStopsTheToolAtTheDeadline(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeTool(t, "echo $$ > '"+pidFile+"'\nexec sleep 60\n")
	bound := securityBound
	t.Cleanup(func() { securityBound = bound })
	securityBound = time.Second
	start := time.Now()
	if _, err := securityGet(keyringService, "BWSYNTH-context"); !errors.Is(err, errStoreSlow) {
		t.Errorf("a read with no answer: %v, want errStoreSlow", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("the call took %v, want it stopped at its deadline", time.Since(start))
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the tool never started: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the tool (pid %d) is still there after the call: %v", pid, err)
	}
}

// TestSecurityPreflightHasItsOwnBound checks that a write and a removal refuse a keychain that gives
// no answer to the question within securityHeldBound, not after securityBound.
func TestSecurityPreflightHasItsOwnBound(t *testing.T) {
	bound, held := securityBound, securityHeldBound
	t.Cleanup(func() { securityBound, securityHeldBound = bound, held })
	securityBound, securityHeldBound = time.Minute, 100*time.Millisecond
	fakeSecurity(t, hang)
	start := time.Now()
	if err := keyringSet("BWSYNTH-context", "{}"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a write whose question gets no answer: %v, want errStoreLocked", err)
	}
	if err := keyringDelete("BWSYNTH-context"); !errors.Is(err, errStoreLocked) {
		t.Errorf("a removal whose question gets no answer: %v, want errStoreLocked", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the calls took %v, want the question stopped at securityHeldBound", time.Since(start))
	}
}

// TestSignInRefusedWhenKeychainFailsAfterAnswering checks the path of a store that answers the
// availability test and then fails: the sign-in is refused before the browser opens, naming the
// locked keychain (login.go reads the store before the sign-in).
func TestSignInRefusedWhenKeychainFailsAfterAnswering(t *testing.T) {
	calls := fakeSecurity(t, func(ctx context.Context, c toolCall) ([]byte, error) {
		return []byte("User interaction is not allowed.\n"), errors.New("exit status 36")
	})
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	if _, err := openSignIn(&x, true, io.Discard); err == nil || !strings.Contains(err.Error(), "the system credential store is locked") {
		t.Errorf("a command on a locked keychain: %v, want it refused, naming the locked store", err)
	}
	if n := len(calls()); n == 0 {
		t.Error("the keychain was never asked")
	}
}

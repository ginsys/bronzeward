package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// The macOS Keychain store (ginsys/bronzeward#628; decision T3 of #254). It runs the system's
// security tool, as go-keyring's darwin provider does, and keeps go-keyring's entry format: the
// service keyringService, the account of the context's name and the secret in base64 behind the
// prefix go-keyring-base64:, so that each reads what the other wrote. go-keyring's own calls are
// not used because they have no deadline and leave their child process open on a refused size.
//
// The secret goes to the tool on its standard input (security -i), never in its arguments
// (client-contracts.md section 9.2 item 4).

// securityTool is the system's security tool, where go-keyring looks for it too. Replaced in tests.
var securityTool = "/usr/bin/security"

// securityLine is the longest command line security -i takes.
const securityLine = 4096

// The prefixes go-keyring puts before the secret it stores. Set writes the first; the second is an
// older form that Get still reads.
const (
	prefixBase64 = "go-keyring-base64:"
	prefixHex    = "go-keyring-encoded:"
)

var (
	// securityBound is how long one security call waits for its answer.
	securityBound = time.Minute
	// securityHeldBound is how long a call under the renewal lock waits: a locked keychain shows a
	// password dialog the call would wait on, so a timeout there is read as a locked keychain.
	securityHeldBound = 15 * time.Second
)

// errStoreSlow: the security tool did not answer within securityBound, before any write was sent.
var errStoreSlow = errors.New("the system credential store did not answer within a minute; nothing was written or removed")

// errEntryTooBig: the sign-in does not fit one security -i command.
var errEntryTooBig = fmt.Errorf("the sign-in is larger than the macOS security tool takes in one command (%d bytes, about 2.9 KB of sign-in); nothing was written", securityLine)

// keyringUnavailable is why the Keychain is not used, "" when it is: the security tool is missing.
func keyringUnavailable() string {
	if fi, err := os.Stat(securityTool); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
		return "the macOS security tool (" + securityTool + ") is not installed"
	}
	return ""
}

// unreached says that a sign-in another session kept in the Keychain is not reached.
func unreached() string {
	return " A sign-in another session kept in the system credential store is not reached, so it is neither removed nor revoked."
}

// The Keychain's calls, replaced in tests: the read that may wait on a password dialog, and the
// calls made under the renewal lock, which never wait on one.
var (
	keyringGetCall                                        = securityGet
	keyringGetHeldCall, keyringSetCall, keyringDeleteCall = securityGetHeld, securitySet, securityDelete
)

func keyringGet(user string) (string, bool, error) { return bounded(keyringGetCall, user) }

func keyringGetHeld(user string) (string, bool, error) { return bounded(keyringGetHeldCall, user) }

func bounded(get func(string, string) (string, error), user string) (string, bool, error) {
	v, err := within(func() (*string, error) {
		v, err := get(keyringService, user)
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, nil
		}
		return &v, err
	})
	if err != nil || v == nil {
		return "", false, err
	}
	return *v, true, nil
}

func keyringSet(user, v string) error { return keyringSetCall(keyringService, user, v) }

func keyringDelete(user string) error {
	if err := keyringDeleteCall(keyringService, user); !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// securityRun runs the security tool with stdin and returns its output. At the deadline it kills
// the tool and returns context.DeadlineExceeded. Replaced in tests.
var securityRun = func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, securityTool, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), context.DeadlineExceeded
	}
	return out.Bytes(), err
}

// securityFailed maps a failed call to the store's errors: the tool's own words decide a missing
// item and a keychain that cannot be used without the operator.
func securityFailed(out []byte, err error) error {
	text := strings.TrimSpace(string(out))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return errStoreSlow
	case strings.Contains(text, "could not be found"):
		return keyring.ErrNotFound
	case strings.Contains(text, "User interaction is not allowed"):
		return errStoreLocked
	}
	return fmt.Errorf("the macOS security tool failed: %v: %s", err, printable(text))
}

// securityUnlocked refuses a locked default keychain without showing its password dialog, by
// asking the tool about the keychain before the call that would prompt (client-contracts.md
// section 9.2 item 3). A call that gets no answer is read as a locked keychain.
func securityUnlocked(ctx context.Context) error {
	out, err := securityRun(ctx, "", "show-keychain-info")
	if err = securityFailed(out, err); errors.Is(err, errStoreSlow) {
		return errStoreLocked
	} else if errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("the macOS security tool found no default keychain: %s", printable(strings.TrimSpace(string(out))))
	}
	return err
}

// securityGet reads through the tool with the password dialog allowed, bounded by securityBound.
func securityGet(service, user string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), securityBound)
	defer cancel()
	return securityFind(ctx, service, user)
}

// securityGetHeld is securityGet under the renewal lock: a locked keychain is refused.
func securityGetHeld(service, user string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), securityHeldBound)
	defer cancel()
	if err := securityUnlocked(ctx); err != nil {
		return "", err
	}
	v, err := securityFind(ctx, service, user)
	if errors.Is(err, errStoreSlow) {
		return "", errStoreLocked
	}
	return v, err
}

func securityFind(ctx context.Context, service, user string) (string, error) {
	out, err := securityRun(ctx, "", "find-generic-password", "-s", service, "-a", user, "-w")
	if err = securityFailed(out, err); err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(out))
	switch {
	case strings.HasPrefix(text, prefixBase64):
		b, err := base64.StdEncoding.DecodeString(text[len(prefixBase64):])
		return string(b), err
	case strings.HasPrefix(text, prefixHex):
		b, err := hex.DecodeString(text[len(prefixHex):])
		return string(b), err
	}
	return text, nil
}

// securityQuote quotes one word of a security -i command line, as go-keyring does.
func securityQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// securityCommand is the add-generic-password line for security -i, or errEntryTooBig.
func securityCommand(service, user, secret string) (string, error) {
	if strings.ContainsAny(user, "\r\n\x00") {
		return "", errors.New("the context name holds a control character the macOS security tool cannot take")
	}
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", securityQuote(service), securityQuote(user),
		securityQuote(prefixBase64+base64.StdEncoding.EncodeToString([]byte(secret))))
	if len(line) > securityLine {
		return "", errEntryTooBig
	}
	return line, nil
}

func securitySet(service, user, secret string) error {
	line, err := securityCommand(service, user, secret)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), securityBound)
	defer cancel()
	if err := securityUnlocked(ctx); err != nil {
		return err
	}
	out, err := securityRun(ctx, line, "-i")
	if err == nil && bytes.Contains(out, []byte("security:")) {
		// security -i reports a failed command on its output and may still exit 0.
		err = errors.New("the tool reported an error")
	}
	return unsettled(securityFailed(out, err), "written")
}

func securityDelete(service, user string) error {
	ctx, cancel := context.WithTimeout(context.Background(), securityBound)
	defer cancel()
	if err := securityUnlocked(ctx); err != nil {
		return err
	}
	out, err := securityRun(ctx, "", "delete-generic-password", "-s", service, "-a", user)
	return unsettled(securityFailed(out, err), "removed")
}

// unsettled is the error of a write that got no answer within securityBound: the tool may still
// have carried it out when it was killed.
func unsettled(err error, what string) error {
	if errors.Is(err, errStoreSlow) {
		return fmt.Errorf("the system credential store did not answer within a minute; the sign-in may still be %s after this command ends", what)
	}
	return err
}

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
	"path/filepath"
	"strings"
	"time"
	"unicode"

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
	// securityBound is how long one security call waits for its answer. It ends before within's
	// bound (storeBound) does, so the tool is killed and reaped before a read returns.
	securityBound = storeBound - 5*time.Second
	// securityHeldBound is how long a call under the renewal lock waits: a locked keychain shows a
	// password dialog the call would wait on, so a timeout there is read as a locked keychain.
	securityHeldBound = 15 * time.Second
)

// errStoreSlow: the security tool did not answer within securityBound, before any write was sent.
var errStoreSlow = errors.New("the system credential store did not answer within a minute; nothing was written or removed")

// kindError is an error with its own text that errors.Is also matches to its kind (store.go).
type kindError struct {
	error
	kind error
}

func (r kindError) Is(target error) bool { return target == r.kind }

func (r kindError) Unwrap() error { return r.error }

// errEntryUnreadableText is the error for a stored entry that carries a go-keyring prefix and does
// not decode. It matches errEntryUnreadable (store.go), so login and logout replace or remove it.
var errEntryUnreadableText = kindError{errors.New("the sign-in kept in the macOS Keychain is not in a form this program reads; run login again"), errEntryUnreadable}

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
	if err := nameRefused(user); err != nil {
		return "", false, err
	}
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
	if nameRefused(user) != nil {
		return nil // no sign-in was ever kept under such a name
	}
	if err := keyringDeleteCall(keyringService, user); !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

// securityRun runs the security tool with stdin and returns its output. At the deadline it kills
// the tool and returns context.DeadlineExceeded; a tool that exited 0 is no failure, whatever the
// clock says. A run that failed returns the tool's standard error only: a read prints the secret on
// standard output, and no error text may carry it. A run that succeeded returns both streams. The
// tool writes its errors to standard error, where securityFailed looks for them. Replaced in tests.
var securityRun = func(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, securityTool, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return securityOutcome(ctx, cmd.ProcessState, out.Bytes(), errOut.Bytes(), err)
}

// securityOutcome is the result of one run, given the state of the tool's process. The exit status
// of the tool decides: Run returns the context's error even for a tool that exited 0, when the
// deadline came while the tool was being reaped, and a write that was carried out is no timeout.
func securityOutcome(ctx context.Context, state *os.ProcessState, out, errOut []byte, err error) ([]byte, error) {
	switch {
	case state != nil && state.Success():
		return append(out, errOut...), nil
	case ctx.Err() != nil:
		return errOut, context.DeadlineExceeded
	}
	return errOut, err
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

// securityKeychain is the path of the user's default keychain. Every call that reads or changes
// an entry names this keychain, and the question about a locked keychain asks about it: the
// keychain asked about is the one the call touches, so no other keychain of the search list can
// show a password dialog under the renewal lock (client-contracts.md section 9.2 item 3).
func securityKeychain(ctx context.Context) (string, error) {
	out, err := securityRun(ctx, "", "default-keychain", "-d", "user")
	if err = securityFailed(out, err); errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("the macOS security tool found no default keychain: %s", printable(strings.TrimSpace(string(out))))
	} else if err != nil {
		return "", err
	}
	// The tool prints the path in double quotes. Only the line's own whitespace and that one pair of
	// quotes go: the path keeps every byte, a space at its end included. The path goes into a
	// security -i line, so the characters that the tool reads as syntax are refused, as in a name.
	path := strings.TrimSpace(string(out))
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		path = path[1 : len(path)-1]
	}
	if !filepath.IsAbs(path) || strings.ContainsAny(path, `\'"`) || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("the macOS security tool gave a default keychain this program cannot name: %s", printable(path))
	}
	return path, nil
}

// securityUnlocked refuses a locked keychain without showing its password dialog, by asking the
// tool about that keychain before the call that would prompt. A call that gets no answer is read
// as a locked keychain.
func securityUnlocked(ctx context.Context, keychain string) error {
	out, err := securityRun(ctx, "", "show-keychain-info", keychain)
	if err = securityFailed(out, err); errors.Is(err, errStoreSlow) {
		return errStoreLocked
	} else if errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("the macOS security tool found no keychain %s: %s", printable(keychain), printable(strings.TrimSpace(string(out))))
	}
	return err
}

// securityReady is the question about the default keychain: its path and a refusal when it is locked
// or gives no answer. It has the deadline of ctx.
func securityReady(ctx context.Context) (string, error) {
	keychain, err := securityKeychain(ctx)
	if errors.Is(err, errStoreSlow) {
		return "", errStoreLocked
	} else if err != nil {
		return "", err
	}
	return keychain, securityUnlocked(ctx, keychain)
}

// securityPreflight is securityReady on a deadline of its own, securityHeldBound, inside ctx: a write
// or a removal refuses a keychain that gives no answer after 15 seconds, not after securityBound.
// The call that follows has what is left of ctx, so both together end within securityBound.
func securityPreflight(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, securityHeldBound)
	defer cancel()
	return securityReady(ctx)
}

// securityGet reads through the tool with the password dialog allowed, bounded by securityBound.
func securityGet(service, user string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), securityBound)
	defer cancel()
	keychain, err := securityKeychain(ctx)
	if err != nil {
		return "", err
	}
	return securityFind(ctx, keychain, service, user)
}

// securityGetHeld is securityGet under the renewal lock: a locked keychain is refused.
func securityGetHeld(service, user string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), securityHeldBound)
	defer cancel()
	keychain, err := securityReady(ctx)
	if err != nil {
		return "", err
	}
	v, err := securityFind(ctx, keychain, service, user)
	if errors.Is(err, errStoreSlow) {
		return "", errStoreLocked
	}
	return v, err
}

func securityFind(ctx context.Context, keychain, service, user string) (string, error) {
	out, err := securityRun(ctx, "", "find-generic-password", "-s", service, "-a", user, "-w", keychain)
	if err = securityFailed(out, err); err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(out))
	// A decoding error is replaced by a fixed text: the library's own errors name bytes of the input,
	// which is the secret.
	switch {
	case strings.HasPrefix(text, prefixBase64):
		b, err := base64.StdEncoding.DecodeString(text[len(prefixBase64):])
		if err != nil {
			return "", errEntryUnreadableText
		}
		return string(b), nil
	case strings.HasPrefix(text, prefixHex):
		b, err := hex.DecodeString(text[len(prefixHex):])
		if err != nil {
			return "", errEntryUnreadableText
		}
		return string(b), nil
	}
	return text, nil
}

// securityQuote quotes one word of a security -i command line, as go-keyring does.
func securityQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// nameRefused is the refusal of a context name the security -i parser would read as syntax: a
// backslash, a single or double quote, or a control character. config.go accepts such a name; the
// Keychain store does not keep a sign-in under it (client-contracts.md section 9.2 item 3). The
// read refuses too, so that login stops before its browser opens and not after.
func nameRefused(user string) error {
	if strings.ContainsAny(user, `\'"`) || strings.IndexFunc(user, unicode.IsControl) >= 0 {
		return kindError{fmt.Errorf("the macOS Keychain store cannot keep the sign-in of the context \"%s\": its name holds a backslash, a quote or a control character, which the security tool reads as syntax. Remove the context and add it again under a name without them", quoted(user)), errNameRefused}
	}
	return nil
}

// securityCommand is the add-generic-password line for security -i, naming the keychain (none when
// keychain is empty), or errEntryTooBig.
func securityCommand(service, user, secret, keychain string) (string, error) {
	if err := nameRefused(user); err != nil {
		return "", err
	}
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s", securityQuote(service), securityQuote(user),
		securityQuote(prefixBase64+base64.StdEncoding.EncodeToString([]byte(secret))))
	if keychain != "" {
		line += " " + securityQuote(keychain)
	}
	line += "\n"
	if len(line) > securityLine {
		return "", errEntryTooBig
	}
	return line, nil
}

func securitySet(service, user, secret string) error {
	// A name or an entry the tool cannot take is refused before the tool runs at all.
	if _, err := securityCommand(service, user, secret, ""); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), securityBound)
	defer cancel()
	keychain, err := securityPreflight(ctx)
	if err != nil {
		return err
	}
	line, err := securityCommand(service, user, secret, keychain)
	if err != nil {
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
	keychain, err := securityPreflight(ctx)
	if err != nil {
		return err
	}
	out, err := securityRun(ctx, "", "delete-generic-password", "-s", service, "-a", user, keychain)
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

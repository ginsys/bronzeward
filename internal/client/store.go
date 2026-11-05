package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// store is where an OIDC context's sign-in is kept (client-contracts.md §9.2 item 3): its D2 (b)
// token file when it has one, else the operating system's credential store, else (D2 (a)) only
// this process's memory, for the reason in none. With noPrompt (--no-input) the credential store
// is never asked to show its unlock prompt: a locked one is refused (errStoreLocked).
type store struct {
	x        *Context
	file     string
	none     string
	noPrompt bool
}

func openStore(x *Context, noPrompt bool) *store {
	s := &store{x: x, file: x.TokenFile(), noPrompt: noPrompt}
	if s.file == "" {
		s.none = keyringUnavailable()
	}
	return s
}

// kept says where the sign-in is kept.
func (s *store) kept() string {
	switch {
	case s.file != "":
		return "the token file " + printable(s.file)
	case s.none == "":
		return "the system credential store"
	}
	return "this process's memory only, since " + printable(s.none)
}

// entry is a credential-store entry. It names the server and issuer it was signed in for, so a
// context removed and added again under the same name for another server never sends it there.
type entry struct {
	Server string `json:"server"`
	Issuer string `json:"issuer"`
	Tokens
}

// load reads the stored sign-in, nil when there is none. A credential store that fails is
// treated as none: the sign-in then lasts for this process only.
func (s *store) load() (*Tokens, error) {
	if s.file != "" || s.none != "" {
		return s.read()
	}
	t, err := s.read()
	if err != nil && !errors.Is(err, ErrNotTokenFile) {
		s.none = "the credential store failed: " + err.Error()
		return nil, nil
	}
	return t, err
}

// read is load without its fallback: a failing credential store is an error. An entry holding a
// refresh token but no access token is returned with ErrNotTokenFile, as ReadTokens returns such a
// document, so that logout still removes it and revokes its refresh token. Unless noPrompt, it
// may wait, at most a minute, on the credential store's unlock prompt, so it is never used under
// the renewal lock.
func (s *store) read() (*Tokens, error) {
	if s.noPrompt {
		return s.readHeld()
	}
	return s.get(keyringGet)
}

// readHeld is read under the renewal lock: a locked credential store is refused (errStoreLocked).
func (s *store) readHeld() (*Tokens, error) { return s.get(keyringGetHeld) }

func (s *store) get(fromKeyring func(string) (string, bool, error)) (*Tokens, error) {
	switch {
	case s.file != "":
		return ReadTokens(s.file)
	case s.none != "":
		return nil, nil
	}
	v, found, err := fromKeyring(s.x.Name)
	if err != nil {
		return nil, err
	}
	var e entry
	if !found || json.Unmarshal([]byte(v), &e) != nil || e.AccessToken == "" && e.RefreshToken == "" ||
		e.Server != s.x.Server || e.Issuer != s.x.OIDC.Issuer {
		return nil, nil
	}
	if e.AccessToken == "" {
		return &e.Tokens, fmt.Errorf("the sign-in stored in the system credential store: %w", ErrNotTokenFile)
	}
	return &e.Tokens, nil
}

// lock takes the sign-in's renewal lock (§9.2 item 3). A token file's lies beside it, shared by
// every context that uses the file; any other context's is its own, in the state directory.
//
// The lock is never held across the credential store's unlock prompt, which go-keyring's calls
// wait on for as long as the operator leaves it: every other bronzewardctl command on the context
// would wait as long. A read bounded by within, before the lock, lets a locked store prompt (unless
// noPrompt); under the lock every call (readHeld, save, keyringDelete) refuses a store that is
// still, or again, locked, without a prompt.
func (s *store) lock() (func(), error) {
	if s.file != "" {
		unlock, err := renewalLock(s.file + ".lock")
		return unlock, printableError(err)
	}
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	if s.none == "" {
		// The entry is not decoded: context remove clears one whose context lost its oidc section.
		get := keyringGet
		if s.noPrompt {
			get = keyringGetHeld
		}
		// A refused name or an unreadable entry does not stop the lock: login, logout and context remove
		// must go on past them, and every read that needs the entry reports them itself. Nothing is
		// kept under a refused name, so nothing needs its lock, whose file name the name could break.
		_, _, err := get(s.x.Name)
		switch {
		case errors.Is(err, errNameRefused):
			return func() {}, nil
		case err != nil && !errors.Is(err, errEntryUnreadable):
			return nil, err
		}
	}
	return renewalLock(filepath.Join(dir, s.x.Name+".renewal.lock"))
}

// stateDir is the CLI's non-secret state directory: $XDG_STATE_HOME/bronzeward, by default
// ~/.local/state/bronzeward.
func stateDir() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "bronzeward"), nil
}

// errStoreLocked: the credential store is locked, and is not unlocked under the renewal lock.
var errStoreLocked = errors.New("the system credential store is locked; unlock it, then run the command again")

// The two refusals a platform store raises for one entry. A name refused never had a sign-in kept
// under it, and an entry that is unreadable is a sign-in to replace or remove, not to read: login,
// logout and context remove go on past both (the Keychain store raises them, store_darwin.go).
var (
	errNameRefused     = errors.New("the credential store refuses the context's name")
	errEntryUnreadable = errors.New("the credential store holds an entry this program cannot read")
)

func (s *store) save(t *Tokens) error {
	switch {
	case s.file != "":
		return WriteTokens(s.file, t)
	case s.none != "":
		return nil
	}
	b, err := json.Marshal(entry{Server: s.x.Server, Issuer: s.x.OIDC.Issuer, Tokens: *t})
	if err != nil {
		return err
	}
	return keyringSet(s.x.Name, string(b))
}

// clear removes the stored sign-in, never an operator's automation token file, under the renewal
// lock, as ClearTokens does: it returns what it removed, and keeps a sign-in that match, when
// given, refuses (ErrOtherSignIn).
func (s *store) clear(match func(*Tokens) bool) (*Tokens, error) {
	switch {
	case s.file != "":
		return ClearTokens(s.file, match)
	case s.none != "":
		return nil, nil
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	t, err := s.readHeld()
	switch {
	case errors.Is(err, errNameRefused):
		return nil, nil // no sign-in was ever kept under such a name
	case errors.Is(err, errEntryUnreadable):
		// Nothing to revoke. Without a match the entry goes, so that login can keep a new one. With
		// one, the entry cannot be attributed to the identity that asked, so it stays.
		if match != nil {
			return nil, nil
		}
		return nil, keyringDelete(s.x.Name)
	}
	if err != nil && !errors.Is(err, ErrNotTokenFile) || t == nil {
		return nil, err
	}
	if match != nil && !match(t) {
		return t, ErrOtherSignIn
	}
	if derr := keyringDelete(s.x.Name); derr != nil {
		return nil, derr
	}
	return t, err
}

// gone says that the store holds no sign-in.
func (s *store) gone() string {
	if s.file != "" {
		return "the token file " + printable(s.file) + " does not exist"
	}
	return "nothing is stored in " + s.kept()
}

// removed says that clear removed the sign-in, given the error clear returned with it.
func (s *store) removed(err error) string {
	switch {
	case s.file == "":
		return "the sign-in is removed from " + s.kept()
	case errors.Is(err, ErrNotTokenFile):
		return "the token file " + printable(s.file) + " held no access token and is removed"
	}
	return "the token file " + printable(s.file) + " is removed"
}

// keyringService names this client's entries in the credential store.
const keyringService = "bronzewardctl"

// storeBound is how long within waits for the credential store.
var storeBound = time.Minute

// within bounds a credential-store read: a locked store may wait on an unlock prompt. The result
// comes back through the channel, so a call that outlives the bound writes nothing shared.
func within[T any](f func() (T, error)) (T, error) {
	type answer struct {
		v   T
		err error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := f()
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		return a.v, a.err
	case <-time.After(storeBound):
		var zero T
		return zero, errors.New("the credential store did not answer within a minute; it may be locked")
	}
}

// ClearStored removes the credential-store entry of a context removed from the contexts file,
// under its renewal lock, so that a renewal in flight finishes first and a waiting one finds the
// entry gone; a file is the operator's to remove. Without the store it returns a note that an
// entry is not reached. With noInput a locked store is refused, not prompted.
func ClearStored(x *Context, noInput bool) (string, error) {
	// A name that cannot name its lock file never had an entry: every sign-in refuses it.
	if x.Credential != "oidc" || x.TokenFile() != "" || x.checkName() != nil {
		return "", nil
	}
	if why := keyringUnavailable(); why != "" {
		if unreached() == "" {
			return "", nil
		}
		return fmt.Sprintf("The system credential store is not reached, since %s, so a sign-in another session kept there is not removed", why), nil
	}
	failed := func(err error) error {
		return fmt.Errorf("removing the stored sign-in of context \"%s\" from the system credential store: %w", quoted(x.Name), err)
	}
	unlock, err := openStore(x, noInput).lock()
	if err != nil {
		return "", failed(err)
	}
	defer unlock()
	// The contexts file is read under the lock, as SignIn reads it before keeping a sign-in: a
	// sign-in finishing meanwhile is kept before this and removed by it, or finds its context gone.
	// A context added again under the name since owns the entry now.
	cfg, err := LoadConfig()
	if err != nil {
		return "", failed(err)
	}
	if cfg.Find(x.Name) != nil {
		return "", nil
	}
	// lock read the store before taking the lock, bounded and, unless noInput, with its unlock
	// prompt; this read under the lock refuses a store locked again since, without a prompt.
	_, found, err := keyringGetHeld(x.Name)
	switch {
	case errors.Is(err, errNameRefused):
		return "", nil // no sign-in was ever kept under such a name
	case errors.Is(err, errEntryUnreadable):
		found, err = true, nil // it cannot be read, but it is removed
	}
	if err != nil || !found {
		if err != nil {
			err = failed(err)
		}
		return "", err
	}
	if err := keyringDelete(x.Name); err != nil {
		return "", failed(err)
	}
	return "", nil
}

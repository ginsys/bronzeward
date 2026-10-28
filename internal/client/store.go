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
// this process's memory, for the reason in none.
type store struct {
	x    *Context
	file string
	none string
}

func openStore(x *Context) *store {
	s := &store{x: x, file: x.TokenFile()}
	if s.file == "" {
		s.none = keyringUnavailable()
	}
	return s
}

// kept says where the sign-in is kept.
func (s *store) kept() string {
	switch {
	case s.file != "":
		return "the token file " + s.file
	case s.none == "":
		return "the system credential store"
	}
	return "this process's memory only, since " + s.none
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
	if err != nil {
		s.none = "the credential store failed: " + err.Error()
		return nil, nil
	}
	return t, nil
}

// read is load without its fallback: a failing credential store is an error. It may wait, at most
// a minute, on the credential store's unlock prompt, so it is never used under the renewal lock.
func (s *store) read() (*Tokens, error) { return s.get(keyringGet) }

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
	if !found || json.Unmarshal([]byte(v), &e) != nil || e.AccessToken == "" ||
		e.Server != s.x.Server || e.Issuer != s.x.OIDC.Issuer {
		return nil, nil
	}
	return &e.Tokens, nil
}

// lock takes the sign-in's renewal lock (§9.2 item 3). A token file's lies beside it, shared by
// every context that uses the file; any other context's is its own, in the state directory.
//
// The lock is never held across the credential store's unlock prompt, which go-keyring's calls
// wait on for as long as the operator leaves it: every other bronzewardctl command on the context
// would wait as long. A read bounded by within, before the lock, lets a locked store prompt; under
// the lock every call (readHeld, save, keyringDelete) refuses a store that is still, or again,
// locked, without a prompt.
func (s *store) lock() (func(), error) {
	if s.file != "" {
		return renewalLock(s.file + ".lock")
	}
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	if s.none == "" {
		if _, _, err := keyringGet(s.x.Name); err != nil {
			return nil, err
		}
	}
	return renewalLock(filepath.Join(dir, "bronzeward", s.x.Name+".renewal.lock"))
}

// errStoreLocked: the credential store is locked, and is not unlocked under the renewal lock.
var errStoreLocked = errors.New("the system credential store is locked; unlock it, then run the command again")

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
	if err != nil || t == nil {
		return nil, err
	}
	if match != nil && !match(t) {
		return t, ErrOtherSignIn
	}
	return t, keyringDelete(s.x.Name)
}

// gone says that the store holds no sign-in.
func (s *store) gone() string {
	if s.file != "" {
		return "the token file " + s.file + " does not exist"
	}
	return "nothing is stored in " + s.kept()
}

// removed says that clear removed the sign-in, given the error clear returned with it.
func (s *store) removed(err error) string {
	switch {
	case s.file == "":
		return "the sign-in is removed from " + s.kept()
	case errors.Is(err, ErrNotTokenFile):
		return "the token file " + s.file + " held no access token and is removed"
	}
	return "the token file " + s.file + " is removed"
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

// ClearStored removes the credential-store entry of a context being removed, under its renewal
// lock, so that a renewal in flight finishes first and a waiting one finds the entry gone; a file
// is the operator's to remove. Without the store it returns a note that an entry is not reached.
func ClearStored(x *Context) (string, error) {
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
		return fmt.Errorf("removing the stored sign-in of context %q from the system credential store: %w", x.Name, err)
	}
	unlock, err := openStore(x).lock()
	if err != nil {
		return "", failed(err)
	}
	defer unlock()
	if err := keyringDelete(x.Name); err != nil {
		return "", failed(err)
	}
	return "", nil
}

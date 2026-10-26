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

// read is load without its fallback: a failing credential store is an error.
func (s *store) read() (*Tokens, error) {
	switch {
	case s.file != "":
		return ReadTokens(s.file)
	case s.none != "":
		return nil, nil
	}
	v, found, err := keyringGet(s.x.Name)
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
	return renewalLock(filepath.Join(dir, "bronzeward", s.x.Name+".renewal.lock"))
}

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
	t, err := s.read()
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

// within bounds a credential-store call: a locked store may wait on an unlock prompt. The result
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
	case <-time.After(time.Minute):
		var zero T
		return zero, errors.New("the credential store did not answer within a minute; it may be locked")
	}
}

// ClearStored removes the credential-store entry of a context being removed; a file is the
// operator's to remove.
func ClearStored(x *Context) error {
	if x.Credential != "oidc" || x.TokenFile() != "" || keyringUnavailable() != "" {
		return nil
	}
	if err := keyringDelete(x.Name); err != nil {
		return fmt.Errorf("removing the stored sign-in: %w", err)
	}
	return nil
}

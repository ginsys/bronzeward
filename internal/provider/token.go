package provider

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// Token is an OpenBao token. It renders as "[openbao token]" under every fmt verb, and holds the
// value behind a pointer: a struct holding a Token in an unexported field is printed by reflection,
// which no method can intercept, and then shows the pointer, not the string.
type Token struct{ p *string }

const tokenText = "[openbao token]"

func (Token) String() string   { return tokenText }
func (Token) GoString() string { return tokenText }

// Format renders every verb as the placeholder, so %d or %x cannot reach the value by reflection.
func (Token) Format(f fmt.State, _ rune) { io.WriteString(f, tokenText) }

func (t Token) value() string {
	if t.p == nil {
		return ""
	}
	return *t.p
}

// maxTokenFile bounds the read: an OpenBao token is about a hundred bytes.
const maxTokenFile = 64 << 10

// ReadTokenFile reads one static token from a file (the PoC's provider authentication: one token
// per component identity, no renewal). The file must be a regular file reached without following
// a symlink, with no group or other permission bit, holding one token of printable ASCII and at
// most one trailing newline. Errors name the path and never the content.
func ReadTokenFile(path string) (Token, error) {
	// O_NONBLOCK keeps the open of a FIFO with no writer from waiting for one, so the
	// regular-file check below refuses it; a regular file ignores the flag.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Token{}, fmt.Errorf("provider: token file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Token{}, fmt.Errorf("provider: token file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return Token{}, fmt.Errorf("provider: token file %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return Token{}, fmt.Errorf("provider: token file %s has mode %o; it must grant nothing to group or others (0600)", path, fi.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, maxTokenFile+1))
	if err != nil {
		return Token{}, fmt.Errorf("provider: token file: %w", err)
	}
	if len(b) > maxTokenFile {
		return Token{}, fmt.Errorf("provider: token file %s is larger than %d bytes", path, maxTokenFile)
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return Token{}, fmt.Errorf("provider: token file %s is empty", path)
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return Token{}, errors.New("provider: token file " + path + " holds a character outside printable ASCII, or more than one line")
		}
	}
	return Token{p: &s}, nil
}

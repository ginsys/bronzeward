package main

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/dbtest"
)

var (
	tokenLine = regexp.MustCompile(`^bwt_tok_[a-z2-7]{26}\.[A-Za-z0-9_-]{43}\n$`)
	identity  = regexp.MustCompile(`idn_[a-z2-7]{26}`)
)

func TestTokenCommand(t *testing.T) {
	_, dsn := dbtest.New(t)
	cfg := configFile(t, dsn)
	if err := runMigrate([]string{"-config", cfg}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := runToken([]string{"issue", "-config", cfg, "-name", "ci", "-roles", "author,publisher",
		"-responsible", "h-all", "-operator", "h-operator"}, &out, &errb); err != nil {
		t.Fatalf("issue: %v; %s", err, errb.String())
	}
	if !tokenLine.MatchString(out.String()) {
		t.Fatalf("issue printed %q on stdout; want exactly the token line", out.String())
	}
	first := strings.TrimSpace(out.String())
	idn := identity.FindString(errb.String())
	if idn == "" {
		t.Fatalf("issue named no identity: %q", errb.String())
	}
	out.Reset()
	if err := runToken([]string{"list", "-config", cfg}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	_, secret, _ := strings.Cut(first, ".")
	if l := out.String(); !strings.Contains(l, idn) || !strings.Contains(l, "h-all") || !strings.Contains(l, "author,publisher") || strings.Contains(l, secret) {
		t.Fatalf("list: %q", l)
	}
	out.Reset()
	if err := runToken([]string{"rotate", "-config", cfg, "-identity", idn, "-operator", "h-operator"}, &out, io.Discard); err != nil || !tokenLine.MatchString(out.String()) || strings.TrimSpace(out.String()) == first {
		t.Fatalf("rotate: %q, %v", out.String(), err)
	}
	errb.Reset()
	if err := runToken([]string{"revoke", "-config", cfg, "-identity", idn, "-operator", "h-operator"}, io.Discard, &errb); err != nil || !strings.Contains(errb.String(), "revoked tok_") {
		t.Fatalf("revoke: %q, %v", errb.String(), err)
	}
	for name, args := range map[string][]string{
		"approver role":    {"issue", "-config", cfg, "-name", "x", "-roles", "approver", "-responsible", "h-all", "-operator", "h-operator"},
		"missing operator": {"issue", "-config", cfg, "-name", "x", "-roles", "author", "-responsible", "h-all"},
		"flag of another":  {"revoke", "-config", cfg, "-identity", idn, "-operator", "h-operator", "-name", "x"},
		"unknown command":  {"grant", "-config", cfg},
		"no command":       {},
	} {
		if err := runToken(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A denied identity's current token is refused (§10.4), so list must not call it valid.
func TestTokenStateDenied(t *testing.T) {
	if s := tokenState(auth.Listed{IdentityDenied: true, CurrentEpoch: true}); s != "identity-denied" {
		t.Fatalf("state of a denied identity's current token: %q", s)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// The token is its only copy: a write that fails after the transaction committed must not look
// like success, and the error must say the token exists so the operator rotates it.
func TestTokenCommandUnprintedTokenFails(t *testing.T) {
	_, dsn := dbtest.New(t)
	cfg := configFile(t, dsn)
	if err := runMigrate([]string{"-config", cfg}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	err := runToken([]string{"issue", "-config", cfg, "-name", "ci", "-roles", "author",
		"-responsible", "h-all", "-operator", "h-operator"}, failingWriter{}, &errb)
	if err == nil || !strings.Contains(err.Error(), "rotate") || !identity.MatchString(err.Error()) {
		t.Fatalf("issue with a failing stdout: %v; want an error naming the identity and rotate", err)
	}
}

func TestTokenCommandRefusesAnUnmigratedDatabase(t *testing.T) {
	_, dsn := dbtest.New(t)
	err := runToken([]string{"list", "-config", configFile(t, dsn)}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "run bronzeward migrate") {
		t.Fatalf("token list on an unmigrated database: %v", err)
	}
}

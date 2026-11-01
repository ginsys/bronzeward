package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

// The escape has layers, each a mechanism that holds if a call above it is missing: the helpers tf, sf
// and ef escape every argument; client.Shown escapes every error; client.GuardWriter escapes every
// control but the line feed on standard error and, in -o table, standard output. The tests below
// remove the layers one at a time in the mutation run of the commit that added them, and each
// removal fails one of them. A per-site call under a layer is redundant by design (client-contracts.md
// section 6 item 8).

// TestFormatHelpersEscapeEveryArgument: tf, sf and ef escape a string and the text of an error,
// line feeds included.
func TestFormatHelpersEscapeEveryArgument(t *testing.T) {
	hostile, err := "a\x1b\nb", errors.New("c\x1b\nd")
	want := `a\x1b\x0ab c\x1b\x0ad`
	var b bytes.Buffer
	if err := tf(&b, "%s %s", hostile, err); err != nil || b.String() != want {
		t.Errorf("tf wrote %q (%v), want %q", b.String(), err, want)
	}
	if got := sf("%s %s", hostile, err); got != want {
		t.Errorf("sf gave %q, want %q", got, want)
	}
	if got := ef("%s %w", hostile, err).Error(); got != want {
		t.Errorf("ef gave %q, want %q", got, want)
	}
	if !errors.Is(ef("x: %w", err), err) {
		t.Error("ef no longer wraps its error")
	}
}

// TestTableGuard: standard output is guarded in -o table and passed unchanged in json and yaml.
func TestTableGuard(t *testing.T) {
	for _, c := range []struct{ output, want string }{
		{"table", `a\x1b[2J\x09b` + "\n"},
		{"json", "a\x1b[2J\tb\n"},
		{"yaml", "a\x1b[2J\tb\n"},
	} {
		var raw, guarded bytes.Buffer
		w := tableGuard{&env{output: c.output}, &raw, client.GuardWriter(&guarded)}
		if _, err := w.Write([]byte("a\x1b[2J\tb\n")); err != nil {
			t.Fatal(err)
		}
		if got := raw.String() + guarded.String(); got != c.want {
			t.Errorf("-o %s: standard output got %q, want %q", c.output, got, c.want)
		}
	}
}

// TestReportIsTheSink: an error from outside the CLI, one with a line feed in its text, shows as one
// line with the line feed escaped, in the usage branch and the last guard alike, and the exit
// codes are kept.
func TestReportIsTheSink(t *testing.T) {
	outside := errors.New("a\nFORGED")
	for _, c := range []struct {
		name string
		err  error
		code int
	}{
		{"an error", outside, 1},
		{"a usage error", usageError{outside}, 2},
		{"an error in a wrapper", fmt.Errorf("ctx: %w", outside), 1},
	} {
		var o, e bytes.Buffer
		if got := report(c.err, &o, &e); got != c.code {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.code)
		}
		first, _, _ := strings.Cut(e.String(), "\n")
		if !strings.Contains(first, `a\x0aFORGED`) || strings.Contains(e.String(), "\nFORGED") {
			t.Errorf("%s: the first line is %q in %q; want the line feed escaped", c.name, first, e.String())
		}
	}
}

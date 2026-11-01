package client

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestShown: the sink of every error escapes all of its text, line feeds included, and keeps only the
// line breaks of the client's own multi-line errors (client-contracts.md section 6 item 8).
func TestShown(t *testing.T) {
	outside := errors.New("a\nFORGED\t\\b")
	own := &Blocked{outside, []string{"own one", "own two"}}
	unknown := &Unknown{Why: "why\nFORGED", Key: "k", Route: "PUT /x", Where: "ctx", Shows: "s", Check: "c"}
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"an outside error", outside, `a\x0aFORGED\x09\b`},
		{"an outside error in a wrapper", fmt.Errorf("ctx: %w", outside), `ctx: a\x0aFORGED\x09\b`},
		// The negative controls: the client's own line breaks stay, so the guard is not just escaping all.
		{"a blocked error", own, "a\\x0aFORGED\\x09\\b\nown one\nown two"},
		{"a blocked error in a wrapper", fmt.Errorf("ctx: %w", own), "ctx: a\\x0aFORGED\\x09\\b\nown one\nown two"},
		{"a blocked error twice", &Blocked{own, []string{"own three"}}, "a\\x0aFORGED\\x09\\b\nown one\nown two\nown three"},
		{"a flattened blocked error", fmt.Errorf("%v", own), `a\x0aFORGED\x09\b\x0aown one\x0aown two`},
	} {
		if got := Shown(c.err); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// A line feed inside a line of the client's own error is text: it cannot start another line.
	shown := Shown(unknown)
	if !strings.Contains(shown, `outcome unknown: why\x0aFORGED.`) || strings.Count(shown, "\n") != len(unknown.Lines())-1 || strings.Contains(shown, "\nFORGED") {
		t.Errorf("an outcome-unknown block: %q; want %d lines and the line feed in the reason escaped", shown, len(unknown.Lines()))
	}
	// Shown is idempotent, as an error that Shown made can be wrapped and shown again.
	if once := Shown(own); Shown(errors.New(once)) != strings.ReplaceAll(once, "\n", `\x0a`) {
		t.Errorf("a second Shown changed the text: %q", Shown(errors.New(once)))
	}
	// The chain stays: the wrapped error and its type are found.
	if !errors.Is(fmt.Errorf("ctx: %w", own), outside) {
		t.Error("a blocked error no longer wraps its error")
	}
}

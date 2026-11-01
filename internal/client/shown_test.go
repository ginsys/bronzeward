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
		// A join keeps the line breaks between its parts and the lines of each part (writeAfter joins
		// a write's error with the failure to save its state); a line feed inside a part is text.
		{"a join of a blocked error and an outside error", errors.Join(own, outside),
			"a\\x0aFORGED\\x09\\b\nown one\nown two\na\\x0aFORGED\\x09\\b"},
		{"a join in a wrapper", fmt.Errorf("ctx: %w", errors.Join(own, errors.New("save\nFORGED"))),
			"ctx: a\\x0aFORGED\\x09\\b\nown one\nown two\nsave\\x0aFORGED"},
		{"a join in a join in a wrapper", fmt.Errorf("ctx: %w", errors.Join(errors.Join(outside, own), &Blocked{errors.New("x"), []string{"y"}})),
			"ctx: a\\x0aFORGED\\x09\\b\na\\x0aFORGED\\x09\\b\nown one\nown two\nx\ny"},
		{"a blocked error around a join", &Blocked{errors.Join(outside, errors.New("z")), []string{"own"}},
			"a\\x0aFORGED\\x09\\b\nz\nown"},
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
	// The outcome-unknown block keeps its lines when a save failure is joined to it, and nothing a
	// part says can add a line.
	joined := Shown(errors.Join(unknown, errors.New("save\nFORGED2")))
	if strings.Contains(joined, "\nFORGED2") || !strings.Contains(joined, `save\x0aFORGED2`) || strings.Count(joined, "\n") != len(unknown.Lines()) {
		t.Errorf("an outcome-unknown block joined to a save failure: %q", joined)
	}
	// The chain stays: the wrapped error and its type are found.
	if !errors.Is(fmt.Errorf("ctx: %w", own), outside) {
		t.Error("a blocked error no longer wraps its error")
	}
}

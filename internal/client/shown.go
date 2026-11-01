package client

import (
	"errors"
	"strings"
)

// lined is an error of several lines that the client wrote itself: Lines returns them, and a line
// feed inside one is text, not a line break. Error is the lines joined by line feeds.
type lined interface {
	error
	Lines() []string
}

// Blocked is Err followed by More, lines of the client's own, each on a line of its own.
type Blocked struct {
	Err  error
	More []string
}

func (b *Blocked) Error() string { return strings.Join(b.Lines(), "\n") }
func (b *Blocked) Unwrap() error { return b.Err }

// Lines are those of Err when it has its own, else its text as one line, and then More.
func (b *Blocked) Lines() []string {
	first := []string{b.Err.Error()}
	if l, ok := b.Err.(lined); ok {
		first = l.Lines()
	}
	return append(first, b.More...)
}

// Shown is the text of err for the terminal, the one place that prints an error: every control
// character, line feeds included, shows as an escape (client-contracts.md section 6 item 8), but
// the line breaks between the lines of a lined error in err's chain. An error that something
// outside the client wrote, or that carries text from outside, cannot start a line of its own.
// A lined error that a wrapper flattened into its own text shows without its line breaks.
func Shown(err error) string {
	whole := err.Error()
	var l lined
	if errors.As(err, &l) {
		ls := l.Lines()
		raw := strings.Join(ls, "\n")
		for i := range ls {
			ls[i] = printable(ls[i])
		}
		esc := strings.Join(ls, "\n")
		// The text is the wrapper's prefix and the lines, as they were or as an inner Shown made them.
		for _, own := range []string{raw, esc} {
			if prefix, ok := strings.CutSuffix(whole, own); ok {
				return printable(prefix) + esc
			}
		}
	}
	return printable(whole)
}

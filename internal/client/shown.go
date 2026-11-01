package client

import "strings"

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
// A lined error that a wrapper flattened into its own text shows without its line breaks. An error
// that joins several (errors.Join) shows each part by the same rule, one after the other.
func Shown(err error) string { return strings.Join(shownLines(err), "\n") }

// shownLines is the lines of Shown(err), each escaped. It finds the first error in the chain that
// is a Blocked, a lined error or a join, and expects the text of err to be a prefix of the
// wrapper's own followed by that error's text, as it was or as an inner Shown made it.
func shownLines(err error) []string {
	whole := err.Error()
	for e := err; e != nil; {
		var parts []string
		switch x := e.(type) {
		case *Blocked:
			parts = shownLines(x.Err)
			for _, m := range x.More {
				parts = append(parts, printable(m))
			}
		case lined:
			for _, l := range x.Lines() {
				parts = append(parts, printable(l))
			}
		case interface{ Unwrap() []error }:
			for _, p := range x.Unwrap() {
				if p != nil {
					parts = append(parts, shownLines(p)...)
				}
			}
		}
		if len(parts) > 0 {
			for _, own := range []string{e.Error(), strings.Join(parts, "\n")} {
				if prefix, ok := strings.CutSuffix(whole, own); ok {
					return append([]string{printable(prefix) + parts[0]}, parts[1:]...)
				}
			}
			break
		}
		next, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = next.Unwrap()
	}
	return []string{printable(whole)}
}

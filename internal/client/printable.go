package client

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// printable is s as bronzewardctl shows text that a remote party may have chosen (client-contracts.md
// §6 item 4): each control character (C0, DEL, C1) and each byte that is not UTF-8 is written as a
// Go escape, `\x1b` for ESC, so none reaches the terminal as itself. A certificate's DNS name, for
// one, may hold any ASCII, and Go's TLS errors list those names unquoted.
func printable(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r == utf8.RuneError || unicode.IsControl(r) || isBidi(r) }) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r) // every control character is below U+0100
		case isBidi(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// isBidi is a character that changes the order in which a terminal draws the text around it: the
// Arabic letter mark, the left and right marks, the embeddings and overrides, and the isolates.
// Remote text can use them to make one string read as another, so printable writes each as \uNNNN.
func isBidi(r rune) bool {
	return r == 0x061C || r == 0x200E || r == 0x200F || r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069
}

// Printable is printable for bronzewardctl's commands, which show remote text outside an error:
// the sign-in line's subject and issuer, for one (#383).
func Printable(s string) string { return printable(s) }

// ShownStartMode is the start mode as shown: normal or recovery, the values GET /server documents,
// else unknown, so a start mode the server or a hand-edited contexts file holds is never echoed.
func ShownStartMode(m string) string {
	if !apitypes.ServerBodyStartMode(m).Valid() {
		return "unknown"
	}
	return m
}

// printableError is err with its text made printable; errors.Is and errors.As still find what err
// wraps. Every transport or TLS error the client shows passes through it or, for a trust refusal,
// through printable (#380).
func printableError(err error) error {
	if err == nil {
		return nil
	}
	return shown{err}
}

type shown struct{ err error }

func (e shown) Error() string { return printable(e.err.Error()) }
func (e shown) Unwrap() error { return e.err }

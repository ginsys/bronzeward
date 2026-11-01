package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Alternate-screen and cursor sequences of an xterm-compatible terminal.
const (
	altScreenOn  = "\x1b[?1049h\x1b[H"
	altScreenOff = "\x1b[?1049l"
	clearScreen  = "\x1b[H\x1b[2J"
)

// page shows text one screen at a time on a terminal of rows by cols that is in raw mode, reading
// keys from keys: space or f forward, b back, q, Ctrl-C or the end of keys quits (#256 T14). It
// has no search, save or export. Every line is wrapped to cols, so none of it is cut off, and the
// last row is the key line. It writes only to w.
func page(text string, rows, cols int, keys io.Reader, w io.Writer) error {
	rows, cols = max(rows, 2), max(cols, 20)
	lines := wrap(text, cols)
	body := rows - 1
	top := 0
	k := bufio.NewReader(keys)
	for {
		var b strings.Builder
		b.WriteString(clearScreen)
		end := min(top+body, len(lines))
		for _, l := range lines[top:end] {
			b.WriteString(l + "\r\n")
		}
		for range body - (end - top) {
			b.WriteString("~\r\n")
		}
		at := "(end)"
		if end < len(lines) {
			at = fmt.Sprintf("lines %d-%d of %d", top+1, end, len(lines))
		}
		fmt.Fprintf(&b, "-- %s -- space or f forward, b back, q quit", at)
		if _, err := io.WriteString(w, b.String()); err != nil {
			return err
		}
		c, err := k.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch c {
		case ' ', 'f':
			if end < len(lines) {
				top = end
			}
		case 'b':
			top = max(top-body, 0)
		case 'q', 0x03:
			return nil
		}
	}
}

// wrap splits text into lines of at most cols runes, a tab counted as one. A carriage return is
// shown as ^M, so a line cannot overwrite the one shown before it, every other C0 control
// character as ^ and its letter, and a C1 one as ?, so none reaches the terminal as a control
// sequence.
func wrap(text string, cols int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		var b strings.Builder
		n := 0
		for _, r := range l {
			s := string(r)
			switch {
			case r == '\t':
				s = " "
			case r < 0x20 || r == 0x7f:
				s = "^" + string(rune(r^0x40))
			case r == utf8.RuneError, r >= 0x80 && r < 0xa0:
				s = "?"
			}
			for _, c := range s {
				if n == cols {
					out = append(out, b.String())
					b.Reset()
					n = 0
				}
				b.WriteRune(c)
				n++
			}
		}
		out = append(out, b.String())
	}
	return out
}

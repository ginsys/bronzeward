package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/width"

	"github.com/ginsys/bronzeward/internal/client"
)

// cells is the terminal columns r takes: two for a wide or full-width character, else one.
func cells(r rune) int {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// Alternate-screen and cursor sequences of an xterm-compatible terminal.
const (
	altScreenOn  = "\x1b[?1049h\x1b[H"
	altScreenOff = "\x1b[?1049l"
	clearScreen  = "\x1b[H\x1b[2J"
)

// page shows text one screen at a time on a terminal of rows by cols that is in raw mode, reading
// keys from keys: space or f forward, b back, q, Ctrl-C or the end of keys quits (#256 T14). It
// has no search, save or export. Every line is wrapped to cols, so none of it is cut off, and the
// last row is the key line, cut to cols. It writes only to w.
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
		// The key line is ASCII, cut to the width so that it never wraps and scrolls the page.
		foot := fmt.Sprintf("-- %s -- space or f forward, b back, q quit", at)
		b.WriteString(foot[:min(len(foot), cols)])
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

// wrap splits text into its lines, escapes each line as all remote text is escaped
// (client-contracts.md §6 item 8), and cuts it into lines of at most cols runes. The line feeds
// alone stay: a tab, a carriage return, any other control character, a byte that is not UTF-8 and
// a bidirectional control show as their Go escape, never as themselves, and an escape is never cut.
// A line holds at most cols terminal cells, a wide character counted as two.
func wrap(text string, cols int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		var b strings.Builder
		n := 0
		for i := 0; i < len(l); {
			_, size := utf8.DecodeRuneInString(l[i:])
			s := client.Printable(l[i : i+size])
			i += size
			w := 0
			for _, r := range s {
				w += cells(r)
			}
			if n > 0 && n+w > cols {
				out = append(out, b.String())
				b.Reset()
				n = 0
			}
			b.WriteString(s)
			n += w
		}
		out = append(out, b.String())
	}
	return out
}

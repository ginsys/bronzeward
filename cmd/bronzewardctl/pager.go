package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"

	"github.com/ginsys/bronzeward/internal/client"
)

// cells is the terminal columns r takes: none for a non-spacing or enclosing mark, which sits on
// the character before it, two for a wide or full-width character, else one. A spacing mark (Mc)
// takes its own column, as wcwidth gives it.
func cells(r rune) int {
	if unicode.In(r, unicode.Mn, unicode.Me) {
		return 0
	}
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

// The smallest terminal the pager takes: one row of text above the key line, and room on a row for
// three of the longest escape (‮, six columns), so that no escape is wider than its row.
const minRows, minCols = 2, 20

// event is what ends the wait for a key.
type event int

const (
	evKey    event = iota // a key came
	evResize              // the terminal was resized
	evSignal              // SIGINT, SIGTERM or SIGHUP came
)

// keySource gives the pager its keys one byte at a time, and tells it of a resize or a signal.
type keySource interface {
	next() (key byte, ev event, err error)
}

// errSignalled is what pageOn returns on an evSignal: the caller knows which signal it was.
var errSignalled = errors.New("the pager was ended by a signal")

// readerKeys is a keySource over a plain reader: it knows no resize and no signal.
type readerKeys struct{ r io.Reader }

func (k readerKeys) next() (byte, event, error) {
	var key [1]byte
	_, err := io.ReadFull(k.r, key[:])
	return key[0], evKey, err
}

// page shows lines one screen at a time on a terminal of rows by cols that is in raw mode, reading
// keys from keys: space or f forward, b back, q, Ctrl-C or the end of keys quits (#256 T14). It
// has no search, save or export. Every line is wrapped to cols, so none of it is cut off, and the
// last row is the key line, cut to cols. It writes only to w. The caller gives at least minRows by
// minCols, as screenSize makes sure of.
func page(text []string, rows, cols int, keys io.Reader, w io.Writer) error {
	return pageOn(text, func() (int, int) { return rows, cols }, readerKeys{keys}, w)
}

// pageOn is page for a terminal that can be resized: on evResize it asks size for the new rows and
// columns. A size of 0 by 0 means the terminal is below the smallest the pager takes: it shows a
// notice instead of a page, ignores every key but quit, and shows the page again once the size is
// usable. With the same columns it keeps the page; with other columns it wraps the text again and
// shows the page that starts at the same source line. It returns errSignalled on evSignal, before
// it writes anything more. Everything it writes, it writes
// from the goroutine that calls it, so a caller that restores the terminal after it returns cannot
// race a redraw.
func pageOn(text []string, size func() (rows, cols int), keys keySource, w io.Writer) error {
	rows, cols := size()
	small := rows == 0 || cols == 0
	if small {
		rows, cols = minRows, minCols
	}
	lines, src := wrapSrc(text, cols)
	top := 0
	for {
		body := rows - 1
		var b strings.Builder
		b.WriteString(clearScreen)
		if small {
			// Short enough for a row of the smallest terminal, and not wrapped on one a little smaller.
			b.WriteString("Window too small")
			if _, err := io.WriteString(w, b.String()); err != nil {
				return err
			}
			key, ev, err := keys.next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			switch {
			case ev == evSignal:
				return errSignalled
			case ev == evResize:
				rows, cols, small = resized(text, size, rows, cols, &lines, &src, &top)
			case key == 'q' || key == 0x03:
				return nil
			}
			continue
		}
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
		// One byte is read at a time, so nothing typed after the key that quits is taken from keys:
		// the marks prompt reads it.
		key, ev, err := keys.next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch ev {
		case evSignal:
			return errSignalled
		case evResize:
			rows, cols, small = resized(text, size, rows, cols, &lines, &src, &top)
			continue
		}
		switch key {
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

// resized reads the new size after a resize and fits the page to it: with the same columns the page
// stays, with others the text is wrapped again and the page starts at the same source line. It
// returns the dimensions to draw at and whether the terminal is now too small to draw on; a
// terminal too small keeps the last usable dimensions and page.
func resized(text []string, size func() (int, int), rows, cols int, lines *[]string, src *[]int, top *int) (int, int, bool) {
	r, c := size()
	if r == 0 || c == 0 {
		return rows, cols, true
	}
	if c != cols {
		first := 0
		if *top < len(*src) {
			first = (*src)[*top]
		}
		*lines, *src = wrapSrc(text, c)
		*top = max(slices.Index(*src, first), 0)
	}
	*top = min(*top, max(len(*lines)-1, 0))
	return r, c, false
}

// wrap escapes each of lines as all remote text is escaped (client-contracts.md §6 item 8) and cuts
// it into screen lines of at most cols terminal cells, a wide character counted as two. Each line
// starts a screen line: a line feed, a tab, a carriage return, any other control character, a byte
// that is not UTF-8 and a bidirectional control inside a line show as their Go escape, never as
// themselves, and an escape is never cut.
func wrap(lines []string, cols int) []string {
	out, _ := wrapSrc(lines, cols)
	return out
}

// wrapSrc is wrap that also says, for each screen line, the index of the line it came from.
func wrapSrc(lines []string, cols int) (out []string, src []int) {
	for i, l := range lines {
		for _, s := range wrapLine(l, cols) {
			out = append(out, s)
			src = append(src, i)
		}
	}
	return out, src
}

// wrapLine is the screen lines of one line. A character and the marks after it are one group that
// is never cut, so a mark never starts a screen line, unless the group alone is wider than a row.
func wrapLine(l string, cols int) []string {
	var out []string
	var b, group strings.Builder
	n, gw := 0, 0
	flush := func() {
		if n > 0 && n+gw > cols {
			out = append(out, b.String())
			b.Reset()
			n = 0
		}
		b.WriteString(group.String())
		n += gw
		group.Reset()
		gw = 0
	}
	for i := 0; i < len(l); {
		r, size := utf8.DecodeRuneInString(l[i:])
		s := client.Printable(l[i : i+size])
		i += size
		w := 0
		for _, c := range s {
			w += cells(c)
		}
		// A mark that is shown as itself joins the group before it, if the row has room for it. When
		// the group is already a whole row, the mark shows as its Go escape, like any other
		// character the pager does not show as itself, so a mark never starts a screen line.
		if unicode.IsMark(r) && s == string(r) && group.Len() > 0 {
			if gw+w <= cols {
				group.WriteString(s)
				gw += w
				continue
			}
			s = strings.Trim(strconv.QuoteToASCII(s), `"`)
			w = len(s)
		}
		flush()
		group.WriteString(s)
		gw = w
	}
	flush()
	return append(out, b.String())
}

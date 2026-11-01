package main

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/tabwriter"

	"github.com/ginsys/bronzeward/internal/client"
	"go.yaml.in/yaml/v3"
)

// shown is args with every string escaped as a value (client-contracts.md §6 item 8): a string
// argument is remote text until the CLI proves it is its own, and escaping the CLI's own text
// changes nothing. An error argument is shown by its escaped text. Any other type is not text.
func shown(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
		if err, ok := a.(error); ok {
			out[i] = client.Printable(err.Error())
		} else if v := reflect.ValueOf(a); v.Kind() == reflect.String {
			out[i] = client.Printable(v.String())
		}
	}
	return out
}

// tf writes a formatted line of human output. Every table line, record line and sentence is
// written through tf or sf, so no remote string reaches the terminal as received.
func tf(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, shown(args)...)
	return err
}

// sf is tf for a line that is built first, as a write's answer is.
func sf(format string, args ...any) string { return fmt.Sprintf(format, shown(args)...) }

// ef is fmt.Errorf with the string arguments escaped. An error argument keeps its place in the
// chain, so %w wraps it and errors.Is and errors.As still find what it wraps. Its text is escaped
// too, line feeds apart: a decoder error can quote a byte the server chose, and the errors of the
// client are multi-line blocks.
func ef(format string, args ...any) error {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
		if err, ok := a.(error); ok {
			out[i] = linesShown{err}
		} else {
			out[i] = shown([]any{a})[0]
		}
	}
	return fmt.Errorf(format, out...)
}

// linesShown is an error whose text is escaped line by line; errors.Is and errors.As see through it.
type linesShown struct{ err error }

func (e linesShown) Error() string {
	ls := strings.Split(e.err.Error(), "\n")
	for i, l := range ls {
		ls[i] = client.Printable(l)
	}
	return strings.Join(ls, "\n")
}

func (e linesShown) Unwrap() error { return e.err }

// emit prints raw, a JSON document, as -o asks: json prints it unchanged; yaml prints the same
// members in the same order and with the same values; table runs table. The CLI uses no
// colour or terminal control, so NO_COLOR and non-terminal output are already plain text with
// the same information (operator-experience.md §13).
func (e *env) emit(raw []byte, table func(*tabwriter.Writer)) error {
	if e.output == "json" || e.output == "yaml" {
		b, err := e.render(raw)
		if err != nil {
			return err
		}
		_, err = e.out.Write(b)
		return err
	}
	w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	table(w)
	return w.Flush()
}

// render is what emit prints for -o json or yaml, built in memory so that a conversion failure
// is known before anything is written.
func (e *env) render(raw []byte) ([]byte, error) {
	if e.output == "json" {
		if !bytes.HasSuffix(raw, []byte("\n")) {
			raw = append(raw, '\n')
		}
		return raw, nil
	}
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	block(&n)
	return yaml.Marshal(&n)
}

// block drops JSON's flow and quoting styles; the encoder still quotes a string that would
// otherwise read as another type.
func block(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		block(c)
	}
}

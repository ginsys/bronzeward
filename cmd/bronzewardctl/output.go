package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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

// linesShown is an error whose text is client.Shown's: escaped, but for the line breaks of the
// client's own multi-line errors; errors.Is and errors.As see through it.
type linesShown struct{ err error }

func (e linesShown) Error() string { return client.Shown(e.err) }

func (e linesShown) Unwrap() error { return e.err }

// outputFailed is a failed write of the command's output, such as a full disk: exit 6
// (operator-experience.md §4). A closed pipe is not one: Go ends the process by SIGPIPE before a
// write returns. errors.Is and errors.As see through it.
type outputFailed struct{ err error }

func (e outputFailed) Error() string { return e.err.Error() }

func (e outputFailed) Unwrap() error { return e.err }

// tableGuard is standard output: guarded in -o table, as it is by the time of each write, since a
// global flag can come after the command; json and yaml print the API resource unchanged. It is the
// one place that tags a failed write as outputFailed, so every command that prints through e.out
// exits 6 with no code of its own.
type tableGuard struct {
	e       *env
	raw     io.Writer
	guarded io.Writer
}

func (t tableGuard) Write(p []byte) (int, error) {
	w := t.raw
	if t.e.output == "table" {
		w = t.guarded
	}
	n, err := w.Write(p)
	if err != nil {
		return n, outputFailed{err}
	}
	return n, nil
}

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
	w, flush := e.newTable()
	table(w)
	return flush()
}

// stickyWriter passes writes to w until one fails, then refuses every later write with that error,
// so a sink that failed once receives nothing more.
type stickyWriter struct {
	w   io.Writer
	err error
}

func (s *stickyWriter) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.w.Write(p)
	if err != nil {
		s.err = err
	}
	return n, err
}

// newTable is a table on standard output and the function that ends it. The tabwriter flushes by
// itself while the rows are written, and a row's callback does not read the error of those writes.
// The sticky writer keeps the first one and stops the writes after it, so the final flush cannot
// retry the buffered text and print its accepted part twice; flush returns that first error.
func (e *env) newTable() (*tabwriter.Writer, func() error) {
	s := &stickyWriter{w: e.out}
	w := tabwriter.NewWriter(s, 0, 0, 2, ' ', 0)
	return w, func() error {
		ferr := w.Flush()
		if s.err != nil {
			return s.err
		}
		return ferr
	}
}

// render is what emit prints for -o json or yaml, built in memory so that a conversion failure
// is known before anything is written.
func (e *env) render(raw []byte) ([]byte, error) {
	// DEL, the C1 controls and bidirectional controls are written as \u escapes for both outputs: YAML
	// rejects DEL and most C1 characters raw, folds U+0085 into a space, and reads the same escape
	// inside a double-quoted string as the same text.
	raw = client.EscapeJSON(raw)
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

// printComposite prints a composite's object for -o json or yaml, once and through render: json is
// escaped as one encoding of the whole object, yaml is one document with no "---"
// (operator-experience.md §5.8, "Structured output of a composite"). It is built in memory first, so a
// conversion failure is known before anything is written, and a failed write is outputFailed.
func (e *env) printComposite(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b, err := e.render(buf.Bytes())
	if err != nil {
		return err
	}
	_, err = e.out.Write(b)
	return err
}

// block drops JSON's flow and quoting styles; the encoder still quotes a string that would
// otherwise read as another type.
func block(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		block(c)
	}
}

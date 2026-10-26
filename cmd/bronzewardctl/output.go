package main

import (
	"bytes"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"
)

// emit prints raw, a JSON document, as -o asks: json prints it unchanged; yaml prints the same
// members in the same order and with the same values; table runs table. The CLI uses no
// colour or terminal control, so NO_COLOR and non-terminal output are already plain text with
// the same information (operator-experience.md §13).
func (e *env) emit(raw []byte, table func(*tabwriter.Writer)) error {
	switch e.output {
	case "json":
		if !bytes.HasSuffix(raw, []byte("\n")) {
			raw = append(raw, '\n')
		}
		_, err := e.out.Write(raw)
		return err
	case "yaml":
		var n yaml.Node
		if err := yaml.Unmarshal(raw, &n); err != nil {
			return err
		}
		block(&n)
		b, err := yaml.Marshal(&n)
		if err != nil {
			return err
		}
		_, err = e.out.Write(b)
		return err
	}
	w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', tabwriter.StripEscape)
	table(w)
	return w.Flush()
}

// block drops JSON's flow and quoting styles; the encoder still quotes a string that would
// otherwise read as another type.
func block(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		block(c)
	}
}

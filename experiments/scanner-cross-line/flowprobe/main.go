// Probe of go.yaml.in/yaml/v3 v3.0.5 (the version the root go.mod of ginsys/bronzeward pins) for
// ginsys/bronzeward#616. For each document it prints the error text of yaml.Unmarshal, or the value.
// Rows 1 to 8 are flow documents where the next line starts with `?`, `[` or `{`, the starts at issue.
// Rows C1 to C4 are controls: documents that must parse, so that an error on rows 1 to 8 is the shape
// and not a broken harness. The probe reads nothing and writes nothing but its standard output.
package main

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

func main() {
	docs := []struct{ id, text string }{
		{"1 seq anchor, ? start", "k: [!bwref &e x/foo\n? y]\n"},
		{"2 seq anchor, [ start", "k: [!bwref &e x/foo\n[y]]\n"},
		{"3 seq anchor, { start", "k: [!bwref &e x/foo\n{y}]\n"},
		{"4 map anchor, ? start", "k: {a: !bwref &e x/foo\n? y}\n"},
		{"5 map anchor, [ start", "k: {a: !bwref &e x/foo\n[y]}\n"},
		{"6 map anchor, { start", "k: {a: !bwref &e x/foo\n{y}}\n"},
		{"7 seq explicit key, ? start", "k: [? !bwref &e x/foo\n? y]\n"},
		{"8 map explicit key, ? start", "k: {? !bwref &e x/foo\n? y}\n"},
		{"9 seq no anchor, [ start", "k: [!bwref x/foo\n[y]]\n"},
		{"10 map no anchor, { start", "k: {a: !bwref x/foo\n{y}}\n"},
		{"C1 control: seq, comma start", "k: [!bwref &e x/foo\n, y]\n"},
		{"C2 control: seq, plain word start", "k: [!bwref &e x/foo\nbar]\n"},
		{"C3 control: map, colon start", "k: {? !bwref &e x/foo\n: v}\n"},
		{"C4 control: seq, close start", "k: [!bwref &e x/foo\n]\n"},
	}
	for _, d := range docs {
		var v any
		err := yaml.Unmarshal([]byte(d.text), &v)
		if err != nil {
			fmt.Printf("%-34s ERROR %q\n", d.id, err.Error())
			continue
		}
		fmt.Printf("%-34s OK    %#v\n", d.id, v)
	}
}

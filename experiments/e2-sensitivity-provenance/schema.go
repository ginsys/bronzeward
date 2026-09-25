package main

import (
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
)

// schemaMarker is what the machinery's own redaction writes; the leaves where it did are the
// schema-identified secret fields.
const schemaMarker = "E2SP-SCHEMA-REDACTED"

// schemaPaths is the schema representation: the Talos machinery (the pinned talosctl's own
// v1.13.6 module) loads the configuration and redacts the fields its types mark secret. The
// leaves where its redacted and unredacted encodings differ are returned as `<doc> <path>`. Paths
// are display paths, because the machinery writes the fields a patch fragment leaves out (so a
// fragment's leaf positions differ from its encoding's) but names every field the same.
func schemaPaths(text []byte) (map[string]bool, error) {
	p, err := configloader.NewFromBytes(text)
	if err != nil {
		return nil, fmt.Errorf("the machinery cannot load it: %w", err)
	}
	opt := encoder.WithComments(encoder.CommentsDisabled)
	raw, err := p.EncodeBytes(opt)
	if err != nil {
		return nil, err
	}
	red, err := p.RedactSecrets(schemaMarker).EncodeBytes(opt)
	if err != nil {
		return nil, err
	}
	a, err := parseTree(raw, nil)
	if err != nil {
		return nil, err
	}
	b, err := parseTree(red, nil)
	if err != nil {
		return nil, err
	}
	if f := sameShape(a, b, "schema"); f != nil {
		return nil, fmt.Errorf("%s", f[0])
	}
	out := map[string]bool{}
	for i, l := range a.Leaves {
		if l.Node.Value != b.Leaves[i].Node.Value {
			out[l.Doc+" "+l.Path] = true
		}
	}
	return out, nil
}

package compile

import (
	"bytes"
	"errors"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// schemaToken stands for a field the pinned machinery marks secret (compilation.md §8.3, the
// schema means).
const schemaToken = "<redacted:schema>"

var (
	errNotRedacted   = errors.New("compile: no redacted configuration")
	errRedactOpaque  = errors.New("compile: a value did not decode, so nothing is shown")
	errRedactEncode  = errors.New("compile: the composed configuration does not re-encode as composed")
	errRedactRewrite = errors.New("compile: the composed configuration could not be redacted")
	errRedactSurvive = errors.New("compile: a value survived redaction, so nothing is shown")
)

// Redacted is the compiled configuration as compilation.md §8.3 shows it, all three means
// applied: every leaf provenance attributes to a reference, and the key of a mapping reference's
// member, is the reference's token (<redacted:REF@VERSION>, with #N for member N); every field
// the pinned machinery marks secret is <redacted:schema>; every remaining scalar or key equal to
// a value form, and every copy of one of six bytes or more inside one, is <redacted:value>.
// Embedded documents are written back with their tokens, JSON with its angle brackets escaped.
// It holds no value, so it is a plain string. A compilation that could not be redacted shows
// nothing and says so.
func (c Compiled) Redacted() (string, error) {
	if c.redacted == nil {
		if c.redactErr != nil {
			return "", c.redactErr
		}
		return "", errNotRedacted
	}
	return *c.redacted, nil
}

// redactedText renders the real composition b redacted: hosts names its identified embedded
// documents, outcomes the output paths each tracer reached, r the value forms. The schema
// secrets are the leaves the machinery's own redaction changes, as ingestion identifies them. The
// result is checked last: a value form of copyFloor bytes or more anywhere in it refuses.
func redactedText(b []byte, hosts map[string]string, outcomes []outcome, r redactor) (string, error) {
	if r.opaque {
		return "", errRedactOpaque
	}
	schema, err := schemaLeaves(b)
	if err != nil {
		return "", err
	}
	tokens := map[string]string{}
	members := map[string]bool{}
	for _, o := range outcomes {
		if len(o.paths) > 0 && r.exact[o.tracer.Ref()] {
			return "", errRedactSurvive
		}
		for _, p := range o.paths {
			tokens[p] = tokenOf(traced{o.tracer, o.source})
			members[p] = o.tracer.Member() >= 0
		}
	}
	text := func(n *yaml.Node, s string) {
		*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	}
	value := func(s string) string {
		if r.exact[s] {
			return valueToken
		}
		return r.values(s)
	}
	met := map[string]bool{}
	out, err := ingest.RewriteLeaves(b, hosts, func(p ingest.Path, n *yaml.Node) error {
		switch k := p.String(); {
		case tokens[k] != "":
			met[k] = true
			text(n, tokens[k])
		case schema[k]:
			text(n, schemaToken)
		case n.Kind == yaml.ScalarNode:
			if v := value(n.Value); v != n.Value {
				text(n, v)
			}
		}
		return nil
	}, func(p ingest.Path, n *yaml.Node) error {
		v := value(n.Value)
		if members[p.String()] {
			v = tokens[p.String()]
		}
		if v != n.Value {
			text(n, v)
		}
		return nil
	})
	// Every attributed leaf must have been met where attribution found it; one that was not, such
	// as an integer the value means does not look for, would be shown as it is.
	if err != nil || len(met) != len(tokens) {
		return "", errRedactRewrite
	}
	if r.holds(string(out)) {
		return "", errRedactSurvive
	}
	return string(out), nil
}

// schemaLeaves is the path of every scalar of b that the pinned machinery's RedactSecrets
// changes, read from b loaded and encoded again, which must give b itself.
func schemaLeaves(b []byte) (map[string]bool, error) {
	cfg, err := configloader.NewFromBytes(b)
	if err != nil {
		return nil, errRedactEncode
	}
	opt := encoder.WithComments(encoder.CommentsDisabled)
	raw, err := cfg.EncodeBytes(opt)
	if err != nil || !bytes.Equal(raw, b) {
		return nil, errRedactEncode
	}
	red, err := cfg.RedactSecrets("bronzeward-redacted-" + provider.NewValueID()).EncodeBytes(opt)
	if err != nil {
		return nil, errRedactEncode
	}
	scalars := func(b []byte) (map[string]string, error) {
		out := map[string]string{}
		err := ingest.WalkLeaves(b, nil, func(p ingest.Path, n *yaml.Node) error {
			if n.Kind == yaml.ScalarNode {
				out[p.String()] = n.Value
			}
			return nil
		})
		return out, err
	}
	a, errA := scalars(raw)
	z, errZ := scalars(red)
	if errA != nil || errZ != nil {
		return nil, errRedactEncode
	}
	out := map[string]bool{}
	for p, v := range a {
		if w, ok := z[p]; !ok || w != v {
			out[p] = true
		}
	}
	return out, nil
}

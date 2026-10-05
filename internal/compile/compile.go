package compile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/validation"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// Mode is a node's platform mode, in which its configuration is validated (compilation.md §6
// step 8). The talos module keeps its own mode type under internal/, so the compiler implements
// the machinery's validation.RuntimeMode itself (§10.1).
type Mode string

const (
	ModeMetal     Mode = "metal"
	ModeContainer Mode = "container"
	ModeCloud     Mode = "cloud"
)

var _ validation.RuntimeMode = ModeMetal

// ParseMode is the mode named s.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeMetal, ModeContainer, ModeCloud:
		return m, nil
	}
	return "", errors.New("compile: an unknown platform mode")
}

func (m Mode) String() string        { return string(m) }
func (m Mode) RequiresInstall() bool { return m == ModeMetal }
func (m Mode) InContainer() bool     { return m == ModeContainer }

// Rule names why a composition or its validation was refused.
type Rule string

const (
	RuleRejected     Rule = "rejected"      // the machinery cannot load an input or merge a fragment
	RuleReservedText Rule = "reserved-text" // a string of the composed output holds the text !bwref
	RuleInvalid      Rule = "invalid"       // the composed configuration fails validation
)

// Error is a refused composition or a failed validation. It names the rule, the input the
// machinery rejected ("base" or "fragment[<i>]") and the documents a check refused. Message is
// the machinery's message as compilation.md §8.3 shows it, set by Compile only: redacted by the
// trace pass's message of the same step, or a notice that it is withheld. The message as the
// machinery wrote it quotes values and is never kept. A native rejection is a correct result,
// not a compiler fault (compilation.md §7). The renderer record's refusals (RuleContract,
// RuleKubernetes) carry a fixed text naming the check instead, never the input.
type Error struct {
	Rule    Rule
	Input   string
	Paths   []string
	Message string
}

func (e *Error) Error() string {
	s := fmt.Sprintf("compile: refused (%s)", e.Rule)
	switch {
	case e.Input != "":
		s += " at " + e.Input
	case len(e.Paths) > 0:
		s += " at " + strings.Join(e.Paths, ", ")
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// Materialized is one machine's complete composed configuration: plaintext. Every fmt verb
// prints a placeholder and the marshallers fail. The text sits behind a pointer to a string: fmt
// prints a struct holding a Materialized in an unexported field by reflection, past those
// methods, and under a verb a pointer does not take (%s, %q) it dereferences a pointer to a
// slice, array, struct or map, but never one to a string.
type Materialized struct{ s *string }

// Compose performs compilation.md §6 steps 5 and 7 (reserved text): the base is the composition
// input and each fragment, in order, a strategic merge patch, applied as talosctl's
// machineconfig patch applies them. The result is always loaded and re-encoded without comments,
// so that a composition without fragments is normalized as one with them. A fragment that loads
// as a JSON6902 patch is refused by ingest's rule, returned as is; ingestion already refuses such
// a fragment as schema-unloadable, so this holds only for a Resolved made some other way.
func Compose(base ingest.Resolved, fragments []ingest.Resolved) (Materialized, error) {
	m, _, err := compose(base, fragments)
	return m, err
}

// compose is Compose, also giving the machinery's message for a rejection: plaintext, which
// quotes values, for Compile's template redaction only.
func compose(base ingest.Resolved, fragments []ingest.Resolved) (Materialized, string, error) {
	in := base.Input()
	if _, err := in.Config(); err != nil {
		return Materialized{}, err.Error(), &Error{Rule: RuleRejected, Input: "base"}
	}
	for i, f := range fragments {
		name := fmt.Sprintf("fragment[%d]", i)
		p, err := f.Patch()
		if err != nil {
			var r *ingest.Refusal
			if errors.As(err, &r) {
				return Materialized{}, "", fmt.Errorf("compile: %s: %w", name, err)
			}
			// ingest's Patch drops the machinery's message; loading the same bytes as it does
			// gives it again.
			cause := ""
			if b, berr := f.Input().Bytes(); berr == nil {
				if _, lerr := configpatcher.LoadPatch(b); lerr != nil {
					cause = lerr.Error()
				}
			}
			return Materialized{}, cause, &Error{Rule: RuleRejected, Input: name}
		}
		if in, err = configpatcher.Apply(in, []configpatcher.Patch{p}); err != nil {
			return Materialized{}, err.Error(), &Error{Rule: RuleRejected, Input: name}
		}
	}
	cfg, err := in.Config()
	if err != nil {
		return Materialized{}, err.Error(), &Error{Rule: RuleRejected}
	}
	out, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		return Materialized{}, err.Error(), &Error{Rule: RuleRejected}
	}
	paths, err := reservedText(out)
	if err != nil {
		return Materialized{}, "", err
	}
	if len(paths) > 0 {
		return Materialized{}, "", &Error{Rule: RuleReservedText, Paths: paths}
	}
	s := string(out)
	return Materialized{s: &s}, "", nil
}

const reserved = "!bwref"

// reservedText is every document of the composed stream holding a scalar, key or tag with the
// reserved text (compilation.md §5.5).
func reservedText(b []byte) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var paths []string
	for doc := 0; ; doc++ {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return paths, nil
		}
		if err != nil {
			return nil, errors.New("compile: the composed configuration does not parse")
		}
		if holdsReserved(&n) {
			paths = append(paths, fmt.Sprintf("doc[%d]", doc))
		}
	}
}

func holdsReserved(n *yaml.Node) bool {
	if strings.Contains(n.Tag, reserved) || n.Kind == yaml.ScalarNode && strings.Contains(n.Value, reserved) {
		return true
	}
	for _, c := range n.Content {
		if holdsReserved(c) {
			return true
		}
	}
	return false
}

// Validate performs compilation.md §6 step 8: the configuration as it would be published is
// loaded again and validated in the node's mode, as talosctl validate --strict validates it
// (local, warnings as errors). Warnings that strict mode leaves as warnings are not reported.
func (m Materialized) Validate(mode Mode) error {
	_, err := m.validate(mode)
	return err
}

// validate is Validate, also giving the machinery's message for an invalid configuration:
// plaintext, which quotes values, for Compile's template redaction only.
func (m Materialized) validate(mode Mode) (string, error) {
	if m.s == nil {
		return "", errors.New("compile: no composed configuration to validate")
	}
	if _, err := ParseMode(string(mode)); err != nil {
		return "", err
	}
	cfg, err := configloader.NewFromBytes([]byte(*m.s))
	if err != nil {
		return err.Error(), &Error{Rule: RuleInvalid}
	}
	if _, err := cfg.Validate(mode, validation.WithLocal(), validation.WithStrict()); err != nil {
		return err.Error(), &Error{Rule: RuleInvalid}
	}
	return "", nil
}

// bytes is a copy of the composed configuration, for this package only.
func (m Materialized) bytes() []byte {
	if m.s == nil {
		return nil
	}
	return []byte(*m.s)
}

const materializedText = "[materialized configuration]"

var errMaterializedRender = errors.New("compile: a materialized configuration is not marshalled")

func (Materialized) String() string               { return materializedText }
func (Materialized) GoString() string             { return materializedText }
func (Materialized) Format(f fmt.State, _ rune)   { io.WriteString(f, materializedText) }
func (Materialized) MarshalJSON() ([]byte, error) { return nil, errMaterializedRender }
func (Materialized) MarshalText() ([]byte, error) { return nil, errMaterializedRender }
func (Materialized) MarshalYAML() (any, error)    { return nil, errMaterializedRender }

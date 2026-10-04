package compile

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// messageFixture traces one fragment with a string, an integer, a mapping and a boolean reference
// and gives its tracers, the redactor of its values and each tracer's stand-in by name ("str",
// "port", "map#0", "map#1"), each checked against the tracer.
func messageFixture(t *testing.T) ([]traced, redactor, map[string]string) {
	t.Helper()
	frag := source(t, "machine:\n  nodeLabels:\n    s: !bwref app/str\n    p: !bwref app/port\n"+
		"  nodeAnnotations: !bwref app/map\n  features:\n    rbac: !bwref app/bool\n",
		refs(map[string]ingest.Reference{
			"app/str": ref(provider.KindString), "app/port": ref(provider.KindInteger),
			"app/bool": ref(provider.KindBoolean), "app/map": {Kind: provider.KindMapping, Version: 3},
		}),
		map[string]provider.Value{
			"app/str":  value(t, provider.KindString, compileSecret),
			"app/port": value(t, provider.KindInteger, 6443),
			"app/bool": value(t, provider.KindBoolean, true),
			"app/map":  value(t, provider.KindMapping, map[string]any{"key-one-7c": "first-" + compileSecret, "key-two-7c": compileSecret + "-second"}),
		})
	_, xs, _, err := ingest.Trace(frag.Text, frag.Values, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var ts []traced
	stand := map[string]string{}
	shape := func(real string, id int) string {
		b := []byte(real)
		for i, c := range b {
			switch {
			case c >= 'a' && c <= 'z':
				b[i] = 'x'
			case c >= '0' && c <= '9':
				b[i] = '0'
			}
		}
		return fmt.Sprintf("zq%03d", id) + string(b[5:])
	}
	for _, x := range xs {
		ts = append(ts, traced{x, 1})
		var name, s string
		switch {
		case x.Ref() == "app/str":
			name, s = "str", shape(compileSecret, x.ID())
		case x.Ref() == "app/port":
			name, s = "port", fmt.Sprint(61000+x.ID())
		case x.Ref() == "app/map" && x.Member() == 0:
			name, s = "map#0", shape("first-"+compileSecret, x.ID())
		case x.Ref() == "app/map" && x.Member() == 1:
			name, s = "map#1", shape(compileSecret+"-second", x.ID())
		default:
			continue
		}
		if !x.Carried(s) {
			t.Fatalf("control: the computed %s stand-in is not the tracer's", name)
		}
		stand[name] = s
	}
	if len(stand) != 4 {
		t.Fatalf("control: stand-ins %v", len(stand))
	}
	return ts, newRedactor([]Source{frag}), stand
}

// A renderer message is redacted by the template the same step gave on the trace pass: each
// stand-in quote marks a value quote, replaced by the reference's token; anything the template
// cannot explain, or explains in more than one way, is withheld behind a notice naming the step.
// A message from a step whose input holds a boolean reference is withheld unless it is empty: a
// boolean's stand-in is its own value, so no quote of it is marked. Exact copies of a resolved
// string of six bytes or more are then redacted as values, longest first (compilation.md §8.3).
func TestRedactMessage(t *testing.T) {
	ts, r, s := messageFixture(t)
	withheld := "<withheld: the composition message may quote a sensitive value>"
	for _, c := range []struct {
		name, real, trace string
		boolInput         bool
		want              string
		outcome           messageOutcome
	}{
		{"nothing printed", "", "", false, "", messageNone},
		{"no quote", "unknown field \"foo\"", "unknown field \"foo\"", false, "unknown field \"foo\"", messageVerbatim},
		{"string", "label \"" + compileSecret + "\" is too long", "label \"" + s["str"] + "\" is too long", false,
			"label \"<redacted:app/str@1>\" is too long", messageRedacted},
		{"integer", "port 6443 is out of range", "port " + s["port"] + " is out of range", false,
			"port <redacted:app/port@1> is out of range", messageRedacted},
		{"decode prefix", "cannot decode \"compile\" as int", "cannot decode \"" + s["str"][:7] + "\" as int", false,
			"cannot decode \"<redacted:app/str@1>\" as int", messageRedacted},
		{"mapping member", "annotation first-" + compileSecret + " and 6443", "annotation " + s["map#0"] + " and " + s["port"], false,
			"annotation <redacted:app/map@3#0> and <redacted:app/port@1>", messageRedacted},
		{"metacharacters", "bad (.*) [x] \"" + compileSecret + "\" $^+?{1}\\", "bad (.*) [x] \"" + s["str"] + "\" $^+?{1}\\", false,
			"bad (.*) [x] \"<redacted:app/str@1>\" $^+?{1}\\", messageRedacted},
		{"template mismatch", "label \"" + compileSecret + "\" is far too long", "label \"" + s["str"] + "\" is too long", false,
			withheld, messageWithheld},
		{"real pass only", "invalid value", "", false, withheld, messageWithheld},
		{"trace pass only", "", "invalid value", false, withheld, messageWithheld},
		{"verbatim text differs", "a b", "a c", false, withheld, messageWithheld},
		{"adjacent quotes", "v " + compileSecret + "6443", "v " + s["str"] + s["port"], false, withheld, messageWithheld},
		{"ambiguous split", "a x\" and \"y\" and \"z", "a " + s["str"] + "\" and \"" + s["map#1"], false, withheld, messageWithheld},
		{"boolean input, verbatim", "rbac must be true", "rbac must be true", true, withheld, messageWithheld},
		{"boolean input, redacted", "label \"" + compileSecret + "\" with true", "label \"" + s["str"] + "\" with true", true,
			withheld, messageWithheld},
		{"boolean input, nothing", "", "", true, "", messageNone},
		{"authored copy", "literal first-" + compileSecret + " and " + compileSecret + "-second", "literal first-" + compileSecret + " and " + compileSecret + "-second", false,
			"literal <redacted:value> and <redacted:value>", messageVerbatim},
		{"copy beside a quote", "label \"" + compileSecret + "\" near " + compileSecret, "label \"" + s["str"] + "\" near " + compileSecret, false,
			"label \"<redacted:app/str@1>\" near <redacted:value>", messageRedacted},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, outcome := redactMessage("composition", c.real, c.trace, ts, c.boolInput, r)
			if got != c.want || outcome != c.outcome {
				t.Errorf("redactMessage = %q, %s; want %q, %s", got, outcome, c.want, c.outcome)
			}
			if c.real != "" && strings.Contains(got, compileSecret) {
				t.Errorf("the message shows the value: %q", got)
			}
		})
	}
	t.Run("opaque values", func(t *testing.T) {
		o := r
		o.opaque = true
		if got, outcome := redactMessage("validation", "no quote", "no quote", ts, false, o); outcome != messageWithheld ||
			got != "<withheld: the validation message may quote a sensitive value>" {
			t.Errorf("opaque: %q, %s; want withheld", got, outcome)
		}
	})
}

// A native rejection or an invalid configuration carries the machinery's message as §8.3 shows
// it: redacted by the trace pass's message of the same step, or withheld when only the real pass
// failed or the step's input holds a boolean reference. No rendering of the error, and nothing it
// wraps, holds a value (compilation.md §8.3, §13).
func TestCompileMessages(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	str := func(text, name, v string) Source {
		return source(t, text, strRef(name), map[string]provider.Value{name: value(t, provider.KindString, v)})
	}
	withFlag := func(text string) Source {
		return source(t, text+"machine:\n  features:\n    rbac: !bwref flag\n", refs(map[string]ingest.Reference{"flag": ref(provider.KindBoolean)}),
			map[string]provider.Value{"flag": value(t, provider.KindBoolean, true)})
	}
	literal := source(t, "cluster:\n  network:\n    dnsDomain: not a domain\n", ingest.Declarations{}, nil)
	port := str("machine:\n  features:\n    kubePrism:\n      port: !bwref port\n", "port", compileSecret)
	flag := withFlag("")
	for _, c := range []struct {
		name  string
		frags []Source
		rule  Rule
		input string
		want  string // the message, or a part of it
	}{
		{"type mismatch", []Source{port}, RuleRejected, "fragment[0]", "cannot construct !!str `<redacted:port@1>...` into int"},
		{"invalid value", []Source{str("cluster:\n  network:\n    dnsDomain: !bwref domain\n", "domain", "compile not a domain 7a3c")},
			RuleInvalid, "", "\"<redacted:domain@1>\" is not a valid DNS name"},
		{"real pass only", []Source{str("cluster:\n  network:\n    dnsDomain: !bwref domain\n", "domain", "-bad.example")},
			RuleInvalid, "", withheldNotice("validation")},
		{"literal", []Source{literal}, RuleInvalid, "", "\"not a domain\" is not a valid DNS name"},
		{"literal beside a boolean", []Source{withFlag("cluster:\n  network:\n    dnsDomain: not a domain\n")}, RuleInvalid, "",
			withheldNotice("validation")},
		{"rejection, boolean after it", []Source{port, flag}, RuleRejected, "fragment[0]", "`<redacted:port@1>...` into int"},
		{"rejection, boolean before it", []Source{flag, port}, RuleRejected, "fragment[1]", withheldNotice("composition")},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Compile(Input{Base: base, Fragments: c.frags, Mode: ModeMetal})
			var e *Error
			if !errors.As(err, &e) || e.Rule != c.rule || e.Input != c.input {
				t.Fatalf("got %v, want a %s refusal at %q", err, c.rule, c.input)
			}
			if !strings.Contains(e.Message, c.want) || strings.HasPrefix(c.want, "<withheld") && e.Message != c.want {
				t.Errorf("message %q, want %q", e.Message, c.want)
			}
			if !strings.HasSuffix(err.Error(), ": "+e.Message) {
				t.Errorf("Error() %q does not end with the message", err.Error())
			}
			if errors.Unwrap(err) != nil {
				t.Errorf("the refusal wraps %v", errors.Unwrap(err))
			}
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
				for _, v := range []string{compileSecret, "compile not", "`compile", "-bad.example", "zq0"} {
					if s := fmt.Sprintf(verb, err); shows(s, v) {
						t.Errorf("%s renders %q: %s", verb, v, s)
					}
				}
			}
		})
	}
}

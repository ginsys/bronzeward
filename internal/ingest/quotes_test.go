package ingest

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// quoteTracers traces one occurrence of each kind, ids from 40: a two-line string, an integer, a
// boolean, a base64-encoded string and a string too short for the id.
func quoteTracers(t *testing.T) map[TraceKind]Tracer {
	t.Helper()
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n    i: !bwref app/int\n    b: !bwref app/bool\n" +
		"    e: !bwref app/enc\n    h: !bwref app/short\n"
	decl := Declarations{References: map[string]Reference{
		"app/str": str(provider.KindString), "app/int": str(provider.KindInteger),
		"app/bool": str(provider.KindBoolean), "app/short": str(provider.KindString),
		"app/enc": {Kind: provider.KindString, Version: 2, Encoding: "base64"},
	}}
	_, ts := traced(t, sanitizedOf(t, text, decl), map[string]provider.Value{
		"app/str":   value(t, provider.KindString, resolveSecret+"\nsecond-line-"+resolveSecret),
		"app/int":   value(t, provider.KindInteger, 6443),
		"app/bool":  value(t, provider.KindBoolean, true),
		"app/enc":   value(t, provider.KindString, resolveSecret),
		"app/short": value(t, provider.KindString, "abc"),
	}, 40, -1)
	out := map[TraceKind]Tracer{}
	for _, x := range ts {
		if x.Ref() == "app/short" {
			out["headless"] = x
			continue
		}
		out[x.Kind()] = x
	}
	return out
}

func span(msg, quote string, from int) [2]int {
	i := strings.Index(msg[from:], quote)
	if i < 0 {
		return [2]int{-1, -1}
	}
	return [2]int{from + i, from + i + len(quote)}
}

// A message quotes a stand-in whole, by a prefix of five bytes or more of it or of one of its
// lines (a decode error quotes seven bytes), an integer's bounded by non-digits, and a
// base64-encoded string's placed or raw form; a boolean, and a stand-in too short for its id,
// mark no quote (compilation.md §8.3; SP §6.2).
func TestTracerQuotes(t *testing.T) {
	ts := quoteTracers(t)
	whole := standIn(resolveSecret+"\nsecond-line-"+resolveSecret, 40)
	line2 := whole[strings.Index(whole, "\n")+1:]
	raw := standIn(resolveSecret, 43)
	placed := base64.StdEncoding.EncodeToString([]byte(raw))

	t.Run("string", func(t *testing.T) {
		msg := "bad value \"" + whole + "\"; cut \"" + whole[:7] + "\" and line \"" + line2 + "\"; head zq04 is short"
		a := span(msg, whole, 0)
		b := span(msg, whole[:7], a[1])
		c := span(msg, line2, b[1])
		if got, want := ts[TraceString].Quotes(msg), [][2]int{a, b, c}; !reflect.DeepEqual(got, want) {
			t.Errorf("Quotes = %v, want %v", got, want)
		}
	})
	t.Run("integer", func(t *testing.T) {
		msg := "port 61041 is invalid, 610411 and 161041 are not it, nor 61041x? yes: 61041"
		a := span(msg, "61041", 0)
		b := span(msg, "61041x", 0)
		c := span(msg, ": 61041", 0)
		want := [][2]int{a, {b[0], b[0] + 5}, {c[0] + 2, c[1]}}
		if got := ts[TraceInteger].Quotes(msg); !reflect.DeepEqual(got, want) {
			t.Errorf("Quotes = %v, want %v", got, want)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		msg := "cannot decode \"" + placed + "\" from \"" + raw + "\""
		a := span(msg, placed, 0)
		b := span(msg, raw, a[1])
		if got, want := ts[TraceBytes].Quotes(msg), [][2]int{a, b}; !reflect.DeepEqual(got, want) {
			t.Errorf("Quotes = %v, want %v", got, want)
		}
	})
	t.Run("overlapping forms", func(t *testing.T) {
		// A placed form whose tail is the raw form's head: the two quotes merge into one span,
		// so no byte of either is left outside a quote.
		tr := Tracer{&tracer{kind: TraceBytes, text: &standInText{value: "QUJDzq041ab", raw: "zq041abcdef"}}}
		msg := "x QUJDzq041abcdef y"
		if got, want := tr.Quotes(msg), [][2]int{{2, 17}}; !reflect.DeepEqual(got, want) {
			t.Errorf("Quotes = %v, want %v", got, want)
		}
	})
	t.Run("boolean and headless", func(t *testing.T) {
		msg := "true is not allowed here, nor xxx"
		if got := ts[TraceBoolean].Quotes(msg); got != nil {
			t.Errorf("boolean Quotes = %v, want none", got)
		}
		if got := ts["headless"].Quotes(msg); got != nil {
			t.Errorf("headless Quotes = %v, want none", got)
		}
		if got := (Tracer{}).Quotes(msg); got != nil {
			t.Errorf("zero Tracer Quotes = %v, want none", got)
		}
	})
}

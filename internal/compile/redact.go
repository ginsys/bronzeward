package compile

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// redactedToken stands for a path token that holds a resolved value (compilation.md §8.3).
const redactedToken = "<redacted>"

// redactor redacts the path tokens that hold a resolved value: one equal to a resolved string, a
// mapping member's key or string value, the base64 encoding of one or its canonical re-encoding,
// or containing one of copyFloor bytes or more. Integers and booleans are not looked for. It holds plaintext, so it
// lives only inside Compile and is never returned.
type redactor struct {
	exact    map[string]bool
	contains []string
	opaque   bool // a value did not decode, so every path is redacted whole
}

func newRedactor(sources []Source) redactor {
	r := redactor{exact: map[string]bool{}}
	add := func(s string) {
		fs := []string{s, base64.StdEncoding.EncodeToString([]byte(s))}
		if c, ok := canonical(s); ok {
			fs = append(fs, c)
		}
		for _, f := range fs {
			r.exact[f] = true
			if len(f) >= copyFloor {
				r.contains = append(r.contains, f)
			}
		}
	}
	for _, s := range sources {
		for _, v := range s.Values {
			x, err := v.Decode()
			if err != nil {
				r.opaque = true
				continue
			}
			switch x := x.(type) {
			case string:
				add(x)
			case map[string]any:
				for k, m := range x {
					add(k)
					if m, ok := m.(string); ok {
						add(m)
					}
				}
			}
		}
	}
	return r
}

// path is s with every token holding a resolved value replaced by redactedToken. The rendering is
// checked too, as ingestion's is: escaping can spell a value a token does not hold, and a value
// can span tokens, so such a path keeps its document only. A path that does not parse, or whose
// document alone holds a value, is redacted whole.
func (r redactor) path(s string) string {
	p, err := ingest.ParsePath(s)
	if r.opaque || err != nil {
		return redactedToken
	}
	p.Pointer, p.Inner = r.tokens(p.Pointer), r.tokens(p.Inner)
	if s := p.String(); !r.renders(s) {
		return s
	}
	if d := (ingest.Path{Doc: p.Doc}).String(); !r.renders(d) {
		return d
	}
	return redactedToken
}

func (r redactor) tokens(ts []string) []string {
	out := slices.Clone(ts)
	for i, t := range out {
		if r.holds(t) {
			out[i] = redactedToken
		}
	}
	return out
}

func (r redactor) holds(t string) bool {
	return r.exact[t] || slices.ContainsFunc(r.contains, func(f string) bool { return strings.Contains(t, f) })
}

// renders reports whether a rendered path holds a value, as written or unescaped: one of copyFloor
// bytes or more anywhere, or any value as a run of whole pieces between separators, as a token
// equal to it would be.
func (r redactor) renders(s string) bool {
	plain := strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
	for _, x := range []string{s, plain} {
		if r.holds(x) {
			return true
		}
		for f := range r.exact {
			if run(x, f) {
				return true
			}
		}
	}
	return false
}

// run reports whether f occurs in x bounded by x's ends or a path separator on both sides.
func run(x, f string) bool {
	if f == "" {
		return false
	}
	sep := func(s string, i int) bool { return s[i] == '/' || s[i] == '|' }
	for i := 0; ; {
		j := strings.Index(x[i:], f)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(f)
		if (start == 0 || sep(x, start-1)) && (end == len(x) || sep(x, end)) {
			return true
		}
		i = start + 1
	}
}

// paths redacts every path of a compile refusal: a rule's own or one ingest raised. An
// InputError directly around the refusal makes its message when asked, so it is kept. A wrapper
// built by fmt.Errorf holds its message as written, so a wrapped refusal is returned wrapped
// again, with the same prefix, around the redacted refusal; a message that does not end with the
// refusal's own is dropped for the refusal alone.
func (r redactor) paths(err error) error {
	var inner error
	var paths []string
	var e *Error
	var f *ingest.Refusal
	switch {
	case errors.As(err, &e):
		inner, paths = e, e.Paths
	case errors.As(err, &f):
		inner, paths = f, f.Paths
	default:
		return err
	}
	before, whole := inner.Error(), err.Error()
	for i, p := range paths {
		paths[i] = r.path(p)
	}
	if inner == err {
		return err
	}
	if w, ok := err.(*InputError); ok && w.Err == inner {
		return err
	}
	if prefix, ok := strings.CutSuffix(whole, before); ok {
		return fmt.Errorf("%s%w", prefix, inner)
	}
	return inner
}

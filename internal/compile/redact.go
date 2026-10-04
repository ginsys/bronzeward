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
// mapping member's key or string value, or the base64 encoding of one, or containing one of
// copyFloor bytes or more. Integers and booleans are not looked for. It holds plaintext, so it
// lives only inside Compile and is never returned.
type redactor struct {
	exact    map[string]bool
	contains []string
	opaque   bool // a value did not decode, so every path is redacted whole
}

func newRedactor(sources []Source) redactor {
	r := redactor{exact: map[string]bool{}}
	add := func(s string) {
		for _, f := range []string{s, base64.StdEncoding.EncodeToString([]byte(s))} {
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

// path is s with every token holding a resolved value replaced by redactedToken; a path that does
// not parse is redacted whole.
func (r redactor) path(s string) string {
	p, err := ingest.ParsePath(s)
	if r.opaque || err != nil {
		return redactedToken
	}
	p.Pointer, p.Inner = r.tokens(p.Pointer), r.tokens(p.Inner)
	return p.String()
}

func (r redactor) tokens(ts []string) []string {
	out := slices.Clone(ts)
	for i, t := range out {
		if r.exact[t] || slices.ContainsFunc(r.contains, func(f string) bool { return strings.Contains(t, f) }) {
			out[i] = redactedToken
		}
	}
	return out
}

// paths redacts every path of a compile refusal: a rule's own or one ingest raised. A wrapper
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
	if prefix, ok := strings.CutSuffix(whole, before); ok {
		return fmt.Errorf("%s%w", prefix, inner)
	}
	return inner
}

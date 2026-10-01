package ingest

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Path names one node of one document of a stream (compilation.md §2.2): doc[<n>] followed by
// an RFC 6901 JSON Pointer inside that document and, inside an identified embedded document,
// "|<format>" and a second pointer. Pointer and Inner hold unescaped tokens.
type Path struct {
	Doc     int
	Pointer []string
	Format  string // "", "yaml" or "json"
	Inner   []string
}

// maxDoc bounds a document index, so a path cannot name an absurd one.
const maxDoc = 1_000_000

// ParsePath parses a path; a malformed one is refused with rule bad-path, quoting nothing.
func ParsePath(s string) (Path, error) {
	bad := refuse(RuleBadPath)
	rest, ok := strings.CutPrefix(s, "doc[")
	if !ok {
		return Path{}, bad
	}
	num, rest, ok := strings.Cut(rest, "]")
	if !ok || !decimal(num) {
		return Path{}, bad
	}
	doc, err := strconv.Atoi(num)
	if err != nil || doc > maxDoc {
		return Path{}, bad
	}
	outer, inner, embedded := strings.Cut(rest, "|")
	p := Path{Doc: doc}
	if p.Pointer, ok = pointerTokens(outer); !ok {
		return Path{}, bad
	}
	if embedded {
		format, innerPointer, _ := strings.Cut(inner, "/")
		if format != "yaml" && format != "json" || len(p.Pointer) == 0 {
			return Path{}, bad
		}
		if inner != format {
			innerPointer = "/" + innerPointer
		} else {
			innerPointer = ""
		}
		p.Format = format
		if p.Inner, ok = pointerTokens(innerPointer); !ok {
			return Path{}, bad
		}
	}
	return p, nil
}

// decimal is a non-negative integer with no sign and no leading zero.
func decimal(s string) bool {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// pointerTokens splits an RFC 6901 pointer: "" is the whole document, and every token is
// unescaped (~1 is "/", ~0 is "~"; any other "~" is malformed). A "|" can only separate the
// embedded part, so a token holding one is refused.
func pointerTokens(s string) ([]string, bool) {
	if s == "" {
		return nil, true
	}
	if s[0] != '/' || strings.Contains(s, "|") {
		return nil, false
	}
	parts := strings.Split(s[1:], "/")
	for i, part := range parts {
		var b strings.Builder
		for j := 0; j < len(part); j++ {
			if part[j] != '~' {
				b.WriteByte(part[j])
				continue
			}
			if j+1 == len(part) {
				return nil, false
			}
			switch part[j+1] {
			case '0':
				b.WriteByte('~')
			case '1':
				b.WriteByte('/')
			default:
				return nil, false
			}
			j++
		}
		parts[i] = b.String()
	}
	return parts, true
}

func (p Path) String() string {
	var b strings.Builder
	b.WriteString("doc[")
	b.WriteString(strconv.Itoa(p.Doc))
	b.WriteString("]")
	writePointer(&b, p.Pointer)
	if p.Format != "" {
		b.WriteString("|")
		b.WriteString(p.Format)
		writePointer(&b, p.Inner)
	}
	return b.String()
}

var tokenEscaper = strings.NewReplacer("~", "~0", "/", "~1")

func writePointer(b *strings.Builder, tokens []string) {
	for _, t := range tokens {
		b.WriteString("/")
		b.WriteString(tokenEscaper.Replace(t))
	}
}

// resolve finds the node a pointer names in document doc, following aliases. A mapping is
// indexed by its scalar keys, a sequence by a decimal index; nothing else has children.
func resolve(docs []*yaml.Node, doc int, pointer []string) (*yaml.Node, bool) {
	if doc < 0 || doc >= len(docs) {
		return nil, false
	}
	return resolveIn(root(docs[doc]), pointer)
}

func resolveIn(n *yaml.Node, pointer []string) (*yaml.Node, bool) {
	for _, tok := range pointer {
		n = deref(n)
		if n == nil {
			return nil, false
		}
		switch n.Kind {
		case yaml.MappingNode:
			child := mappingValue(n, tok)
			if child == nil {
				return nil, false
			}
			n = child
		case yaml.SequenceNode:
			if !decimal(tok) {
				return nil, false
			}
			i, err := strconv.Atoi(tok)
			if err != nil || i >= len(n.Content) {
				return nil, false
			}
			n = n.Content[i]
		default:
			return nil, false
		}
	}
	n = deref(n)
	return n, n != nil
}

// root is a document's top node.
func root(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	return doc
}

// deref follows an alias to its anchored node.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// mappingValue is the value of the first scalar key equal to key.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := deref(m.Content[i]); k != nil && k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

package main

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Step is one element of a path into a machine configuration document: a mapping key, a
// sequence element picked by an identifying field or by position, or the point where a string
// value is parsed as an embedded YAML or JSON document.
type Step struct {
	Key      string // mapping key
	SelKey   string // sequence element whose mapping has SelKey: SelVal
	SelVal   string
	Index    int // sequence element by position, when IsIndex
	IsIndex  bool
	Embedded string // "yaml" or "json": the string value here, parsed
}

// Path is written as steps joined by '/': `key`, `key[field=value]`, `key[3]`, and `key|yaml` for
// a string value parsed as an embedded document. A selector value may hold '/'.
type Path []Step

// identifying are the fields that name a sequence element in the documents this experiment
// touches (inlineManifests and files by name and path, interfaces by interface).
var identifying = []string{"name", "interface", "path"}

func (p Path) String() string {
	var b strings.Builder
	for i, s := range p {
		switch {
		case s.Embedded != "":
			b.WriteString("|" + s.Embedded)
			continue
		case s.IsIndex:
			fmt.Fprintf(&b, "[%d]", s.Index)
			continue
		case s.SelKey != "":
			fmt.Fprintf(&b, "[%s=%s]", s.SelKey, s.SelVal)
			continue
		}
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(s.Key)
	}
	return b.String()
}

func (p Path) with(s Step) Path {
	q := make(Path, len(p), len(p)+1)
	copy(q, p)
	return append(q, s)
}

// ParsePath reads the String form back.
func ParsePath(text string) (Path, error) {
	var p Path
	i := 0
	for i < len(text) {
		switch text[i] {
		case '/':
			i++
		case '[':
			end := strings.IndexByte(text[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("path %q: unclosed [", text)
			}
			sel := text[i+1 : i+end]
			i += end + 1
			if k, v, ok := strings.Cut(sel, "="); ok {
				p = append(p, Step{SelKey: k, SelVal: v})
			} else if n, err := strconv.Atoi(sel); err == nil {
				p = append(p, Step{Index: n, IsIndex: true})
			} else {
				return nil, fmt.Errorf("path %q: selector %q is neither field=value nor an index", text, sel)
			}
		case '|':
			j := i + 1
			for j < len(text) && text[j] != '/' && text[j] != '[' && text[j] != '|' {
				j++
			}
			f := text[i+1 : j]
			if f != "yaml" && f != "json" {
				return nil, fmt.Errorf("path %q: embedded format %q is neither yaml nor json", text, f)
			}
			p = append(p, Step{Embedded: f})
			i = j
		default:
			j := i
			for j < len(text) && text[j] != '/' && text[j] != '[' && text[j] != '|' {
				j++
			}
			p = append(p, Step{Key: text[i:j]})
			i = j
		}
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	return p, nil
}

// elementStep names a sequence element: by its first identifying field, else by position.
func elementStep(item *yaml.Node, index int) Step {
	if item.Kind == yaml.MappingNode {
		for _, f := range identifying {
			for i := 0; i+1 < len(item.Content); i += 2 {
				if item.Content[i].Value == f && item.Content[i+1].Kind == yaml.ScalarNode {
					return Step{SelKey: f, SelVal: item.Content[i+1].Value}
				}
			}
		}
	}
	return Step{Index: index, IsIndex: true}
}

// docID names a document of a multi-document machine configuration: `v1alpha1` for the legacy
// document, `<kind>/<name>` (or `<kind>`) for the others.
func docID(doc *yaml.Node) string {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	var kind, name string
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			switch root.Content[i].Value {
			case "kind":
				kind = root.Content[i+1].Value
			case "name":
				name = root.Content[i+1].Value
			}
		}
	}
	switch {
	case kind == "":
		return "v1alpha1"
	case name == "":
		return kind
	}
	return kind + "/" + name
}

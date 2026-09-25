package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// paths prints the key paths in which two configurations differ, never their values:
// `- p` only in the first, `+ p` only in the second, `~ p` in both with a different value. The
// generated configurations hold the fixture's synthetic secrets, and this is how the evidence
// records what a contract changes without committing them.
func paths(args []string) error {
	if len(args) != 2 {
		return errors.New("paths needs <a.yaml> <b.yaml>")
	}
	a, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	b, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	lines, err := pathDiff(a, b)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Printf("result=paths differing=%d\n", len(lines))
	return nil
}

type diffLine struct{ sign, path string }

// pathDiff compares two multi-document YAML files. A document is named by its `kind` (and its
// `name`, if it has one), or by its position when it has no kind, as the v1alpha1 document has
// none.
func pathDiff(a, b []byte) ([]string, error) {
	da, err := documents(a)
	if err != nil {
		return nil, err
	}
	db, err := documents(b)
	if err != nil {
		return nil, err
	}
	var out []diffLine
	for name, n := range da {
		if m, ok := db[name]; ok {
			compare(name, n, m, &out)
		} else {
			out = append(out, diffLine{"-", name})
		}
	}
	for name := range db {
		if _, ok := da[name]; !ok {
			out = append(out, diffLine{"+", name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	lines := make([]string, len(out))
	for i, l := range out {
		lines[i] = l.sign + " " + l.path
	}
	return lines, nil
}

func documents(data []byte) (map[string]*yaml.Node, error) {
	docs := map[string]*yaml.Node{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for i := 0; ; i++ {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		if len(doc.Content) == 0 {
			continue
		}
		n := doc.Content[0]
		name := strconv.Itoa(i)
		if kind := value(n, "kind"); kind != "" {
			name = kind
			if v := value(n, "name"); v != "" {
				name += "/" + v
			}
		}
		if _, dup := docs[name]; dup {
			return nil, fmt.Errorf("two documents named %s", name)
		}
		docs[name] = n
	}
}

func value(n *yaml.Node, key string) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key && n.Content[i+1].Kind == yaml.ScalarNode {
			return n.Content[i+1].Value
		}
	}
	return ""
}

func compare(path string, a, b *yaml.Node, out *[]diffLine) {
	switch {
	case a.Kind == yaml.MappingNode && b.Kind == yaml.MappingNode:
		ka, kb := children(a), children(b)
		for k, va := range ka {
			if vb, ok := kb[k]; ok {
				compare(path+"/"+k, va, vb, out)
			} else {
				*out = append(*out, diffLine{"-", path + "/" + k})
			}
		}
		for k := range kb {
			if _, ok := ka[k]; !ok {
				*out = append(*out, diffLine{"+", path + "/" + k})
			}
		}
	case a.Kind == yaml.SequenceNode && b.Kind == yaml.SequenceNode:
		for i := 0; i < len(a.Content) || i < len(b.Content); i++ {
			p := path + "/" + strconv.Itoa(i)
			switch {
			case i >= len(b.Content):
				*out = append(*out, diffLine{"-", p})
			case i >= len(a.Content):
				*out = append(*out, diffLine{"+", p})
			default:
				compare(p, a.Content[i], b.Content[i], out)
			}
		}
	case a.Kind == yaml.ScalarNode && b.Kind == yaml.ScalarNode:
		if a.Value != b.Value || a.ShortTag() != b.ShortTag() {
			*out = append(*out, diffLine{"~", path})
		}
	default:
		*out = append(*out, diffLine{"~", path})
	}
}

func children(n *yaml.Node) map[string]*yaml.Node {
	m := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		m[n.Content[i].Value] = n.Content[i+1]
	}
	return m
}

package ingest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// parse decodes every document of the input's stream, keeping tags (compilation.md §2.3 step 2).
// A failure refuses the input with at most a line number: the decoder's messages can quote the
// offending text.
func parse(u Unresolved) ([]*yaml.Node, error) {
	b := u.bytes()
	if len(b) == 0 {
		return nil, ErrEmptyInput
	}
	return parseStream(b)
}

func parseStream(b []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, parseRefusal(err)
		}
		docs = append(docs, &n)
	}
	if len(docs) == 0 {
		return nil, refuse(RuleParse, "the stream holds no document")
	}
	return docs, nil
}

var yamlLine = regexp.MustCompile(`\bline ([0-9]+)\b`)

func parseRefusal(err error) error {
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		return refuse(RuleParse, fmt.Sprintf("line %s", m[1]))
	}
	return refuse(RuleParse)
}

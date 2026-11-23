package ingest

import (
	"errors"

	"go.yaml.in/yaml/v3"
)

// Filled records compilation.md §2.3 step 2a for one machine: the stored stream no longer holds the
// HostnameConfig document whose hostname equals Name, the machine's name when the request read it, and
// Equal says the removed document was exactly what the fill writes for that name (apiVersion v1alpha1,
// kind, `auto: off` and the hostname, nothing else; comments and key order do not count). A bare
// `hostname` with no `auto` or with `auto: null`, or any other key, records Equal false. The zero value is "nothing removed".
type Filled struct {
	Name  string
	Equal bool
}

// Filled is what step 2a removed from this candidate's stream.
func (c *Candidate) Filled() Filled { return c.filled }

// filledHostname finds the HostnameConfig document of docs whose hostname equals name exactly, and
// reports its position and record, or -1 and the zero record. A machine with no name, a stream with no
// such document, a hostname that is a reference or differs in case or in form, and a stream that the
// removal would leave empty are all left as they are. The stream is not changed: the caller removes the
// document after identification, so that every position stays that of the input.
func filledHostname(docs []*yaml.Node, name string) (int, Filled) {
	if name == "" || len(docs) < 2 {
		return -1, Filled{}
	}
	for i, d := range docs {
		members, ok := plainMembers(root(d))
		if !ok || members["kind"] != "HostnameConfig" {
			continue
		}
		if h, ok := members["hostname"]; !ok || h != name {
			continue
		}
		equal := len(members) == 4 && members["apiVersion"] == "v1alpha1" && members["auto"] == "off"
		return i, Filled{Name: name, Equal: equal}
	}
	return -1, Filled{}
}

// Step 2a leaves the stream one document shorter from the document at dropAt on. Marks and every
// refusal keep the positions of the input; what the stream's checks and what is stored read are the
// stream's own. toStored and toInput translate a document path between the two.

// toStored is the path of an input document in the shortened stream; ok is false for the removed one.
func toStored(s string, dropAt int) (string, bool) {
	p, err := ParsePath(s)
	switch {
	case err != nil || p.Doc < dropAt:
		return s, true
	case p.Doc == dropAt:
		return "", false
	}
	p.Doc--
	return p.String(), true
}

// toInput is the path of a document of the shortened stream, as the input numbers it.
func toInput(s string, dropAt int) string {
	p, err := ParsePath(s)
	if err != nil || p.Doc < dropAt {
		return s
	}
	p.Doc++
	return p.String()
}

// shiftEmbedded renumbers the embedded declarations for the shortened stream, dropping one inside the
// removed document; the second result is the same set by path, for the guard.
func shiftEmbedded(list []Embedded, dropAt int) ([]Embedded, map[string]string) {
	var out []Embedded
	declared := map[string]string{}
	for _, e := range list {
		path, ok := toStored(e.Path, dropAt)
		if !ok {
			continue
		}
		e.Path = path
		out = append(out, e)
		if p, err := ParsePath(path); err == nil {
			declared[p.String()] = e.Format
		}
	}
	return out, declared
}

// inInput gives a refusal of the shortened stream the input's positions.
func inInput(err error, dropAt int) error {
	var r *Refusal
	if dropAt >= 0 && errors.As(err, &r) {
		for i, s := range r.Paths {
			r.Paths[i] = toInput(s, dropAt)
		}
	}
	return err
}

// storedHolders is removed, the holders of embedded documents, for the shortened stream.
func storedHolders(removed [][]string, dropAt int) [][]string {
	out := make([][]string, len(removed))
	for k, hs := range removed {
		for _, h := range hs {
			if s, ok := toStored(h, dropAt); ok {
				out[k] = append(out[k], s)
			}
		}
	}
	return out
}

// plainMembers is the members of a mapping node as text, and whether every value is a plain string
// scalar: a reference, a collection or another type makes a document no candidate for the removal.
func plainMembers(n *yaml.Node) (map[string]string, bool) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, false
	}
	out := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		// `auto: null` leaves Talos's pointer unset, the same as no `auto`: the document is still the
		// machine's own hostname, and it is never exactly the fill.
		if k.Kind == yaml.ScalarNode && k.Value == "auto" && v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue
		}
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return nil, false
		}
		out[k.Value] = v.Value
	}
	return out, true
}

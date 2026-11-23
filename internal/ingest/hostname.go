package ingest

import "go.yaml.in/yaml/v3"

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

// shiftEmbedded renumbers the embedded declarations after step 2a removed the document at dropAt: the
// stored stream has one document fewer, so a declaration of a later document names it one position lower,
// and one inside the removed document is dropped with it. The second result is the same set by
// normalised path, for the guard. Marks and every refusal keep the positions of the input; only what is stored moves.
func shiftEmbedded(list []Embedded, dropAt int) ([]Embedded, map[string]string) {
	var out []Embedded
	shifted := map[string]string{}
	for _, e := range list {
		p, err := ParsePath(e.Path)
		if err != nil || p.Doc == dropAt {
			continue
		}
		if p.Doc > dropAt {
			p.Doc--
			e.Path = p.String()
		}
		out = append(out, e)
		shifted[p.String()] = e.Format
	}
	return out, shifted
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

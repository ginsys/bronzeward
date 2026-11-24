package ingest

import (
	"errors"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Filled records compilation.md §2.3 step 2a for one machine: the stored stream no longer holds the
// HostnameConfig document whose hostname equals Name, the machine's name when the request read it, and
// Equal says the removed document was exactly what the fill writes for that name (apiVersion v1alpha1,
// kind, `auto: off` and the hostname, nothing else; comments and key order do not count). A bare
// `hostname` with no `auto` or with `auto: null`, or any other key, records Equal false. The zero value is "nothing removed".
// Key says the removed thing was a legacy machine.network.hostname key, not a document (releaseLegacyKey): Equal is then
// always false, since the node holds a v1alpha1 key and the release sends a document.
type Filled struct {
	Name  string
	Equal bool
	Key   bool
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

// legacyHost is the one v1alpha1 document of a stream that holds machine.network.hostname, with the nodes the
// stable-hostname guard of step 2a reads. features and stable are nil where the stream has none.
type legacyHost struct {
	root, machine, network, features, stable, key *yaml.Node
	at                                            int // the index of the key in network.Content
}

// holdsKind reports whether a document of the stream is of this kind, whatever else it holds.
func holdsKind(docs []*yaml.Node, kind string) bool {
	for _, d := range docs {
		if r := root(d); r != nil && r.Kind == yaml.MappingNode {
			// The machinery resolves an alias at kind, so this does too.
			if k := deref(mappingValue(r, "kind")); k != nil && k.Kind == yaml.ScalarNode && k.Value == kind {
				return true
			}
		}
	}
	return false
}

// mapping is the mapping member of n named key: nil and absent where n has none, and present false where the
// member is anything but a mapping (a reference, an alias, a null, a scalar).
func mapping(n *yaml.Node, key string) (_ *yaml.Node, present bool) {
	v := mappingValue(n, key)
	if v == nil {
		return nil, false
	}
	if v.Kind != yaml.MappingNode {
		return nil, true
	}
	return v, true
}

// machineDocuments is the documents of docs that hold a machine mapping, as legacyHost records, with the key
// and the features nodes where they exist. A member that is not a mapping on the way is no document of it.
func machineDocuments(docs []*yaml.Node) []legacyHost {
	var out []legacyHost
	for _, d := range docs {
		r := root(d)
		if r == nil || r.Kind != yaml.MappingNode {
			continue
		}
		m, _ := mapping(r, "machine")
		if m == nil {
			continue
		}
		h := legacyHost{root: r, machine: m}
		if f, _ := mapping(m, "features"); f != nil {
			h.features, h.stable = f, mappingValue(f, "stableHostname")
		}
		out = append(out, h)
	}
	return out
}

// releaseLegacyKey is compilation.md §2.3 step 2a for a legacy hostname: where the stream holds no
// HostnameConfig document (Talos refuses both) and the stable-hostname guard holds, a machine.network.hostname
// that is a plain string equal to name exactly is removed from its document, the key alone: its parent mapping
// stays, even empty. The guard: machine.features is absent or a mapping, and machine.features.stableHostname is
// absent or the plain boolean false. The key stays, with nothing recorded, where it differs in case or form,
// where the machine has no name, where it is not a plain string or carries an anchor, where more than one
// document holds one, and where a mark of step 3 targets a mapping that holds stableHostname or the key (the
// document, machine, machine.features, machine.network) or stableHostname itself: a reference there would make
// step 5a suppress the fill and lose the hostname. A mark that targets the key refuses the input as a mark that
// addresses no node does. No document is removed, so no position changes.
func releaseLegacyKey(docs []*yaml.Node, name string, marks []Path) (Filled, error) {
	if name == "" || holdsKind(docs, "HostnameConfig") {
		return Filled{}, nil
	}
	var found *legacyHost
	for _, h := range machineDocuments(docs) {
		net, present := mapping(h.machine, "network")
		if !present || net == nil {
			continue
		}
		for i := 0; i+1 < len(net.Content); i += 2 {
			if k := net.Content[i]; k.Kind == yaml.ScalarNode && k.Value == "hostname" {
				if found != nil {
					return Filled{}, nil
				}
				h.network, h.key, h.at = net, net.Content[i+1], i
				found = &h
			}
		}
	}
	if found == nil {
		return Filled{}, nil
	}
	if k := found.key; k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Anchor != "" || k.Value != name {
		return Filled{}, nil
	}
	// An anchor on the key node itself: an alias after it would read another node once the pair is gone.
	if found.network.Content[found.at].Anchor != "" {
		return Filled{}, nil
	}
	// A mapping an alias can reach is shared: deleting the key would change what the alias reads as well.
	if found.root.Anchor != "" || found.machine.Anchor != "" || found.network.Anchor != "" {
		return Filled{}, nil
	}
	if f := mappingValue(found.machine, "features"); f != nil && found.features == nil {
		return Filled{}, nil
	}
	if s := found.stable; s != nil && (s.Kind != yaml.ScalarNode || s.Tag != "!!bool" || s.Value != "false") {
		return Filled{}, nil
	}
	holders := map[*yaml.Node]bool{found.root: true, found.machine: true, found.network: true, found.features: true, found.stable: true}
	keep := false
	for _, m := range marks {
		n, ok := resolve(docs, m.Doc, m.Pointer)
		switch {
		case !ok:
		case n == found.key:
			return Filled{}, refuseAt(RuleMarkUnaddressed, []Path{m}, m.String())
		case holders[n]:
			keep = true
		}
	}
	if keep {
		return Filled{}, nil
	}
	found.network.Content = slices.Delete(found.network.Content, found.at, found.at+2)
	return Filled{Name: name, Key: true}, nil
}

// marksReleasedHostname is the first of marks that targets stableHostname or a mapping that holds it in a
// document of docs, where step 2a removed a legacy key from that document's stream (Filled.Key): compilation.md
// §3.6 item 3. The key is gone from the staged stream, so this reads the guard's own nodes, which every
// machine document has.
func marksReleasedHostname(docs []*yaml.Node, marks []Path) (Path, bool) {
	holders := map[*yaml.Node]bool{}
	for _, h := range machineDocuments(docs) {
		holders[h.root], holders[h.machine] = true, true
		if h.features != nil {
			holders[h.features] = true
		}
		if h.stable != nil {
			holders[h.stable] = true
		}
	}
	for _, m := range marks {
		if n, ok := resolve(docs, m.Doc, m.Pointer); ok && m.Format == "" && holders[n] {
			return m, true
		}
	}
	return Path{}, false
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

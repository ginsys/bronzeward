//go:build ignore

// measure.go: measurements for the import-analysis note (ginsys/bronzeward#861). Synthetic
// inputs only: the machine configurations are written by gen.sh with the pinned talosctl.
//
//	go run docs/design/research/evidence/20261118-import-equivalence/measure.go baseline <machines dir>
//
// Every secret value stays in memory; only counts and booleans are printed.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

type machine struct {
	name string
	node []byte // the configuration as talosctl wrote it (stands in for the bytes read from a node)
}

func main() {
	if len(os.Args) != 3 {
		fatal(errors.New("usage: measure.go baseline <machines dir>"))
	}
	ms := load(os.Args[2])
	switch os.Args[1] {
	case "baseline":
		baseline(ms)
	case "shape":
		shape(ms)
	case "split":
		splitCases(ms)
	case "pairs":
		pairs(ms)
	case "placeholder":
		// the redacted form carries no secret: print the lines that show what a reference becomes
		b, _, _ := importBase("ph", ms[3].node)
		_, red, err := render(compile.Input{Base: b, Mode: compile.ModeMetal})
		if err != nil {
			fatal(err)
		}
		for _, l := range strings.Split(red, "\n") {
			if strings.Contains(l, "token") || strings.Contains(l, "clusterSecret") {
				fmt.Println(l)
			}
		}
	default:
		fatal(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "measure:", err)
	os.Exit(1)
}

func must[T any](v T, err error) T {
	if err != nil {
		fatal(err)
	}
	return v
}

func load(dir string) []machine {
	files := must(filepath.Glob(filepath.Join(dir, "*.yaml")))
	slices.Sort(files)
	var out []machine
	for _, f := range files {
		out = append(out, machine{name: strings.TrimSuffix(filepath.Base(f), ".yaml"), node: must(os.ReadFile(f))})
	}
	return out
}

// importBase is what ingestion makes of a node's configuration: the sanitized document and the
// extracted values, held in memory instead of a provider (compilation.md §2.3).
func importBase(revision string, text []byte) (compile.Source, ingest.Sanitized, map[string]provider.Value) {
	u := must(ingest.Read(bytes.NewReader(text), 1<<20))
	c := must(ingest.Extract(ingest.Request{Input: u}))
	values := map[string]provider.Value{}
	s := must(c.Commit(context.Background(), func(_ context.Context, name string, v provider.Value) error {
		values[name] = v
		return nil
	}))
	sum := sha256.Sum256(text)
	return compile.Source{Revision: revision, Digest: hex.EncodeToString(sum[:]), Text: s, Values: values}, s, values
}

// render is the compiler's output for one machine: its digest and its redacted form.
func render(in compile.Input) (digest [32]byte, redacted string, err error) {
	c, err := compile.Compile(in)
	if err != nil {
		return digest, "", fmt.Errorf("compile: %w", err)
	}
	digest, err = c.Materialized().Digest()
	if err != nil {
		return digest, "", err
	}
	redacted, err = c.Redacted()
	if err != nil {
		redacted = "<redacted form refused>"
	}
	return digest, redacted, nil
}

// plainCompose is the compiler's composition on plaintext: the fragments as strategic merge
// patches in order, then the machinery's encoding without comments (compile.compose).
func plainCompose(base []byte, frags [][]byte) ([]byte, error) {
	var patches []configpatcher.Patch
	for _, f := range frags {
		p, err := configpatcher.LoadPatch(f)
		if err != nil {
			return nil, err
		}
		patches = append(patches, p)
	}
	o, err := configpatcher.Apply(configpatcher.WithBytes(base), patches)
	if err != nil {
		return nil, err
	}
	cfg, err := o.Config()
	if err != nil {
		return nil, err
	}
	return cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
}

// The equivalence levels (note, section "The equivalence definition").

// e0: the configuration digest is equal (the system's own comparison, choice §10.2).
func e0(a, b []byte) bool { return talos.ConfigurationDigest(a) == talos.ConfigurationDigest(b) }

// e1: equal after the same encoder: both through the machinery's load and encode.
func e1(a, b []byte) bool {
	x, err1 := plainCompose(a, nil)
	y, err2 := plainCompose(b, nil)
	return err1 == nil && err2 == nil && e0(x, y)
}

// e2: equal parsed trees, the documents matched by kind and name: comments, key order, formatting
// and document order do not count; list order does.
func e2(a, b []byte) bool {
	x, err1 := parseKeyed(a)
	y, err2 := parseKeyed(b)
	return err1 == nil && err2 == nil && reflect.DeepEqual(x, y)
}

func parseKeyed(b []byte) (map[string]any, error) {
	out := map[string]any{}
	for _, d := range parseDocs(b) {
		var v any
		if err := d.root.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := out[d.key]; dup {
			return nil, fmt.Errorf("two documents %s", d.key)
		}
		out[d.key] = v
	}
	return out, nil
}

// sameOrder: the documents come in the same order.
func sameOrder(a, b []byte) bool {
	keys := func(x []byte) []string {
		var k []string
		for _, d := range parseDocs(x) {
			k = append(k, d.key)
		}
		return k
	}
	return slices.Equal(keys(a), keys(b))
}

// e3: equal typed documents, decoded by the pinned machinery and matched by kind and name.
func e3(a, b []byte) bool {
	x, err1 := typed(a)
	y, err2 := typed(b)
	return err1 == nil && err2 == nil && reflect.DeepEqual(x, y)
}

func typed(b []byte) (map[string]any, error) {
	cfg, err := configloader.NewFromBytes(b)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, d := range cfg.Documents() {
		key := d.Kind()
		if n, ok := d.(interface{ Name() string }); ok {
			key += "/" + n.Name()
		}
		out[key] = d
	}
	return out, nil
}

// ---- shape: comparing the import bases of a cluster, and a proposed split ----

type doc struct {
	key  string // kind/name, or "main" for the legacy v1alpha1 document
	root *yaml.Node
}

func parseDocs(b []byte) []*doc {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var out []*doc
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			fatal(err)
		}
		out = append(out, &doc{key: docKey(n.Content[0]), root: n.Content[0]})
	}
}

func field(root *yaml.Node, k string) string {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == k {
			return root.Content[i+1].Value
		}
	}
	return ""
}

func docKey(root *yaml.Node) string {
	k := field(root, "kind")
	if k == "" {
		return "main"
	}
	return k + "/" + field(root, "name")
}

func identity(path []string) bool {
	return len(path) == 1 && (path[0] == "apiVersion" || path[0] == "kind" || path[0] == "name" || path[0] == "version")
}

// flatten calls fn on every leaf of d: a scalar, a list (atomic), or an empty mapping.
func flatten(d *doc, fn func(path []string, v *yaml.Node)) {
	var walk func(n *yaml.Node, path []string)
	walk = func(n *yaml.Node, path []string) {
		if n.Kind == yaml.MappingNode && len(n.Content) > 0 {
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], append(slices.Clone(path), n.Content[i].Value))
			}
			return
		}
		if !identity(path) {
			fn(path, n)
		}
	}
	walk(d.root, nil)
}

func sig(n *yaml.Node) string {
	b, err := yaml.Marshal(n)
	if err != nil {
		fatal(err)
	}
	return string(b)
}

func lid(d string, path []string) string { return d + "\x00" + strings.Join(path, "\x00") }

func show(id string) string { return strings.ReplaceAll(id, "\x00", " > ") }

type minfo struct {
	name, role string
	san        ingest.Sanitized
	vals       map[string]provider.Value
	docs       []*doc
	plain      []*doc
	leaves     map[string]string // leaf id -> signature, over the sanitized documents
	nodes      map[string]*yaml.Node
	order      []string // leaf ids in document order
}

func roleOf(name string) string {
	if strings.HasPrefix(name, "cp") || name == "controlplane" {
		return "controlplane"
	}
	return "worker"
}

func infos(ms []machine) []*minfo {
	var out []*minfo
	for _, m := range ms {
		_, san, vals := importBase("base-"+m.name, m.node)
		mi := &minfo{name: m.name, role: roleOf(m.name), san: san, vals: vals, docs: parseDocs(san.Documents()), plain: parseDocs(m.node),
			leaves: map[string]string{}, nodes: map[string]*yaml.Node{}}
		for _, d := range mi.docs {
			flatten(d, func(p []string, v *yaml.Node) {
				id := lid(d.key, p)
				mi.leaves[id] = sig(v)
				mi.nodes[id] = v
				mi.order = append(mi.order, id)
			})
		}
		out = append(out, mi)
	}
	return out
}

const (
	clsCommon  = "common"
	clsRole    = "role"
	clsMachine = "machine"
)

// classify gives each (machine, leaf) its class over the sanitized documents.
func classify(mis []*minfo) map[string]map[string]string { // machine -> leaf id -> class
	out := map[string]map[string]string{}
	byRole := map[string][]*minfo{}
	for _, m := range mis {
		byRole[m.role] = append(byRole[m.role], m)
		out[m.name] = map[string]string{}
	}
	same := func(group []*minfo, id string) bool {
		want, ok := group[0].leaves[id]
		if !ok {
			return false
		}
		for _, g := range group[1:] {
			if got, ok := g.leaves[id]; !ok || got != want {
				return false
			}
		}
		return true
	}
	for _, m := range mis {
		for _, id := range m.order {
			switch {
			case same(mis, id):
				out[m.name][id] = clsCommon
			case len(byRole[m.role]) > 1 && same(byRole[m.role], id):
				out[m.name][id] = clsRole
			default:
				out[m.name][id] = clsMachine
			}
		}
	}
	return out
}

// demote applies rule R2 to a classification: a mapping whose direct leaves fall in different
// classes stays in the base whole, because a patch does not always merge a mapping field by
// field (the first attempt, without this rule, was refused by the compiler).
func demote(mis []*minfo, cl map[string]map[string]string) {
	for _, m := range mis {
		group := map[string][]string{}
		for _, id := range m.order {
			key, path := splitID(id)
			parent := key + "\x00" + strings.Join(path[:len(path)-1], "\x00")
			group[parent] = append(group[parent], id)
		}
		for _, ids := range group {
			classes := map[string]bool{}
			for _, id := range ids {
				classes[cl[m.name][id]] = true
			}
			if len(classes) > 1 {
				for _, id := range ids {
					cl[m.name][id] = clsMachine
				}
			}
		}
	}
}

func isRef(n *yaml.Node) bool { return strings.Contains(n.Tag, "bwref") }

func shape(ms []machine) {
	mis := infos(ms)
	cl := classify(mis)
	fmt.Println("== M2: leaves by class, over the sanitized documents (a list is one leaf)")
	fmt.Println("machine  role          leaves common role machine  of which references")
	for _, m := range mis {
		n := map[string]int{}
		refs := map[string]int{}
		for _, id := range m.order {
			c := cl[m.name][id]
			n[c]++
			if isRef(m.nodes[id]) {
				refs[c]++
			}
		}
		fmt.Printf("%-7s  %-12s %7d %6d %4d %7d  common=%d role=%d machine=%d\n", m.name, m.role, len(m.order), n[clsCommon], n[clsRole], n[clsMachine],
			refs[clsCommon], refs[clsRole], refs[clsMachine])
	}
	// distinct leaves of the proposal: common once, role once per role, machine leaves once each
	seen := map[string]bool{}
	total, proposal := 0, 0
	for _, m := range mis {
		for _, id := range m.order {
			total++
			c := cl[m.name][id]
			key := c + "|" + id
			if c == clsRole {
				key = c + "|" + m.role + "|" + id
			}
			if c == clsMachine {
				key = c + "|" + m.name + "|" + id
			}
			if !seen[key] {
				seen[key] = true
				proposal++
			}
		}
	}
	fmt.Printf("leaves over all machines %d; in the proposal (common once, role once per role, machine leaves each) %d\n", total, proposal)
	fmt.Println("== machine-specific leaves that are not references")
	for _, m := range mis {
		var ls []string
		for _, id := range m.order {
			if cl[m.name][id] == clsMachine && !isRef(m.nodes[id]) {
				ls = append(ls, show(id))
			}
		}
		fmt.Printf("%s: %d: %s\n", m.name, len(ls), strings.Join(ls, "; "))
	}
}

// ---- split: the proposal built from the classes, and its equivalence at every level ----

func clone(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, x := range n.Content {
		c.Content[i] = clone(x)
	}
	return &c
}

func mapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
func str(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// delLeaf removes the leaf at path and prunes the mappings it empties.
func delLeaf(root *yaml.Node, path []string) {
	var rec func(n *yaml.Node, p []string) bool
	rec = func(n *yaml.Node, p []string) bool {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value != p[0] {
				continue
			}
			if len(p) == 1 || rec(n.Content[i+1], p[1:]) {
				n.Content = slices.Delete(n.Content, i, i+2)
			}
			break
		}
		return len(n.Content) == 0
	}
	rec(root, path)
}

func setLeaf(root *yaml.Node, path []string, val *yaml.Node) {
	n := root
	for _, k := range path[:len(path)-1] {
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				next = n.Content[i+1]
			}
		}
		if next == nil {
			next = mapping()
			n.Content = append(n.Content, str(k), next)
		}
		n = next
	}
	n.Content = append(n.Content, str(path[len(path)-1]), clone(val))
}

func findLeaf(docs []*doc, key string, path []string) *yaml.Node {
	for _, d := range docs {
		if d.key != key {
			continue
		}
		n := d.root
		for _, k := range path {
			var next *yaml.Node
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == k {
					next = n.Content[i+1]
				}
			}
			if next == nil {
				return nil
			}
			n = next
		}
		return n
	}
	return nil
}

func encodeDocs(docs []*doc) []byte {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d.root); err != nil {
			fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		fatal(err)
	}
	return b.Bytes()
}

func splitID(id string) (string, []string) {
	p := strings.Split(id, "\x00")
	return p[0], p[1:]
}

// fragment builds one fragment from the leaves of m that pick selects, in m's document order,
// each document carrying its identity fields.
func fragment(m *minfo, cl map[string]string, want string) []byte {
	var out []*doc
	byKey := map[string]*doc{}
	for _, id := range m.order {
		if cl[id] != want {
			continue
		}
		key, path := splitID(id)
		d := byKey[key]
		if d == nil {
			d = &doc{key: key, root: mapping()}
			for _, src := range m.docs {
				if src.key == key {
					for _, f := range []string{"apiVersion", "kind", "name"} {
						if v := field(src.root, f); v != "" {
							d.root.Content = append(d.root.Content, str(f), str(v))
						}
					}
				}
			}
			byKey[key] = d
			out = append(out, d)
		}
		setLeaf(d.root, path, m.nodes[id])
	}
	return encodeDocs(out)
}

// thin is a machine's document stream with the leaves of the given classes removed; a document
// left with no leaf is dropped, except the legacy one.
func thin(docs []*doc, ids map[string]bool) []byte {
	var out []*doc
	for _, d := range docs {
		c := &doc{key: d.key, root: clone(d.root)}
		for id := range ids {
			if key, path := splitID(id); key == d.key {
				delLeaf(c.root, path)
			}
		}
		n := 0
		flatten(c, func([]string, *yaml.Node) { n++ })
		if n > 0 || c.key == "main" || os.Getenv("BW_SHELLS") == "1" { // BW_SHELLS keeps an emptied document as its identity fields
			out = append(out, c)
		}
	}
	return encodeDocs(out)
}

func asSource(revision string, text []byte, decl ingest.Declarations, vals map[string]provider.Value) (compile.Source, error) {
	u, err := ingest.Read(bytes.NewReader(text), 1<<20)
	if err != nil {
		return compile.Source{}, err
	}
	c, err := ingest.Extract(ingest.Request{Input: u, Declarations: decl})
	if err != nil {
		return compile.Source{}, fmt.Errorf("extract: %w", err)
	}
	all := maps.Clone(vals)
	if all == nil {
		all = map[string]provider.Value{}
	}
	s, err := c.Commit(context.Background(), func(_ context.Context, name string, v provider.Value) error {
		all[name] = v
		return nil
	})
	if err != nil {
		return compile.Source{}, fmt.Errorf("commit: %w", err)
	}
	sum := sha256.Sum256(text)
	return compile.Source{Revision: revision, Digest: hex.EncodeToString(sum[:]), Text: s, Values: all}, nil
}

type outcome struct {
	note                   string // "" when the compiler accepted the composition
	e0, e1, e2, e3, e4, ok bool
	order, control         bool
}

func (o outcome) String() string {
	if o.note != "" {
		return "refused: " + o.note
	}
	return fmt.Sprintf("E0=%v E1=%v E2=%v E3=%v E4=%v docOrderSame=%v control=%v", o.e0, o.e1, o.e2, o.e3, o.e4, o.order, o.control)
}

// run composes thin base and fragments through the compiler (sanitized path) and on plaintext,
// and compares both with the machine's unchanged import base at every level.
func run(m *minfo, node []byte, thinSan, thinPlain []byte, frags [][]byte, vals map[string]provider.Value) outcome {
	base, err := asSource("base-"+m.name, thinSan, m.san.Declarations(), vals)
	if err != nil {
		return outcome{note: err.Error()}
	}
	in := compile.Input{Base: base, Mode: compile.ModeMetal}
	for i, f := range frags {
		s, err := asSource(fmt.Sprintf("frag-%s-%d", m.name, i), f, ingest.Declarations{}, nil)
		if err != nil {
			return outcome{note: err.Error()}
		}
		in.Fragments = append(in.Fragments, s)
	}
	d, red, err := render(in)
	if err != nil {
		return outcome{note: err.Error()}
	}
	refBase, _, _ := importBase("ref-"+m.name, node)
	refD, refRed, err := render(compile.Input{Base: refBase, Mode: compile.ModeMetal})
	if err != nil {
		fatal(err)
	}
	var o outcome
	o.e0 = d == talos.ConfigurationDigest(node)
	// The redacted forms as keyed trees. A placeholder is <redacted:NAME@VERSION> and the names
	// are random per ingest (B2), so the names are masked: what is compared is the structure and
	// the places where a secret sits, never a secret.
	red, refRed = maskRefs(red), maskRefs(refRed)
	o.e4 = e2([]byte(red), []byte(refRed))
	if os.Getenv("BW_REDDIFF") != "" && !o.e4 {
		for _, p := range treeDiff([]byte(red), []byte(refRed)) {
			fmt.Printf("    redacted differs: %s\n", p)
		}
	}
	plain, err := plainCompose(thinPlain, frags)
	if err != nil {
		return outcome{note: "plain: " + err.Error()}
	}
	o.control = d == talos.ConfigurationDigest(plain)
	o.e1 = e0(plain, must(plainCompose(node, nil)))
	o.e2 = e2(plain, node)
	o.e3 = e3(plain, node)
	o.order = sameOrder(plain, node)
	_ = refD
	return o
}

var redactedName = regexp.MustCompile(`<redacted:[^@>]*@`)

// maskRefs replaces the reference name inside every redaction placeholder.
func maskRefs(s string) string { return redactedName.ReplaceAllString(s, "<redacted:ref@") }

// treeDiff lists the leaf paths where two document streams differ, without any value.
func treeDiff(a, b []byte) []string {
	leaves := func(x []byte) map[string]string {
		out := map[string]string{}
		for _, d := range parseDocs(x) {
			flatten(d, func(p []string, v *yaml.Node) { out[lid(d.key, p)] = sig(v) })
		}
		return out
	}
	la, lb := leaves(a), leaves(b)
	var out []string
	for _, id := range slices.Sorted(maps.Keys(la)) {
		if v, ok := lb[id]; !ok {
			out = append(out, "only in the node: "+show(id))
		} else if v != la[id] {
			out = append(out, "differs: "+show(id))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(lb)) {
		if _, ok := la[id]; !ok {
			out = append(out, "only in the render: "+show(id))
		}
	}
	return out
}

func splitCases(ms []machine) {
	mis := infos(ms)
	cl := classify(mis)
	if os.Getenv("BW_R2") != "off" {
		demote(mis, cl)
	}
	byName := map[string]*minfo{}
	nodes := map[string][]byte{}
	for i, m := range mis {
		byName[m.name] = m
		nodes[m.name] = ms[i].node
	}
	common := fragment(mis[0], cl[mis[0].name], clsCommon)
	roleFrag := map[string][]byte{}
	for _, m := range mis {
		if _, ok := roleFrag[m.role]; !ok {
			roleFrag[m.role] = fragment(m, cl[m.name], clsRole)
		}
	}
	fmt.Printf("== M3: a split built from the classes: common fragment %d bytes, role fragments", len(common))
	for _, r := range []string{"controlplane", "worker"} {
		fmt.Printf(" %s %d bytes", r, len(roleFrag[r]))
	}
	fmt.Println()
	cut := func(m *minfo) (san, plain []byte) {
		ids := map[string]bool{}
		for id, c := range cl[m.name] {
			if c != clsMachine {
				ids[id] = true
			}
		}
		return thin(m.docs, ids), thin(m.plain, ids)
	}
	if d := os.Getenv("BW_DUMP"); d != "" { // the fragments and the sanitized thin bases hold no secret value
		must(0, os.MkdirAll(d, 0o700))
		must(0, os.WriteFile(filepath.Join(d, "common.yaml"), common, 0o600))
		for r, f := range roleFrag {
			must(0, os.WriteFile(filepath.Join(d, "role-"+r+".yaml"), f, 0o600))
		}
		for _, m := range mis {
			s, _ := cut(m)
			must(0, os.WriteFile(filepath.Join(d, "thin-"+m.name+".sanitized.yaml"), s, 0o600))
		}
	}
	frags := func(m *minfo, extra ...[]byte) [][]byte { return append([][]byte{common, roleFrag[m.role]}, extra...) }
	fmt.Println("-- S1: every machine, thin base plus common and role fragments")
	for _, m := range mis {
		san, plain := cut(m)
		fmt.Printf("%-7s base %5d -> %5d bytes: %s\n", m.name, len(nodes[m.name]), len(san), run(m, nodes[m.name], san, plain, frags(m), m.vals))
		if out, err := plainCompose(plain, frags(m)); err == nil && (m.name == "cp1" || m.name == "w1") {
			for _, d := range treeDiff(nodes[m.name], out) {
				fmt.Printf("        %s\n", d)
			}
		}
	}

	edit := func(frag []byte, key string, path []string, fn func(*yaml.Node)) []byte {
		ds := parseDocs(frag)
		n := findLeaf(ds, key, path)
		if n == nil {
			fatal(fmt.Errorf("no leaf %s %v in the fragment", key, path))
		}
		fn(n)
		return encodeDocs(ds)
	}
	drop := func(frag []byte, key string, path []string) []byte {
		ds := parseDocs(frag)
		for _, d := range ds {
			if d.key == key {
				delLeaf(d.root, path)
			}
		}
		return encodeDocs(ds)
	}
	fmt.Println("-- negative controls on cp1 and w1: each must be detected")
	for _, name := range []string{"cp1", "w1"} {
		m := byName[name]
		san, plain := cut(m)
		c1 := edit(common, "KubeletConfig/", []string{"image"}, func(n *yaml.Node) { n.Value += "-changed" })
		fmt.Printf("%-3s N1 a changed value in the common fragment (kubelet image): %s\n", name, run(m, nodes[name], san, plain, [][]byte{c1, roleFrag[m.role]}, m.vals))
		c2 := drop(common, "KubeNetworkConfig/", []string{"dnsDomain"})
		fmt.Printf("%-3s N2 a key dropped from the common fragment (dnsDomain): %s\n", name, run(m, nodes[name], san, plain, [][]byte{c2, roleFrag[m.role]}, m.vals))
		fmt.Printf("%-3s N5 the role fragment left out: %s\n", name, run(m, nodes[name], san, plain, [][]byte{common}, m.vals))
		fmt.Printf("%-3s N6 the fragments in the other order (role first): %s\n", name, run(m, nodes[name], san, plain, [][]byte{roleFrag[m.role], common}, m.vals))
	}

	fmt.Println("-- N4 a changed secret value behind an unchanged reference (cp1 machine.token)")
	m := byName["cp1"]
	tok := m.nodes[lid("main", []string{"machine", "token"})]
	vals := maps.Clone(m.vals)
	vals[tok.Value] = must(provider.NewValue(provider.KindString, "abcdef.0123456789abcdef"))
	san, plain := cut(m)
	n4 := run(m, nodes["cp1"], san, plain, frags(m), vals)
	// the plaintext columns E1 to E3 do not carry the changed value, so only E0 and E4 are read
	fmt.Printf("cp1 N4 same base text, one pinned value changed: E0=%v (digest over the plaintext) E4=%v (redacted form) note=%q\n", n4.e0, n4.e4, n4.note)
}

// ---- N3: pairs that differ in form only ----

func pairs(ms []machine) {
	w1 := ms[slices.IndexFunc(ms, func(m machine) bool { return m.name == "w1" })].node
	variant := func(fn func([]*doc)) []byte {
		ds := parseDocs(w1)
		fn(ds)
		return encodeDocs(ds)
	}
	sub := func(order ...string) func([]*doc) {
		return func(ds []*doc) {
			n := findLeaf(ds, "KubeNetworkConfig/", []string{"podSubnets"})
			n.Content = nil
			for _, s := range order {
				n.Content = append(n.Content, str(s))
			}
		}
	}
	a := variant(sub("10.244.0.0/16", "10.245.0.0/16"))
	b := variant(sub("10.245.0.0/16", "10.244.0.0/16"))
	report := func(label string, x, y []byte) {
		_, err1 := plainCompose(x, nil)
		_, err2 := plainCompose(y, nil)
		fmt.Printf("%s: both load=%v E0=%v E1=%v E2=%v E3=%v\n", label, err1 == nil && err2 == nil, e0(x, y), e1(x, y), e2(x, y), e3(x, y))
	}
	fmt.Println("== N3: pairs (w1)")
	report("list order (podSubnets reversed)", a, b)
	// A control only: w1 is already comment-free, so this pair says nothing about comments (B1 does).
	report("control: the comment-free machine against its re-encoding", w1, must(plainCompose(w1, nil)))
	omit := variant(func(ds []*doc) {
		for _, d := range ds {
			if d.key == "KubeletConfig/" {
				delLeaf(d.root, []string{"defaultRuntimeSeccompProfileEnabled"})
			}
		}
	})
	// Omitted reads as false (machinery v1.14.5, config/types/k8s/kubelet.go DefaultRuntimeSeccompProfileEnabled
	// dereferences a nil pointer to false), and gen config writes true: so the first pair differs in behaviour
	// and the second does not.
	explicitFalse := variant(func(ds []*doc) {
		findLeaf(ds, "KubeletConfig/", []string{"defaultRuntimeSeccompProfileEnabled"}).Value = "false"
	})
	report("explicit true (as generated) against omitted: different behaviour", w1, omit)
	report("explicit false against omitted: same behaviour", explicitFalse, omit)
}

func commentLines(b []byte) int {
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			n++
		}
	}
	return n
}

func baseline(ms []machine) {
	fmt.Println("== B1: an unchanged import base, rendered with no fragment, against the node's bytes")
	fmt.Println("machine  nodeBytes renderBytes commentLines refs  E0(node,render) E1 E2 E3 control(compile==plain)  redactedBytes")
	values := map[string]map[string]provider.Value{}
	for _, m := range ms {
		src, san, vals := importBase("base-"+m.name, m.node)
		values[m.name] = vals
		d, red, err := render(compile.Input{Base: src, Mode: compile.ModeMetal})
		if err != nil {
			fmt.Printf("%-7s  refused: %v\n", m.name, err)
			continue
		}
		plain := must(plainCompose(m.node, nil))
		control := d == talos.ConfigurationDigest(plain)
		// the render's digest against the node's digest is E0; the plain composition stands in
		// for the render's bytes in E1 to E3 once the control holds
		e0r := d == talos.ConfigurationDigest(m.node)
		fmt.Printf("%-7s  %9d %11d %12d %4d  %v %v %v %v %v  %d\n", m.name, len(m.node), len(plain), commentLines(m.node),
			strings.Count(string(san.Documents()), "!bwref"), e0r, e1(m.node, plain), e2(m.node, plain), e3(m.node, plain), control, len(red))
	}

	fmt.Println("== B2: what a comparison of sanitized bases can see of shared secrets")
	names := slices.Sorted(maps.Keys(values))
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := values[names[i]], values[names[j]]
			sharedNames, sharedValues := 0, 0
			for na, va := range a {
				if _, ok := b[na]; ok {
					sharedNames++
				}
				da, _ := va.Decode()
				for _, vb := range b {
					if db, _ := vb.Decode(); reflect.DeepEqual(da, db) {
						sharedValues++
						break
					}
				}
			}
			fmt.Printf("%s/%s: references %d/%d, shared reference names %d, values equal to one in the other %d\n",
				names[i], names[j], len(a), len(b), sharedNames, sharedValues)
		}
	}
}

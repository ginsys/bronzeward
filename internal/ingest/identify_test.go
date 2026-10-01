package ingest

import (
	"encoding/base64"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

// Synthetic WireGuard keys: 32 bytes each, base64, never used anywhere.
const (
	wgPrivate   = "c3ludGhldGljLXByaXZhdGUta2V5LWZvci10ZXN0cyE="
	wgPublic    = "c3ludGhldGljLXB1YmxpYy1rZXktZm9yLXRlc3RzISE="
	wgPreshared = "c3ludGhldGljLXByZXNoYXJlZC1rZXktZm9yLXRlc3Q="
)

const wireguardDoc = `apiVersion: v1alpha1
kind: WireguardConfig
name: wg0
privateKey: ` + wgPrivate + `
peers:
  - publicKey: ` + wgPublic + `
    presharedKey: ` + wgPreshared + `
    allowedIPs: [10.0.0.0/8]
`

// generatedStream is a freshly generated controlplane configuration (v1alpha1 and
// HostnameConfig) followed by a WireguardConfig document at doc[2].
func generatedStream(t *testing.T) []byte {
	t.Helper()
	in, err := generate.NewInput("ingest-test", "https://192.0.2.10:6443", constants.DefaultKubernetesVersion)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := in.Config(machine.TypeControlPlane)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	return append(b, []byte("---\n"+wireguardDoc)...)
}

// containerSchemaSet is the expected set computed independently of identify: the whole stream
// loaded as one container, its encoding diffed with and without RedactSecrets, each output
// document matched back to the input document of the same kind.
func containerSchemaSet(t *testing.T, stream []byte) map[string]bool {
	t.Helper()
	p, err := configloader.NewFromBytes(stream)
	if err != nil {
		t.Fatal(err)
	}
	opt := encoder.WithComments(encoder.CommentsDisabled)
	raw, err := p.EncodeBytes(opt)
	if err != nil {
		t.Fatal(err)
	}
	red, err := p.RedactSecrets("EXPECTED-REDACTED").EncodeBytes(opt)
	if err != nil {
		t.Fatal(err)
	}
	in, err := parseStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	index := map[string]int{}
	for i, d := range in {
		index[docKind(d)] = i
	}
	a, _ := parseStream(raw)
	b, _ := parseStream(red)
	if len(a) != len(b) {
		t.Fatal("encodings differ in documents")
	}
	out := map[string]bool{}
	for j := range a {
		i, ok := index[docKind(a[j])]
		if !ok {
			t.Fatalf("output document %d has no input document", j)
		}
		ra, rb := leaves(a[j]), leaves(b[j])
		for k, v := range ra {
			if rb[k] != v {
				out[(Path{Doc: i, Pointer: strings.Split(k, "\x00")}).String()] = true
			}
		}
	}
	return out
}

func docKind(d *yaml.Node) string {
	if n := mappingValue(root(d), "kind"); n != nil {
		return n.Value
	}
	return "v1alpha1"
}

func leaves(d *yaml.Node) map[string]string {
	out := map[string]string{}
	_ = walkStream([]*yaml.Node{d}, func(n *yaml.Node, p Path, key bool, _ *yaml.Node) error {
		if !key && n.Kind == yaml.ScalarNode {
			out[strings.Join(p.Pointer, "\x00")] = n.Value
		}
		return nil
	})
	return out
}

func pathSet(ts []*target) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		for _, p := range t.paths {
			out[p.String()] = true
		}
	}
	return out
}

func TestIdentifySchemaSetOfGeneratedConfig(t *testing.T) {
	stream := generatedStream(t)
	docs, err := parseStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	got, err := identify(docs, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := containerSchemaSet(t, stream)
	for _, p := range []string{
		"doc[0]/machine/ca/key", "doc[0]/cluster/secretboxEncryptionSecret", "doc[0]/machine/token",
		"doc[2]/privateKey", "doc[2]/peers/0/presharedKey",
	} {
		if !want[p] {
			t.Errorf("control: the machinery does not redact %s", p)
		}
	}
	if gotSet := pathSet(got); !sameSet(gotSet, want) {
		t.Errorf("identified %v\nwant %v", keys(gotSet), keys(want))
	}
	for _, tg := range got {
		n, ok := resolve(docs, tg.paths[0].Doc, tg.paths[0].Pointer)
		if !ok || n != tg.node {
			t.Errorf("%s: the target is not the input node", tg.paths[0])
		}
	}
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

var (
	b64Secret = base64.StdEncoding.EncodeToString([]byte(secretText))
	b64Crt    = base64.StdEncoding.EncodeToString([]byte("synthetic-certificate"))
)

func identifyText(t *testing.T, text string, marks ...string) ([]*target, error) {
	t.Helper()
	docs, err := parseStream([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	var ps []Path
	for _, m := range marks {
		p, err := ParsePath(m)
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	return identify(docs, ps)
}

func TestIdentifyExcludesReferences(t *testing.T) {
	text := "machine:\n  token: !bwref s-tok\n  ca:\n    crt: " + b64Crt + "\n    key: " + b64Secret + "\n"
	for _, marks := range [][]string{nil, {"doc[0]/machine/token"}} {
		got, err := identifyText(t, text, marks...)
		if err != nil {
			t.Fatal(err)
		}
		if set := pathSet(got); !sameSet(set, map[string]bool{"doc[0]/machine/ca/key": true}) {
			t.Errorf("marks %v: identified %v", marks, keys(set))
		}
	}
}

func TestIdentifyAliasedNodeOnce(t *testing.T) {
	got, err := identifyText(t, "machine:\n  token: &t "+secretText+"\ncluster:\n  token: *t\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].paths) != 2 {
		t.Fatalf("got %d targets", len(got))
	}
}

func TestIdentifyMarksAndKinds(t *testing.T) {
	text := `machine:
  nodeLabels:
    example.test/role: ` + secretText + `
  install:
    wipe: true
    disk: 0x1F
    image: True
  certSANs: [a.example.test, b.example.test]
  kubelet:
    extraArgs:
      x: "1"
      y: -0
cluster:
  controlPlane:
    localAPIServerPort: 6443
`
	for _, tc := range []struct {
		mark  string
		kind  provider.Kind
		value any
		rule  Rule
	}{
		{mark: "doc[0]/machine/nodeLabels/example.test~1role", kind: provider.KindString, value: secretText},
		{mark: "doc[0]/machine/install/wipe", kind: provider.KindBoolean, value: true},
		{mark: "doc[0]/cluster/controlPlane/localAPIServerPort", kind: provider.KindInteger, value: int64(6443)},
		{mark: "doc[0]/machine/nodeLabels", kind: provider.KindMapping, value: map[string]any{"example.test/role": secretText}},
		{mark: "doc[0]/machine/certSANs", rule: RuleMarkKind},
		{mark: "doc[0]/machine/install/disk", rule: RuleMarkKind},        // 0x1F would come back as 31
		{mark: "doc[0]/machine/install/image", rule: RuleMarkKind},       // True would come back as true
		{mark: "doc[0]/machine/kubelet/extraArgs/y", rule: RuleMarkKind}, // -0 would come back as 0
		{mark: "doc[0]/machine/kubelet", rule: RuleMarkKind},
		{mark: "doc[0]/machine/nope", rule: RuleMarkUnaddressed},
		{mark: "doc[1]/machine", rule: RuleMarkUnaddressed},
		{mark: "doc[0]/machine/certSANs/2", rule: RuleMarkUnaddressed},
	} {
		got, err := identifyText(t, text, tc.mark)
		if tc.rule != "" {
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != tc.rule {
				t.Errorf("%s: %v, want a %s refusal", tc.mark, err, tc.rule)
			} else if strings.Contains(err.Error(), secretText) {
				t.Errorf("%s: the refusal quotes the input: %v", tc.mark, err)
			}
			continue
		}
		if err != nil || len(got) != 1 {
			t.Errorf("%s: %d targets, %v", tc.mark, len(got), err)
			continue
		}
		kind, v, err := valueOf(got[0].node)
		if err != nil {
			t.Errorf("%s: %v", tc.mark, err)
			continue
		}
		if kind != tc.kind || !reflect.DeepEqual(v, tc.value) {
			t.Errorf("%s: kind %s, or the value differs", tc.mark, kind)
		}
	}
}

// TestIdentifyRefusesMarkTheStoredFormCannotLoad: a later ingestion loads a stored reference as
// a null, so a mark on a field the machinery needs to load the document would store a document
// no later ingestion accepts. It is refused now, before any provider write, naming the document.
func TestIdentifyRefusesMarkTheStoredFormCannotLoad(t *testing.T) {
	text := "apiVersion: v1alpha1\nkind: HostnameConfig\nhostname: " + secretText + "\n"
	for _, mark := range []string{"doc[0]/kind", "doc[0]/apiVersion"} {
		_, err := identifyText(t, text, mark)
		if !isRule(err, RuleSchemaUnloadable) {
			t.Errorf("%s: %v, want a %s refusal", mark, err, RuleSchemaUnloadable)
		} else if strings.Contains(err.Error(), secretText) {
			t.Errorf("%s: the refusal quotes the input: %v", mark, err)
		}
	}
	if _, err := identifyText(t, text, "doc[0]/hostname"); err != nil {
		t.Errorf("doc[0]/hostname: %v", err)
	}
}

// TestIdentifySchemaRefusals: a document the machinery cannot load, or a secret it finds that
// has no node of its own in the input, refuses the input; the refusal names the document only.
func TestIdentifySchemaRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		rule       Rule
	}{
		{"unknown kind", "apiVersion: v1alpha1\nkind: Nope\nsecret: " + secretText + "\n", RuleSchemaUnloadable},
		{"unknown field", "machine:\n  token: " + secretText + "\n  nope: " + secretText + "\n", RuleSchemaUnloadable},
		{"not a mapping", "- " + secretText + "\n", RuleSchemaUnloadable},
		{"merge key", "machine:\n  <<: {token: " + secretText + "}\n", ""},
		{"key folded over lines", "machine:\n  ca:\n    crt: " + b64Crt + "\n    key: |\n      " + b64Secret[:8] + "\n      " + b64Secret[8:] + "\n", RuleSchemaIndirect},
		{"binary tag", "cluster:\n  secretboxEncryptionSecret: !!binary " + b64Secret + "\n", RuleSchemaIndirect},
	} {
		_, err := identifyText(t, tc.text)
		var r *Refusal
		if !errors.As(err, &r) || tc.rule != "" && r.Rule != tc.rule {
			t.Errorf("%s: %v, want a %s refusal", tc.name, err, tc.rule)
			continue
		}
		if strings.Contains(err.Error(), secretText) {
			t.Errorf("%s: the refusal quotes the input: %v", tc.name, err)
		}
	}
}

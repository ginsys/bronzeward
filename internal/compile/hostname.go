package compile

import (
	"errors"
	"maps"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/machinename"
)

// HostnameSource is where a compiled machine's hostname comes from (compilation.md §6.1 item 2).
type HostnameSource string

const (
	// HostnameMachineName: the compiler filled the machine's name.
	HostnameMachineName HostnameSource = "machine-name"
	// HostnameConfiguration: the composition sets a hostname.
	HostnameConfiguration HostnameSource = "configuration"
	// HostnameNone: no fill and no explicit hostname: the machine has no name, or the composition
	// chose another source (a HostnameConfig with auto off or addr).
	HostnameNone HostnameSource = "none"
	// HostnameLegacyStable: a named machine whose composition sets machine.features.stableHostname,
	// or whose setting depends on a secret reference; neither the source nor the name tells which.
	HostnameLegacyStable HostnameSource = "legacy-stable"
)

// Hostname is the compiler's decision about one machine's hostname. Name is set for machine-name
// only: no other source records a hostname value.
type Hostname struct {
	Source HostnameSource
	Name   string
}

// Hostname is the decision taken for this compilation (compilation.md §6 step 5a).
func (c Compiled) Hostname() Hostname { return c.hostname }

// NodeName is the Kubernetes node name the composed configuration gives its node, and whether it may
// be compared with another machine's (compilation.md §6.1 item 6). It is not compared, and no error
// returns, where the composition holds no hostname, where the hostname or the effective
// registerWithFQDN value is the value of a secret reference (the compiler's own trace says which
// leaves hold one: comparing it would make a refusal an equality test on a secret), and where the
// derived name is empty, so two empty names never clash. It holds no configured value beyond the
// derived name.
func (c Compiled) NodeName() (string, bool, error) {
	host, fqdn, ok := nodeInputs(c.m.bytes())
	if !ok || host == "" || c.nodeSecret {
		return "", false, nil
	}
	node := nodeName(host, fqdn)
	return node, node != "", nil
}

// RecordedNodeName is the node name a release records for this machine (persistence-api.md §9.3), and
// whether one is recorded. It is NodeName's answer, kept only where the review shows every input to the
// name as the literal it is: where the review is withheld in whole (redacted is nil), or redacts the
// hostname or registerWithFQDN by value, so that the review's leaves differ from the composition's or
// cannot be read, the record is "not compared", never more than the review shows.
func (c Compiled) RecordedNodeName(redacted *string) (string, bool, error) {
	node, known, err := c.NodeName()
	if err != nil || !known || redacted == nil {
		return "", false, err
	}
	have, okHave := nodeLeafValues(c.m.bytes())
	seen, okSeen := nodeLeafValues([]byte(*redacted))
	if !okHave || !okSeen || !maps.Equal(have, seen) {
		return "", false, nil
	}
	return node, true, nil
}

// nodeLeafValues are the scalar texts at the four leaves the node name derives from, by path. The review
// is not a configuration the loader accepts (its placeholders fill typed fields), so the leaves are read
// as text; a stream that cannot be walked answers false.
func nodeLeafValues(b []byte) (map[string]string, bool) {
	values := map[string]string{}
	err := ingest.WalkLeaves(b, nil, func(p ingest.Path, n *yaml.Node) error {
		if p.Format == "" && nodeLeafPointer(p.Pointer) {
			values[p.String()] = n.Value
		}
		return nil
	})
	return values, err == nil
}

// holdsNodeLeaf reports whether a secret reference's value reached one of the four leaves the node name
// is derived from: the hostname of a HostnameConfig document or of machine.network, and
// registerWithFQDN of a KubeNodeConfig document or of machine.kubelet. It reads the output paths the
// trace and flip passes attributed to a reference, never the text of the redacted configuration, which
// also redacts a public value that happens to equal an unrelated secret. A document that cannot be
// walked counts as holding one.
func holdsNodeLeaf(b []byte, outcomes []outcome) bool {
	held := map[string]bool{}
	for _, o := range outcomes {
		for _, p := range o.paths {
			held[p] = true
		}
	}
	if len(held) == 0 {
		return false
	}
	found := false
	err := ingest.WalkLeaves(b, nil, func(p ingest.Path, _ *yaml.Node) error {
		if held[p.String()] && p.Format == "" && nodeLeafPointer(p.Pointer) {
			found = true
		}
		return nil
	})
	return found || err != nil
}

// nodeLeafPointer is whether a pointer names one of the four leaves, in a typed document (one token) or
// in the legacy one. A top-level `hostname` of a document of another kind is counted too: the check
// then skips a comparison it could have made, never makes one it should not.
func nodeLeafPointer(p []string) bool {
	switch len(p) {
	case 1:
		return p[0] == "hostname" || p[0] == "registerWithFQDN"
	case 3:
		return p[0] == "machine" && (p[1] == "network" && p[2] == "hostname" ||
			p[1] == "kubelet" && p[2] == "registerWithFQDN")
	}
	return false
}

// nodeInputs are the two values Talos derives the node name from: the hostname the accessor returns and
// the effective registerWithFQDN (the KubeNodeConfig document's field, else the machine.kubelet one).
// ok is false for text the loader refuses: a real composition it refuses took no decision either.
func nodeInputs(b []byte) (host string, fqdn, ok bool) {
	cfg, err := configloader.NewFromBytes(b)
	if err != nil {
		return "", false, false
	}
	if h := cfg.NetworkHostnameConfig(); h != nil {
		host = h.Hostname()
	}
	if k := cfg.K8sNodeConfig(); k != nil {
		fqdn = k.RegisterWithFQDN()
	}
	return host, fqdn, true
}

// nodeName is Talos's node name for a hostname (nodename.go FromHostname, v1.14, copied because the
// package is internal): the first label unless registerWithFQDN is true, `A` to `Z` lower-cased, an
// underscore turned into a hyphen, every rune but `a` to `z`, `0` to `9`, the hyphen and the dot
// dropped, and hyphens and dots trimmed from both ends. It is empty where nothing is left.
func nodeName(host string, fqdn bool) string {
	if !fqdn {
		host, _, _ = strings.Cut(host, ".")
	}
	n := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		case r == '_':
			return '-'
		}
		return -1
	}, host)
	return strings.Trim(n, "-.")
}

// hostState is what the machinery's accessor says about one composition.
type hostState struct {
	explicit     bool // the accessor returns a hostname
	doc          bool // the answer is a HostnameConfig document
	auto         nethelpers.AutoHostnameKind
	legacyStable bool // no document, and machine.features.stableHostname is set
}

func hostnameOf(m Materialized) (hostState, error) {
	cfg, err := configloader.NewFromBytes(m.bytes())
	if err != nil {
		return hostState{}, err
	}
	h := cfg.NetworkHostnameConfig()
	if h == nil {
		return hostState{}, nil
	}
	s := hostState{explicit: h.Hostname() != "", auto: h.AutoHostname()}
	if _, ok := h.(*network.HostnameConfigV1Alpha1); ok {
		s.doc = true
	} else {
		s.legacyStable = s.auto == nethelpers.AutoHostnameKindStable
	}
	return s, nil
}

// decideHostname is compilation.md §6.1 item 2, from the real composition, the trace pass and each
// boolean's flip pass. A flip that changes whether stableHostname is set makes the setting depend on
// a secret: it counts as set, whatever its value.
//
// A real composition the loader refuses takes no decision (none, no error): the validation of step 8
// loads the same bytes with the same loader and refuses it as invalid, as it did before the fill
// existed, and a refusal here would replace that answer. The trace and flip compositions are read
// only for a named machine, and one that will not load, where the real one does, is the error: the
// machine's decision cannot be taken, and no step 8 load of those two compositions exists (it
// validates the restored passes), so the error is a fidelity refusal and never a silent skip of the
// fill. An unnamed machine reads neither, and its answers stay those of main.
func decideHostname(name string, real, trace Materialized, flips []Materialized) (Hostname, error) {
	none := Hostname{Source: HostnameNone}
	r, err := hostnameOf(real)
	if err != nil {
		return none, nil
	}
	if r.explicit {
		return Hostname{Source: HostnameConfiguration}, nil
	}
	if name == "" {
		return none, nil
	}
	t, err := hostnameOf(trace)
	if err != nil {
		return Hostname{}, err
	}
	legacy := r.legacyStable
	for _, f := range flips {
		s, err := hostnameOf(f)
		if err != nil {
			return Hostname{}, err
		}
		legacy = legacy || s.legacyStable != t.legacyStable
	}
	switch {
	case legacy:
		return Hostname{Source: HostnameLegacyStable}, nil
	case r.doc && r.auto != nethelpers.AutoHostnameKindStable:
		return none, nil // the operator chose another source
	}
	return Hostname{Source: HostnameMachineName, Name: name}, nil
}

// hostnamePatchRevision names the compiler's own source: not a fragment revision, and never
// recorded (the patch holds no reference, so no occurrence is placed in or overridden by it).
const hostnamePatchRevision = "bronzeward-hostname"

// hostnamePatch is the one document the compiler composes last: auto off and the machine's name
// (compilation.md §6.1 item 1).
func hostnamePatch(name string) (Source, error) {
	if !machinename.Valid(name) {
		return Source{}, errors.New("compile: the machine's name is not a valid hostname")
	}
	text, err := yaml.Marshal(struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Auto       string `yaml:"auto"`
		Hostname   string `yaml:"hostname"`
	}{"v1alpha1", "HostnameConfig", "off", name})
	if err != nil {
		return Source{}, err
	}
	s, err := ingest.Stored(string(text), ingest.Declarations{})
	if err != nil {
		return Source{}, err
	}
	return Source{Revision: hostnamePatchRevision, Text: s}, nil
}

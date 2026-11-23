package compile

import (
	"errors"

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
func decideHostname(name string, real, trace Materialized, flips []Materialized) (Hostname, error) {
	r, err := hostnameOf(real)
	if err != nil {
		return Hostname{}, err
	}
	if r.explicit {
		return Hostname{Source: HostnameConfiguration}, nil
	}
	if name == "" {
		return Hostname{Source: HostnameNone}, nil
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
		return Hostname{Source: HostnameNone}, nil // the operator chose another source
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

package compile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/ginsys/bronzeward/internal/contract"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"
)

// machineryModule is the renderer's module (compilation.md §10.1).
const machineryModule = "github.com/siderolabs/talos/pkg/machinery"

const (
	RuleContract   Rule = "contract"   // the cluster's contract is not a supported one this build renders
	RuleKubernetes Rule = "kubernetes" // the configuration names no Kubernetes version the contract supports
)

var (
	moduleSum = regexp.MustCompile(`^h1:[A-Za-z0-9+/]{43}=$`)
	// releaseTag is a Kubernetes release, as an image tag names it and the release's column holds it.
	releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})$`)

	errMachinery = errors.New("compile: the build does not record the machinery module it holds")
)

// Machinery is the version and go.sum checksum of the machinery module this build compiles with
// (compilation.md §10.2): the renderer a release records. A build that does not record them, or
// holds a replaced module, records nothing.
func Machinery() (version, checksum string, err error) {
	return machinery(debug.ReadBuildInfo())
}

func machinery(bi *debug.BuildInfo, ok bool) (string, string, error) {
	if !ok || bi == nil {
		return "", "", errMachinery
	}
	for _, d := range bi.Deps {
		if d.Path != machineryModule {
			continue
		}
		if _, ok := contract.Minor(d.Version); d.Replace != nil || len(d.Version) > 80 || !ok || !moduleSum.MatchString(d.Sum) {
			return "", "", errMachinery
		}
		return d.Version, d.Sum, nil
	}
	return "", "", errMachinery
}

// CheckContract refuses a cluster contract that is not on the list of supported contracts or is
// newer than the machinery version (compilation.md §10.2, choices §16.25 and §16.33): the machinery
// renders a newer contract silently as its own, and a contract nobody has evidence for stays out.
func CheckContract(machinery, clusterContract string) error {
	if !contract.Supported.Listed(clusterContract, machinery) {
		return &Error{Rule: RuleContract, Message: "the cluster's contract is not one this build renders"}
	}
	return nil
}

// SupportedContracts are the contracts this build renders: the listed ones the machinery version it
// records is not older than. It reads them the way CheckContract does, so a cluster that is
// registered with one is never refused at publish for its contract.
func SupportedContracts() ([]string, error) {
	version, _, err := Machinery()
	if err != nil {
		return nil, err
	}
	return contract.Supported.Contracts(version), nil
}

// KubernetesVersion is the Kubernetes version the compiled configuration runs (compilation.md
// §10.2): the tag of its kubelet image, the machinery's default when none is set. It is refused
// unless it is a release in the SupportedWith window of the cluster's contract, which validation
// does not check, and unless the image is the one the redacted configuration shows: an image a
// reference placed, or a copy of a value, is not recorded. The refusal names the check in fixed
// text, never the image, and carries no path: a path is text a value could equal (§8.3), and
// the configuration has one kubelet image.
func (c Compiled) KubernetesVersion(clusterContract string) (string, error) {
	refuse := func(msg string) error { return &Error{Rule: RuleKubernetes, Message: msg} }
	if c.m.s == nil {
		return "", refuse("no compiled configuration")
	}
	cfg, err := configloader.NewFromBytes(c.m.bytes())
	if err != nil || cfg.Machine() == nil || cfg.K8sKubeletConfig() == nil {
		return "", refuse("the configuration has no machine document")
	}
	composed := cfg.K8sKubeletConfig().Image()
	redacted, err := c.Redacted()
	if err != nil {
		return "", refuse("the kubelet image cannot be shown")
	}
	image, err := shownImage(redacted)
	if err != nil {
		return "", refuse("the kubelet image cannot be shown")
	}
	if image == "" {
		image = fmt.Sprintf("%s:v%s", constants.KubeletImage, constants.DefaultKubernetesVersion)
	}
	// Compile already refuses a copy of a value in an unattributed leaf, so the shown image differs
	// from the composed one only if redaction changed it some other way: fail closed.
	if image != composed {
		return "", refuse("the kubelet image holds a value")
	}
	// The tag as the machinery reads it (v1alpha1.KubernetesVersionFromImageRef): after the last
	// ":v", up to a digest.
	i := strings.LastIndex(image, ":v")
	if i < 0 {
		return "", refuse("the kubelet image names no Kubernetes version")
	}
	tag, _, _ := strings.Cut(image[i+1:], "@")
	k := releaseTag.FindStringSubmatch(tag)
	if k == nil {
		return "", refuse("the kubelet image names no Kubernetes version")
	}
	lo, hi, ok := contract.Supported.Window(clusterContract)
	if !ok {
		return "", refuse("the cluster's contract has no Kubernetes window")
	}
	if v := [3]uint64{number(k[1]), number(k[2]), number(k[3])}; less(v, lo) || !less(v, hi) {
		return "", refuse("the Kubernetes version is outside the contract's supported window")
	}
	return tag, nil
}

// number is a numeric part the version patterns matched: at most four digits, so it parses.
func number(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

func less(a, b [3]uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// shownImage is the kubelet image the redacted configuration sets, "" when it sets none. A key on
// its way that redaction replaced hides whether an image is set, so it refuses, as does a second
// image.
func shownImage(redacted string) (string, error) {
	errHidden := errors.New("compile: the kubelet image is hidden")
	dec := yaml.NewDecoder(bytes.NewReader([]byte(redacted)))
	image, found := "", false
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return image, nil
		}
		if err != nil {
			return "", errHidden
		}
		if len(n.Content) == 0 {
			continue
		}
		node := n.Content[0]
		// v1.14 keeps the image in the KubeletConfig document, and in machine.kubelet of v1alpha1.
		path := []string{"machine", "kubelet", "image"}
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == "kind" && node.Content[i+1].Value == "KubeletConfig" {
					path = []string{"image"}
				}
			}
		}
		for _, key := range path {
			if node.Kind != yaml.MappingNode {
				return "", errHidden
			}
			var next *yaml.Node
			for i := 0; i+1 < len(node.Content); i += 2 {
				k := node.Content[i]
				if strings.Contains(k.Value, "<redacted") {
					return "", errHidden
				}
				if k.Value == key {
					next = node.Content[i+1]
				}
			}
			if next == nil {
				break
			}
			node = next
			if key == "image" {
				if node.Kind != yaml.ScalarNode || found {
					return "", errHidden
				}
				image, found = node.Value, true
			}
		}
	}
}

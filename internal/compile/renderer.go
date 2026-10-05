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

	"github.com/siderolabs/talos/pkg/machinery/compatibility/talos113"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"
)

// machineryModule is the renderer's module (compilation.md §10.1).
const machineryModule = "github.com/siderolabs/talos/pkg/machinery"

const (
	RuleContract   Rule = "contract"   // the cluster's contract is not the machinery's minor
	RuleKubernetes Rule = "kubernetes" // the configuration names no Kubernetes version the machinery supports
)

var (
	// moduleVersion is a canonical Go module version, as the release's column holds it.
	moduleVersion = regexp.MustCompile(`^v(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})` +
		`(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
	moduleSum  = regexp.MustCompile(`^h1:[A-Za-z0-9+/]{43}=$`)
	contractRe = regexp.MustCompile(`^v(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})$`)
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
		if d.Replace != nil || len(d.Version) > 80 || !moduleVersion.MatchString(d.Version) || !moduleSum.MatchString(d.Sum) {
			return "", "", errMachinery
		}
		return d.Version, d.Sum, nil
	}
	return "", "", errMachinery
}

// CheckContract refuses a cluster contract other than the minor of the machinery version: the PoC
// compiles only the node's running contract (compilation.md §10.2, choice §16.25), and the
// machinery renders a newer contract silently as its own.
func CheckContract(machinery, contract string) error {
	refuse := &Error{Rule: RuleContract, Message: "the cluster's contract is not the renderer's minor"}
	if !moduleVersion.MatchString(machinery) || !contractRe.MatchString(contract) {
		return refuse
	}
	major, rest, _ := strings.Cut(strings.TrimPrefix(machinery, "v"), ".")
	minor, _, _ := strings.Cut(rest, ".")
	if contract != "v"+major+"."+minor {
		return refuse
	}
	return nil
}

// KubernetesVersion is the Kubernetes version the compiled configuration runs (compilation.md
// §10.2): the tag of its kubelet image, the machinery's default when none is set. It is refused
// unless it is a release in the SupportedWith window of machinery's Talos minor, which validation
// does not check, and unless the image is the one the redacted configuration shows: an image a
// reference placed, or a copy of a value, is not recorded. The refusal names the check, never the
// image.
func (c Compiled) KubernetesVersion(machinery string) (string, error) {
	refuse := func(path, msg string) error {
		e := &Error{Rule: RuleKubernetes, Message: msg}
		if path != "" {
			e.Paths = []string{path}
		}
		return e
	}
	if c.m.s == nil {
		return "", refuse("", "no compiled configuration")
	}
	cfg, err := configloader.NewFromBytes(c.m.bytes())
	if err != nil || cfg.Machine() == nil {
		return "", refuse("", "the configuration has no machine document")
	}
	composed := cfg.Machine().Kubelet().Image()
	redacted, err := c.Redacted()
	if err != nil {
		return "", refuse("", "the kubelet image cannot be shown")
	}
	image, path, err := shownImage(redacted)
	if err != nil {
		return "", refuse(path, "the kubelet image cannot be shown")
	}
	if image == "" {
		image = fmt.Sprintf("%s:v%s", constants.KubeletImage, constants.DefaultKubernetesVersion)
	}
	// Compile already refuses a copy of a value in an unattributed leaf, so the shown image differs
	// from the composed one only if redaction changed it some other way: fail closed.
	if image != composed {
		return "", refuse(path, "the kubelet image holds a value")
	}
	// The tag as the machinery reads it (v1alpha1.KubernetesVersionFromImageRef): after the last
	// ":v", up to a digest.
	i := strings.LastIndex(image, ":v")
	if i < 0 {
		return "", refuse(path, "the kubelet image names no Kubernetes version")
	}
	tag, _, _ := strings.Cut(image[i+1:], "@")
	k := releaseTag.FindStringSubmatch(tag)
	if k == nil {
		return "", refuse(path, "the kubelet image names no Kubernetes version")
	}
	m := moduleVersion.FindStringSubmatch(machinery)
	if m == nil {
		return "", refuse(path, "the renderer's version is not known")
	}
	w, ok := kubernetesWindows[[2]uint64{number(m[1]), number(m[2])}]
	if !ok {
		return "", refuse(path, "the renderer's version is not known")
	}
	if v := [3]uint64{number(k[1]), number(k[2]), number(k[3])}; less(v, w[0]) || !less(v, w[1]) {
		return "", refuse(path, "the Kubernetes version is outside the renderer's supported window")
	}
	return tag, nil
}

// kubernetesWindows is the machinery's SupportedWith window for each Talos minor the PoC renders:
// from the first version, inclusive, to the second, exclusive, compared on major, minor and patch
// (compatibility.KubernetesVersion.SupportedWith). compatibility.ParseTalosVersion takes the
// machine API's version message, which only internal/talos imports, so the window is read from
// the minor's own constants; a machinery of any other minor has none and refuses.
var kubernetesWindows = map[[2]uint64][2][3]uint64{
	talos113.MajorMinor: {
		{talos113.MinimumKubernetesVersion.Major, talos113.MinimumKubernetesVersion.Minor, talos113.MinimumKubernetesVersion.Patch},
		{talos113.MaximumKubernetesVersion.Major, talos113.MaximumKubernetesVersion.Minor, talos113.MaximumKubernetesVersion.Patch},
	},
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

// shownImage is the kubelet image the redacted configuration sets, "" when it sets none, and its
// path. A key on its way that redaction replaced hides whether an image is set, so it refuses.
func shownImage(redacted string) (string, string, error) {
	errHidden := errors.New("compile: the kubelet image is hidden")
	dec := yaml.NewDecoder(bytes.NewReader([]byte(redacted)))
	image, at := "", ""
	for doc := 0; ; doc++ {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return image, at, nil
		}
		if err != nil {
			return "", "", errHidden
		}
		if len(n.Content) == 0 {
			continue
		}
		path := fmt.Sprintf("doc[%d]/machine/kubelet/image", doc)
		node := n.Content[0]
		for _, key := range []string{"machine", "kubelet", "image"} {
			if node.Kind != yaml.MappingNode {
				return "", path, errHidden
			}
			var next *yaml.Node
			for i := 0; i+1 < len(node.Content); i += 2 {
				k := node.Content[i]
				if strings.Contains(k.Value, "<redacted") {
					return "", path, errHidden
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
				if node.Kind != yaml.ScalarNode || at != "" {
					return "", path, errHidden
				}
				image, at = node.Value, path
			}
		}
	}
}

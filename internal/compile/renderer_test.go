package compile

import (
	"errors"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// moduleLine is the machinery's line of the named module file, split into fields; in go.sum, the
// line of the module's content, not of its go.mod.
func moduleLine(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == machineryModule && !strings.HasSuffix(f[1], "/go.mod") {
			return f
		}
	}
	t.Fatalf("%s names no %s line", file, machineryModule)
	return nil
}

// The release records the machinery module version and its checksum (compilation.md §10.2) as
// the build that compiles it holds them: the version go.mod requires and go.sum's h1 checksum of
// the module's content, not of its go.mod.
func TestMachinery(t *testing.T) {
	version, sum, err := Machinery()
	if err != nil {
		t.Fatal(err)
	}
	if want := moduleLine(t, "../../go.mod")[1]; version != want {
		t.Errorf("version %q, go.mod requires %q", version, want)
	}
	if want := moduleLine(t, "../../go.sum")[2]; sum != want {
		t.Errorf("checksum %q, go.sum holds %q", sum, want)
	}
	if !regexp.MustCompile(`^h1:[A-Za-z0-9+/]{43}=$`).MatchString(sum) {
		t.Errorf("checksum %q is not an h1 checksum", sum)
	}
}

// A build that does not say which machinery it holds, or holds a replaced one, records nothing:
// the release would name a renderer it cannot show it ran.
func TestMachineryRefuses(t *testing.T) {
	dep := func(m debug.Module) *debug.BuildInfo { return &debug.BuildInfo{Deps: []*debug.Module{&m}} }
	good := debug.Module{Path: machineryModule, Version: "v1.13.6", Sum: "h1:0XwJroRMDS8whr2KdxFQuyOJz+Zs4AV9G8oyE0emDJI="}
	if v, s, err := machinery(dep(good), true); err != nil || v != good.Version || s != good.Sum {
		t.Fatalf("a recorded machinery: %q %q %v", v, s, err)
	}
	replaced := good
	replaced.Replace = &debug.Module{Path: "../talos/pkg/machinery"}
	for name, c := range map[string]struct {
		bi *debug.BuildInfo
		ok bool
	}{
		"no build information":  {nil, false},
		"no machinery module":   {&debug.BuildInfo{Deps: []*debug.Module{{Path: "example.com/other", Version: "v1.0.0", Sum: good.Sum}}}, true},
		"a replaced machinery":  {dep(replaced), true},
		"no checksum":           {dep(debug.Module{Path: machineryModule, Version: "v1.13.6"}), true},
		"a development version": {dep(debug.Module{Path: machineryModule, Version: "(devel)", Sum: good.Sum}), true},
		"a checksum of no form": {dep(debug.Module{Path: machineryModule, Version: "v1.13.6", Sum: "h1:short="}), true},
		"a version of no form":  {dep(debug.Module{Path: machineryModule, Version: "v1.13", Sum: good.Sum}), true},
		"a leading zero":        {dep(debug.Module{Path: machineryModule, Version: "v1.013.6", Sum: good.Sum}), true},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := machinery(c.bi, c.ok); err == nil {
				t.Fatal("recorded")
			}
		})
	}
}

// The PoC compiles only the machinery's own contract minor (compilation.md §10.2, choice §16.25):
// a newer contract would be rendered silently as the machinery's, an older one is not the node's.
func TestCheckContract(t *testing.T) {
	if err := CheckContract("v1.13.6", "v1.13"); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{
		{"v1.13.6", "v1.14"}, {"v1.13.6", "v1.12"}, {"v1.13.6", "v2.13"}, {"v1.13.6", "1.13"},
		{"v1.13.6", "v1.13.6"}, {"v1.13.6", ""}, {"v1.13", "v1.13"}, {"", "v1.13"},
	} {
		err := CheckContract(c[0], c[1])
		var e *Error
		if !errors.As(err, &e) || e.Rule != RuleContract {
			t.Errorf("machinery %q, contract %q: %v, want a %s refusal", c[0], c[1], err, RuleContract)
		}
	}
}

// kubelet is a fragment setting the kubelet image to image.
func kubelet(t *testing.T, image string) Source {
	t.Helper()
	return source(t, "machine:\n  kubelet:\n    image: "+image+"\n", ingest.Declarations{}, nil)
}

// The release's Kubernetes version is the kubelet image tag of the validated configuration, the
// machinery's default when none is set, and must lie in the machinery's SupportedWith window for
// its own Talos minor, which validation does not check (compilation.md §10.2).
func TestKubernetesVersion(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	for name, c := range map[string]struct {
		frags []Source
		want  string
	}{
		"the generated image":     {nil, "v1.36.2"},
		"an explicit 1.31":        {[]Source{kubelet(t, "ghcr.io/siderolabs/kubelet:v1.31.0")}, "v1.31.0"},
		"the window's last patch": {[]Source{kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.98")}, "v1.36.98"},
		"a pinned digest":         {[]Source{kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.1@sha256:"+strings.Repeat("ab", 32))}, "v1.36.1"},
		"a registry with a port":  {[]Source{kubelet(t, "registry.example:5000/kubelet:v1.35.4")}, "v1.35.4"},
		"an explicitly empty one": {[]Source{kubelet(t, `""`)}, "v1.36.2"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := compiled(t, Input{Base: base, Fragments: c.frags, Mode: ModeMetal}).KubernetesVersion("v1.13.6")
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	// The machinery this build holds has a window: an upgrade to a minor without one fails here,
	// not at the first publication.
	version, _, err := Machinery()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled(t, Input{Base: base, Mode: ModeMetal}).KubernetesVersion(version); err != nil {
		t.Errorf("the build's machinery %s: %v", version, err)
	}
}

func TestKubernetesVersionRefuses(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	placed := source(t, "machine:\n  kubelet:\n    image: !bwref app/image\n",
		refs(map[string]ingest.Reference{"app/image": ref(provider.KindString)}),
		map[string]provider.Value{"app/image": value(t, provider.KindString, "ghcr.io/siderolabs/kubelet:v1.36.2")})
	whole := source(t, "machine:\n  kubelet: !bwref app/kubelet\n",
		refs(map[string]ingest.Reference{"app/kubelet": ref(provider.KindMapping)}),
		map[string]provider.Value{"app/kubelet": value(t, provider.KindMapping, map[string]any{"image": "ghcr.io/siderolabs/kubelet:v1.36.2"})})
	for name, c := range map[string]struct {
		frag      Source
		machinery string
	}{
		"Kubernetes 1.37":                {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.37.0"), "v1.13.6"},
		"Kubernetes 1.30":                {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.30.9"), "v1.13.6"},
		"the window's exclusive end":     {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.99"), "v1.13.6"},
		"Kubernetes 2.32":                {kubelet(t, "ghcr.io/siderolabs/kubelet:v2.32.0"), "v1.13.6"},
		"a tag that is not a version":    {kubelet(t, "ghcr.io/siderolabs/kubelet:latest"), "v1.13.6"},
		"a v tag that is not a version":  {kubelet(t, "ghcr.io/siderolabs/kubelet:vnext"), "v1.13.6"},
		"a prerelease":                   {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.0-rc.1"), "v1.13.6"},
		"an image a reference placed":    {placed, "v1.13.6"},
		"a kubelet a reference placed":   {whole, "v1.13.6"},
		"a machinery with no window":     {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.2"), "v1.99.0"},
		"a machinery version of no form": {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.2"), "devel"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := compiled(t, Input{Base: base, Fragments: []Source{c.frag}, Mode: ModeMetal}).KubernetesVersion(c.machinery)
			var e *Error
			if !errors.As(err, &e) || e.Rule != RuleKubernetes {
				t.Fatalf("got %q, %v; want a %s refusal", got, err, RuleKubernetes)
			}
			if strings.Contains(err.Error(), "kubelet:") || strings.Contains(err.Error(), "1.3") {
				t.Errorf("the refusal quotes the image: %v", err)
			}
		})
	}
	if _, err := (Compiled{}).KubernetesVersion("v1.13.6"); err == nil {
		t.Error("an empty compilation gave a version")
	}
}

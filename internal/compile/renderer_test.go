package compile

import (
	"errors"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/contract"
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
	good := debug.Module{Path: machineryModule, Version: "v1.14.5", Sum: "h1:s7tKS2fe2TjD8mVfD4NG2mF6GMJn5THhsgt4/x5+eUM="}
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
		"no checksum":           {dep(debug.Module{Path: machineryModule, Version: "v1.14.5"}), true},
		"a development version": {dep(debug.Module{Path: machineryModule, Version: "(devel)", Sum: good.Sum}), true},
		"a checksum of no form": {dep(debug.Module{Path: machineryModule, Version: "v1.14.5", Sum: "h1:short="}), true},
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

// A cluster's contract is supported when the list of supported contracts holds it and the machinery
// is not older than it (compilation.md §10.2, choices §16.25 and §16.33): a newer contract would be
// rendered silently as the machinery's. A machinery newer than the listed contract is a stand-in for
// the library after a bump: v1.14 is still accepted, as itself.
func TestCheckContract(t *testing.T) {
	for _, library := range []string{"v1.14.5", "v1.15.0", "v1.15.0-beta.1"} {
		if err := CheckContract(library, "v1.14"); err != nil {
			t.Errorf("machinery %s, contract v1.14: %v", library, err)
		}
	}
	for _, c := range [][2]string{
		{"v1.14.5", "v1.15"}, {"v1.14.5", "v1.13"}, {"v1.15.0", "v1.15"}, {"v1.15.0", "v1.13"}, {"v1.14.5", "v2.14"},
		{"v1.14.5", "1.14"}, {"v1.14.5", "v1.14.5"}, {"v1.14.5", ""}, {"v1.14", "v1.14"}, {"", "v1.14"},
		{"v1.13.6", "v1.14"},
	} {
		err := CheckContract(c[0], c[1])
		var e *Error
		if !errors.As(err, &e) || e.Rule != RuleContract {
			t.Errorf("machinery %q, contract %q: %v, want a %s refusal", c[0], c[1], err, RuleContract)
		}
	}
}

// kubelet is a fragment setting the kubelet image to image, in the KubeletConfig document that
// v1.14 keeps it in.
func kubelet(t *testing.T, image string) Source {
	t.Helper()
	return source(t, "apiVersion: v1alpha1\nkind: KubeletConfig\nimage: "+image+"\n", ingest.Declarations{}, nil)
}

// The release's Kubernetes version is the kubelet image tag of the validated configuration, the
// machinery's default when none is set, and must lie in the SupportedWith window of the cluster's
// contract, which validation does not check (compilation.md §10.2). The window is the contract's,
// not the library's: it is asked for the contract and for nothing else.
func TestKubernetesVersion(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	for name, c := range map[string]struct {
		frags []Source
		want  string
	}{
		"the generated image":     {nil, "v1.37.1"},
		"an explicit 1.32":        {[]Source{kubelet(t, "ghcr.io/siderolabs/kubelet:v1.32.0")}, "v1.32.0"},
		"the window's last patch": {[]Source{kubelet(t, "ghcr.io/siderolabs/kubelet:v1.37.98")}, "v1.37.98"},
		"a pinned digest":         {[]Source{kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.1@sha256:"+strings.Repeat("ab", 32))}, "v1.36.1"},
		"a registry with a port":  {[]Source{kubelet(t, "registry.example:5000/kubelet:v1.35.4")}, "v1.35.4"},
		"an explicitly empty one": {[]Source{kubelet(t, `""`)}, "v1.37.1"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := compiled(t, Input{Base: base, Fragments: c.frags, Mode: ModeMetal}).KubernetesVersion("v1.14")
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
	// The machinery's default Kubernetes version, which the generated base and an unset kubelet image
	// take, lies in the window of every contract this build can render: a library bump whose default
	// leaves it fails here, not at the first publication.
	version, _, err := Machinery()
	if err != nil {
		t.Fatal(err)
	}
	listed := contract.Supported.Contracts(version)
	if len(listed) == 0 {
		t.Fatalf("the build's machinery %s renders no listed contract", version)
	}
	for _, c := range listed {
		if _, err := compiled(t, Input{Base: base, Mode: ModeMetal}).KubernetesVersion(c); err != nil {
			t.Errorf("contract %s with the build's machinery %s: %v", c, version, err)
		}
	}
}

func TestKubernetesVersionRefuses(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	placed := source(t, "apiVersion: v1alpha1\nkind: KubeletConfig\nimage: !bwref app/image\n",
		refs(map[string]ingest.Reference{"app/image": ref(provider.KindString)}),
		map[string]provider.Value{"app/image": value(t, provider.KindString, "ghcr.io/siderolabs/kubelet:v1.36.2")})
	for name, c := range map[string]struct {
		frag     Source
		contract string
	}{
		"Kubernetes 1.38":               {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.38.0"), "v1.14"},
		"Kubernetes 1.31":               {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.31.9"), "v1.14"},
		"the window's exclusive end":    {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.37.99"), "v1.14"},
		"Kubernetes 2.32":               {kubelet(t, "ghcr.io/siderolabs/kubelet:v2.32.0"), "v1.14"},
		"a tag that is not a version":   {kubelet(t, "ghcr.io/siderolabs/kubelet:latest"), "v1.14"},
		"a v tag that is not a version": {kubelet(t, "ghcr.io/siderolabs/kubelet:vnext"), "v1.14"},
		"a prerelease":                  {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.0-rc.1"), "v1.14"},
		"an image a reference placed":   {placed, "v1.14"},
		"a contract with no window":     {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.2"), "v1.15"},
		"a contract of no form":         {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.2"), "devel"},
		"a library version":             {kubelet(t, "ghcr.io/siderolabs/kubelet:v1.36.2"), "v1.14.5"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := compiled(t, Input{Base: base, Fragments: []Source{c.frag}, Mode: ModeMetal}).KubernetesVersion(c.contract)
			var e *Error
			if !errors.As(err, &e) || e.Rule != RuleKubernetes {
				t.Fatalf("got %q, %v; want a %s refusal", got, err, RuleKubernetes)
			}
			if strings.Contains(err.Error(), "kubelet:") || strings.Contains(err.Error(), "1.3") {
				t.Errorf("the refusal quotes the image: %v", err)
			}
			// A path is text a value could equal (§8.3); the refusal names the field in its fixed
			// message instead.
			if len(e.Paths) != 0 || e.Input != "" {
				t.Errorf("the refusal carries input-shaped text: %+v", e)
			}
		})
	}
	if _, err := (Compiled{}).KubernetesVersion("v1.14"); err == nil {
		t.Error("an empty compilation gave a version")
	}
}

package accessput

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This guard reads the package's source, not its behaviour: the CLI links this package with the
// operator's own token, and what keeps it a create-only writer is that its source never makes any
// other call. Every rule has a failing control on a synthetic source, so a checker that stopped
// matching fails its control instead of passing the real tree.

const (
	secretFunc = "secretPath"
	idPackage  = "github.com/ginsys/bronzeward/internal/id"
)

// check returns one line per violation in the given sources.
func check(t *testing.T, files map[string]string) []string {
	t.Helper()
	var out []string
	for name, src := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !allowedImport(p) {
				out = append(out, name+": imports "+p)
			}
		}
		for _, d := range f.Decls {
			fn, isFunc := d.(*ast.FuncDecl)
			ast.Inspect(d, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					// Only the POST method constant of net/http is used, and no helper that picks
					// another method or builds its own request.
					if x, ok := n.X.(*ast.Ident); ok && x.Name == "http" && n.Sel.Name != "MethodPost" && (strings.HasPrefix(n.Sel.Name, "Method") || slices.Contains(helpers, n.Sel.Name)) {
						out = append(out, name+": uses http."+n.Sel.Name)
					}
					if slices.Contains(clientCalls, n.Sel.Name) {
						out = append(out, name+": calls ."+n.Sel.Name)
					}
				case *ast.BasicLit:
					if n.Kind != token.STRING {
						return true
					}
					s, _ := strconv.Unquote(n.Value)
					low := strings.ToLower(s)
					switch {
					case strings.Contains(s, "/v1/") && !(isFunc && fn.Name.Name == secretFunc):
						out = append(out, name+": a /v1/ path outside "+secretFunc)
					case slices.Contains([]string{"get", "head", "delete", "list", "put", "patch", "options", "connect", "trace"}, low):
						out = append(out, name+": names the method "+s)
					case strings.Contains(low, "metadata") || strings.Contains(low, "destroy") || strings.Contains(low, "undelete") || strings.Contains(low, "list="):
						out = append(out, name+": names a KV operation other than the create")
					}
				}
				return true
			})
			if isFunc && fn.Name.Name == secretFunc && !buildsFromCluster(fn) {
				out = append(out, name+": "+secretFunc+" does not check the cluster with id.MustHave(..., id.Cluster)")
			}
		}
	}
	slices.Sort(out)
	return out
}

// helpers are net/http functions that send a request of their own.
var helpers = []string{"Get", "Head", "Post", "PostForm", "DefaultClient"}

// clientCalls are method names that send a request without naming the method constant.
var clientCalls = []string{"Get", "Head", "PostForm", "Delete"}

func allowedImport(p string) bool {
	if p == idPackage {
		return true
	}
	first, _, _ := strings.Cut(p, "/")
	return !strings.Contains(first, ".") // the standard library: no module, no OpenBao library, no internal/provider
}

// buildsFromCluster reports whether fn calls id.MustHave with id.Cluster.
func buildsFromCluster(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) != 2 {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		arg, ok2 := c.Args[1].(*ast.SelectorExpr)
		if ok && ok2 && sel.Sel.Name == "MustHave" && arg.Sel.Name == "Cluster" {
			found = true
		}
		return true
	})
	return found
}

func TestSourceStaysCreateOnly(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		files[n] = string(b)
	}
	if len(files) == 0 {
		t.Fatal("no source files")
	}
	if v := check(t, files); len(v) != 0 {
		t.Errorf("the source leaves the create-only shape:\n%s", strings.Join(v, "\n"))
	}
}

// The checker finds what it names: a clean synthetic source passes, and each rule fails on a source
// that breaks only that rule.
func TestGuardControls(t *testing.T) {
	const clean = `package p

import (
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
)

func secretPath(cluster string) (string, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return "", err
	}
	return "/v1/secret/data/access/talos/" + cluster, nil
}

func send(u string) { http.NewRequest(http.MethodPost, u, nil) }
`
	if v := check(t, map[string]string{"clean.go": clean}); len(v) != 0 {
		t.Fatalf("the clean control fails: %v", v)
	}
	withImport := func(imp string) string { return strings.Replace(clean, `"net/http"`, `"net/http"`+"\n\t"+imp, 1) }
	withSend := func(call string) string {
		return strings.Replace(clean, `func send(u string) { http.NewRequest(http.MethodPost, u, nil) }`, "func send(u string) { "+call+" }", 1)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"GET":                 {withSend(`http.NewRequest(http.MethodGet, u, nil)`), "http.MethodGet"},
		"DELETE":              {withSend(`http.NewRequest(http.MethodDelete, u, nil)`), "http.MethodDelete"},
		"LIST by method":      {withSend(`http.NewRequest("LIST", u, nil)`), `names the method LIST`},
		"LIST by query":       {strings.Replace(clean, "/talos/", "/talos/?list=true#", 1), "KV operation"},
		"PUT":                 {withSend(`http.NewRequest(http.MethodPut, u, nil)`), "http.MethodPut"},
		"helper Get":          {withSend(`http.Get(u)`), "http.Get"},
		"helper Post":         {withSend(`http.Post(u, "", nil)`), "http.Post"},
		"default client":      {withSend(`http.DefaultClient.Do(nil)`), "http.DefaultClient"},
		"client Get":          {withSend(`c.Get(u)`), "calls .Get"},
		"another path":        {withSend(`http.NewRequest(http.MethodPost, "/v1/sys/seal", nil)`), "a /v1/ path outside"},
		"metadata path":       {strings.Replace(clean, "/data/", "/metadata/", 1), "KV operation"},
		"destroy path":        {strings.Replace(clean, "/data/", "/destroy/", 1), "KV operation"},
		"provider import":     {withImport(`"github.com/ginsys/bronzeward/internal/provider"`), "internal/provider"},
		"talos import":        {withImport(`"github.com/ginsys/bronzeward/internal/talos"`), "internal/talos"},
		"OpenBao library":     {withImport(`"github.com/openbao/openbao/api/v2"`), "openbao/openbao/api"},
		"Vault library":       {withImport(`"github.com/hashicorp/vault/api"`), "hashicorp/vault/api"},
		"cluster not checked": {strings.Replace(clean, "id.MustHave(cluster, id.Cluster)", "error(nil)", 1), "does not check the cluster"},
	} {
		t.Run(name, func(t *testing.T) {
			v := check(t, map[string]string{"bad.go": tc.src})
			if len(v) == 0 {
				t.Fatal("the checker passes a source that breaks the rule")
			}
			if !strings.Contains(strings.Join(v, "\n"), tc.want) {
				t.Errorf("violations %v do not name %q", v, tc.want)
			}
		})
	}
}

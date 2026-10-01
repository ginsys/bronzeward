package talos

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The guards read this package's source, not its behaviour: the machinery client can apply, reset,
// reboot and upgrade a node, and the only thing between it and ingestion is that this package
// never makes such a call. Each checker has a failing control on a synthetic source, so a checker
// that silently stopped matching would fail its control rather than pass the real tree.

const (
	machineryClient = "github.com/siderolabs/talos/pkg/machinery/client"
	machineryConfig = "github.com/siderolabs/talos/pkg/machinery/resources/config"
	cosiSafe        = "github.com/cosi-project/runtime/pkg/safe"
	thisPackage     = "github.com/ginsys/bronzeward/internal/talos"
)

// parsePackage parses this package's non-test files.
func parsePackage(t *testing.T) map[string][]byte {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		src, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		files[n] = src
	}
	if len(files) == 0 {
		t.Fatal("no source files")
	}
	return files
}

func parse(t *testing.T, name string, src []byte) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// exportedSurface lists a package's exported top-level names, the exported fields of its exported
// structs, every method of an exported or unexported type as "Type.Method", and the methods of its
// interfaces as "Interface.Method".
func exportedSurface(t *testing.T, files map[string][]byte) []string {
	t.Helper()
	var out []string
	for name, src := range files {
		f := parse(t, name, src)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					if d.Name.IsExported() {
						out = append(out, d.Name.Name)
					}
					continue
				}
				typ := d.Recv.List[0].Type
				if s, ok := typ.(*ast.StarExpr); ok {
					typ = s.X
				}
				out = append(out, typ.(*ast.Ident).Name+"."+d.Name.Name)
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out = append(out, s.Name.Name)
						}
						switch ty := s.Type.(type) {
						case *ast.StructType:
							for _, fl := range ty.Fields.List {
								for _, n := range fl.Names {
									if n.IsExported() {
										out = append(out, s.Name.Name+"."+n.Name)
									}
								}
								if len(fl.Names) == 0 {
									out = append(out, s.Name.Name+".<embedded>")
								}
							}
						case *ast.InterfaceType:
							for _, m := range ty.Methods.List {
								for _, n := range m.Names {
									out = append(out, s.Name.Name+"."+n.Name)
								}
								if len(m.Names) == 0 {
									out = append(out, s.Name.Name+".<embedded>")
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								out = append(out, n.Name)
							}
						}
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

var surface = []string{
	"Config", "Config.Bytes", "Config.Format", "Config.GoString", "Config.MarshalJSON",
	"Config.MarshalText", "Config.MarshalYAML", "Config.ResourceVersion", "Config.String",
	"Dial",
	"Reader", "Reader.Close", "Reader.MachineConfig", "Reader.Version",
	"Target", "Target.Endpoint", "Target.Node",
	"reader.Close", "reader.MachineConfig", "reader.Version",
}

func TestExportedSurface(t *testing.T) {
	if got := exportedSurface(t, parsePackage(t)); !slices.Equal(got, surface) {
		t.Fatalf("surface\n got %q\nwant %q", got, surface)
	}
	// Control: one more method on the concrete type is a different surface.
	control := map[string][]byte{"x.go": []byte("package talos\ntype reader struct{}\nfunc (r *reader) Apply() {}\n")}
	if got := exportedSurface(t, control); slices.Contains(got, "reader.Apply") == false || slices.Equal(got, surface) {
		t.Fatalf("control not flagged: %q", got)
	}
	t.Logf("control: a source with reader.Apply has surface %q, not the allowlist", exportedSurface(t, control))
}

// denied are the machinery client's sub-clients and mutating calls, and COSI's writers. The
// allowlists below are the guard; this list catches the names in any other position too (a
// method value, a type, a string-free reference through another identifier).
var denied = []string{
	"MachineClient", "ClusterClient", "StorageClient", "TimeClient", "InspectClient", "ImageClient",
	"DebugClient", "LifecycleClient", "Inspect",
	"ApplyConfiguration", "Bootstrap", "BlockDeviceWipe", "GenerateClientConfiguration",
	"ImagePull", "MetaDelete", "MetaWrite", "PacketCapture",
	"Reboot", "Reset", "ResetGeneric", "Restart", "Rollback", "ServiceRestart", "ServiceStart",
	"ServiceStop", "Shutdown", "Upgrade", "UpgradeWithOptions",
	"EtcdAlarmDisarm", "EtcdDefragment", "EtcdForfeitLeadership", "EtcdLeaveCluster", "EtcdRecover",
	"EtcdRemoveMemberByID",
	"Create", "Update", "Destroy", "Modify", "StateModify", "StateModifyWithResult", "Teardown",
	"AddFinalizer", "RemoveFinalizer",
}

// allowedSelectors are, per imported package, the only names this package may use from it.
var allowedSelectors = map[string][]string{
	machineryClient: {"Client", "New", "WithConfigFromFile", "WithEndpoints", "WithNode"},
	machineryConfig: {"ActiveID", "MachineConfig"},
	cosiSafe:        {"StateGetByID"},
}

// clientMembers are the only members of the stored client (the `api` field) this package uses.
var clientMembers = []string{"COSI", "Close", "Version"}

// machineryViolations reports every use of the machinery client outside the allowlist in one
// source file.
func machineryViolations(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0) // object resolution: idents bound from client.New
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	flag := func(n ast.Node, format string, args ...any) {
		out = append(out, fset.Position(n.Pos()).String()+": "+fmt.Sprintf(format, args...))
	}
	local := map[string]string{} // local package name → import path, for the machinery imports
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		if !strings.HasPrefix(p, "github.com/siderolabs/") && !strings.HasPrefix(p, "github.com/cosi-project/") {
			continue
		}
		if _, ok := allowedSelectors[p]; !ok {
			flag(im, "import %s is not allowlisted", p)
			continue
		}
		n := filepath.Base(p)
		if im.Name != nil {
			n = im.Name.Name
		}
		if n == "_" || n == "." {
			flag(im, "import %s as %s", p, n)
			continue
		}
		local[n] = p
	}

	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})

	// Identifiers bound from client.New's result.
	bound := map[*ast.Object]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		a, ok := n.(*ast.AssignStmt)
		if !ok || len(a.Rhs) != 1 {
			return true
		}
		if c, ok := a.Rhs[0].(*ast.CallExpr); ok && isPkgSel(c.Fun, local, machineryClient, "New") {
			if id, ok := a.Lhs[0].(*ast.Ident); ok && id.Obj != nil {
				bound[id.Obj] = true
			} else {
				flag(a, "client.New's result is not bound to a plain variable")
			}
		}
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if slices.Contains(denied, n.Name) {
				flag(n, "%s is denied", n.Name)
			}
			if n.Obj != nil && bound[n.Obj] {
				if _, isDef := parents[n].(*ast.AssignStmt); isDef && isLHS(parents[n].(*ast.AssignStmt), n) {
					break
				}
				switch p := parents[n].(type) {
				case *ast.SelectorExpr:
					if p.X == n && slices.Contains(clientMembers, p.Sel.Name) && p.Sel.Name != "COSI" {
						break
					}
					flag(n, "the client from client.New is used as %s.%s", n.Name, p.Sel.Name)
				case *ast.KeyValueExpr:
					lit, _ := parents[p].(*ast.CompositeLit)
					if k, ok := p.Key.(*ast.Ident); ok && k.Name == "api" && p.Value == n && lit != nil && isIdent(lit.Type, "reader") {
						break
					}
					flag(n, "the client from client.New is stored other than in reader's api field")
				default:
					flag(n, "the client from client.New escapes (%T)", p)
				}
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok {
				if p, ok := local[x.Name]; ok {
					if !slices.Contains(allowedSelectors[p], n.Sel.Name) {
						flag(n, "%s.%s is not allowlisted", x.Name, n.Sel.Name)
					}
					break
				}
			}
			if n.Sel.Name == "api" {
				outer, ok := parents[n].(*ast.SelectorExpr)
				if !ok || outer.X != n {
					flag(n, "the api field is used bare")
					break
				}
				if !slices.Contains(clientMembers, outer.Sel.Name) {
					flag(outer, "api.%s is not allowlisted", outer.Sel.Name)
				}
			}
			if n.Sel.Name == "COSI" {
				call, ok := parents[n].(*ast.CallExpr)
				if !ok || !slices.Contains(call.Args, ast.Expr(n)) || !isStateGetByID(call.Fun, local) {
					flag(n, "COSI is used other than as an argument to safe.StateGetByID")
				}
			}
		}
		return true
	})

	// The client type appears only as reader's api field.
	ast.Inspect(f, func(n ast.Node) bool {
		s, ok := n.(*ast.SelectorExpr)
		if !ok || !isPkgSel(s, local, machineryClient, "Client") {
			return true
		}
		star, ok := parents[s].(*ast.StarExpr)
		field, ok2 := parents[star].(*ast.Field)
		if !ok || !ok2 || len(field.Names) != 1 || field.Names[0].Name != "api" {
			flag(s, "client.Client used other than as the api field's type")
		}
		return true
	})
	return out
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func isLHS(a *ast.AssignStmt, id *ast.Ident) bool {
	return slices.ContainsFunc(a.Lhs, func(e ast.Expr) bool { return e == id })
}

func isPkgSel(e ast.Expr, local map[string]string, path, name string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && local[x.Name] == path && s.Sel.Name == name
}

func isStateGetByID(fun ast.Expr, local map[string]string) bool {
	switch f := fun.(type) {
	case *ast.IndexExpr:
		return isPkgSel(f.X, local, cosiSafe, "StateGetByID")
	case *ast.IndexListExpr:
		return isPkgSel(f.X, local, cosiSafe, "StateGetByID")
	}
	return isPkgSel(fun, local, cosiSafe, "StateGetByID")
}

func TestMachineryCallsAllowlisted(t *testing.T) {
	for name, src := range parsePackage(t) {
		for _, v := range machineryViolations(t, name, src) {
			t.Error(v)
		}
	}
	head := "package talos\nimport (\n\t\"context\"\n\t\"github.com/cosi-project/runtime/pkg/safe\"\n\t\"github.com/siderolabs/talos/pkg/machinery/client\"\n\tcfgres \"github.com/siderolabs/talos/pkg/machinery/resources/config\"\n)\n" +
		"var _ = context.Background\nvar _ = safe.StateGetByID[*cfgres.MachineConfig]\ntype reader struct{ api *client.Client }\n"
	controls := map[string]struct {
		body string
		want []string
	}{
		"apply and a COSI destroy": {
			"func (r *reader) f(ctx context.Context) { r.api.ApplyConfiguration(ctx, nil); r.api.COSI.Destroy(ctx, nil) }\n",
			[]string{"ApplyConfiguration is denied", "api.ApplyConfiguration is not allowlisted", "Destroy is denied", "COSI is used other than"},
		},
		"method value":          {"func (r *reader) f() { g := r.api.Reboot; _ = g }\n", []string{"api.Reboot is not allowlisted"}},
		"COSI in a variable":    {"func (r *reader) f() { s := r.api.COSI; _ = s }\n", []string{"COSI is used other than"}},
		"client passed on":      {"func (r *reader) f() { h(r.api) }\nfunc h(any) {}\n", []string{"the api field is used bare"}},
		"client behind any":     {"func (r *reader) f() any { return r.api }\n", []string{"the api field is used bare"}},
		"other safe call":       {"func (r *reader) f() { safe.StateList[*cfgres.MachineConfig](nil, nil, nil) }\n", []string{"safe.StateList is not allowlisted"}},
		"other client option":   {"var _ = client.WithDefaultConfig\n", []string{"client.WithDefaultConfig is not allowlisted"}},
		"client.New escapes":    {"func f(ctx context.Context) any { c, _ := client.New(ctx); return c }\n", []string{"escapes"}},
		"client.New elsewhere":  {"type other struct{ api any }\nfunc f(ctx context.Context) any { c, _ := client.New(ctx); return other{api: c} }\n", []string{"stored other than in reader's api field"}},
		"client.New sub-client": {"func f(ctx context.Context) { c, _ := client.New(ctx); _ = c.MachineClient }\n", []string{"MachineClient is denied", "used as c.MachineClient"}},
		"client type elsewhere": {"var other *client.Client\n", []string{"client.Client used other than"}},
		"unlisted import":       {"", nil},
	}
	for name, c := range controls {
		src := head + c.body
		if name == "unlisted import" {
			src = strings.Replace(head, "import (\n", "import (\n\t\"github.com/siderolabs/talos/pkg/machinery/api/machine\"\n", 1) + "var _ machine.ApplyConfigurationRequest\n"
			c.want = []string{"import github.com/siderolabs/talos/pkg/machinery/api/machine is not allowlisted"}
		}
		got := strings.Join(machineryViolations(t, "control.go", []byte(src)), "\n")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("control %s: no violation %q in:\n%s", name, w, got)
			}
		}
		t.Logf("control %s: flagged %d violation(s)", name, strings.Count(got, "\n")+1)
	}
	// Control for the controls: the head alone, which uses only allowlisted names, is clean.
	if got := machineryViolations(t, "control.go", []byte(head)); len(got) != 0 {
		t.Errorf("the clean control head is flagged: %q", got)
	}
}

// guarded are the imports only this package may hold: the machinery client, its machine API (the
// mutating RPCs' request types) and COSI's state and safe packages (the writers).
var guarded = []string{
	machineryClient,
	"github.com/siderolabs/talos/pkg/machinery/api/machine",
	"github.com/cosi-project/runtime/pkg/state",
	cosiSafe,
}

// moduleRoot is the directory holding go.mod, found upward from the working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		t.Fatal("no go.mod above the working directory")
	}
}

// sourceRoots are the root module's own Go trees. The walk never starts at the module root: that
// would enter experiments/ (other modules) and any worktree checked out under the root.
var sourceRoots = []string{"cmd", "internal", "fixtures/oidc"}

// walkGo calls fn with each .go file under the source roots, by its slash path relative to root.
func walkGo(t *testing.T, root string, fn func(rel string, src []byte)) {
	t.Helper()
	n := 0
	for _, r := range sourceRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			n++
			fn(filepath.ToSlash(rel), src)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 20 {
		t.Fatalf("the walk read %d files: is it rooted at the module?", n)
	}
}

func importViolations(t *testing.T, rel string, src []byte) []string {
	t.Helper()
	if strings.HasPrefix(rel, "internal/talos/") {
		return nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		if slices.Contains(guarded, p) {
			out = append(out, rel+" imports "+p)
		}
	}
	return out
}

func TestMachineryImportedOnlyHere(t *testing.T) {
	walkGo(t, moduleRoot(t), func(rel string, src []byte) {
		for _, v := range importViolations(t, rel, src) {
			t.Error(v)
		}
	})
	control := []byte("package ingest\nimport \"github.com/siderolabs/talos/pkg/machinery/client\"\nvar _ client.Client\n")
	if got := importViolations(t, "internal/ingest/x.go", control); len(got) != 1 {
		t.Fatalf("control not flagged: %q", got)
	}
	t.Logf("control: internal/ingest/x.go importing the machinery client is flagged")
}

// bytesViolations flags, in a file outside internal/talos and internal/ingest that imports this
// package, every selector named Bytes. Without type information the guard cannot tell a Config's
// Bytes from another type's, so it refuses them all in such files; none exists today.
func bytesViolations(t *testing.T, rel string, src []byte) []string {
	t.Helper()
	if strings.HasPrefix(rel, "internal/talos/") || strings.HasPrefix(rel, "internal/ingest/") {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(f.Imports, func(im *ast.ImportSpec) bool { return im.Path.Value == strconv.Quote(thisPackage) }) {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "Bytes" {
			out = append(out, fset.Position(s.Pos()).String()+": .Bytes in a file importing internal/talos")
		}
		return true
	})
	return out
}

func TestConfigBytesCallers(t *testing.T) {
	walkGo(t, moduleRoot(t), func(rel string, src []byte) {
		for _, v := range bytesViolations(t, rel, src) {
			t.Error(v)
		}
	})
	control := []byte("package api\nimport \"github.com/ginsys/bronzeward/internal/talos\"\nfunc f(c talos.Config) []byte { return c.Bytes() }\n")
	if got := bytesViolations(t, "internal/api/x.go", control); len(got) != 1 {
		t.Fatalf("control not flagged: %q", got)
	}
	if got := bytesViolations(t, "internal/ingest/x.go", control); len(got) != 0 {
		t.Fatalf("internal/ingest flagged: %q", got)
	}
	t.Logf("control: internal/api/x.go calling Config.Bytes is flagged; internal/ingest is not")
}

package talos

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The guards read this package's source, not its behaviour: the machinery client can apply, reset,
// reboot and upgrade a node, and the only thing between it and ingestion is that this package
// never makes such a call. Each checker has a failing control on a synthetic source, so a checker
// that silently stopped matching would fail its control rather than pass the real tree.

const (
	machineryClient = "github.com/siderolabs/talos/pkg/machinery/client"
	machineryConfig = "github.com/siderolabs/talos/pkg/machinery/resources/config"
	clientConfig    = "github.com/siderolabs/talos/pkg/machinery/client/config"
	cosiSafe        = "github.com/cosi-project/runtime/pkg/safe"
	cosiState       = "github.com/cosi-project/runtime/pkg/state"
	machineryHW     = "github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	machineryCl     = "github.com/siderolabs/talos/pkg/machinery/resources/cluster"
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
	"Applier", "Applier.ApplyNoReboot", "Applier.Close", "Class", "ClassAccepted", "ClassCode",
	"ClassInvalidArgument", "ClassRejected", "ClassTransport", "ClassUnrecognised",
	"Config", "Config.Bytes", "Config.Digest", "Config.Format", "Config.GoString", "Config.MarshalJSON",
	"Config.MarshalText", "Config.MarshalYAML", "Config.ResourceVersion", "Config.String",
	"ConfigurationDigest", "Dial", "DialApplier", "ErrNoDeadline", "Identity", "Identity.ClusterID",
	"Identity.NodeID", "Identity.SMBIOSUUID",
	"ParseEndpoint",
	"Reader", "Reader.Close", "Reader.Identity", "Reader.MachineConfig", "Reader.Version",
	"Response", "Response.Accounts", "Response.Class", "Response.Code", "Response.Text",
	"applier.ApplyNoReboot", "applier.Close",
	"reader.Close", "reader.Identity", "reader.MachineConfig", "reader.Version",
	"requestError.Error", "requestError.GRPCStatus", "requestError.Unwrap",
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

// allowedSelectors are, per imported package, the only names this package may use from it. The
// client is built from a talosconfig's bytes and one endpoint: no option that reads a file
// (WithConfigFromFile, WithDefaultConfig) or sends node metadata (WithNode, WithNodes) is listed
// (persistence-api §3.3).
var allowedSelectors = map[string][]string{
	machineryClient: {"Client", "New", "WithConfigContext", "WithEndpoints"},
	machineryConfig: {"ActiveID", "MachineConfig"},
	clientConfig:    {"Context", "FromBytes"},
	cosiSafe:        {"StateGetByID"},
	cosiState:       {"IsNotFoundError"},
	machineryHW:     {"SystemInformation", "SystemInformationID"},
	machineryCl:     {"Identity", "Info", "InfoID", "LocalIdentity"},
}

// clientMembers are the only members of the stored client (the `api` field) this package uses.
var clientMembers = []string{"COSI", "Close", "Version"}

// policy is what one source file may use of the machinery: per imported package the selectors, of
// the stored client (the `api` field of holder) the members, and which denied names it is exempt
// from. Reader's file has readerPolicy; the one file that sends a request has applierPolicy, and no
// other file may use ApplyConfiguration (the denied list still names it there).
type policy struct {
	selectors map[string][]string
	members   []string
	holder    string
	exempt    []string
}

var readerPolicy = policy{selectors: allowedSelectors, members: clientMembers, holder: "reader"}

const (
	machineryMachine = "github.com/siderolabs/talos/pkg/machinery/api/machine"
	applyFile        = "apply.go"
)

// applierMembers are the only members of the Applier's stored client.
var applierMembers = []string{"ApplyConfiguration", "Close"}

// applierPolicy is apply.go's: the client constructed as Reader's is, the one request type with its
// no-reboot mode constant, and the one call. applierShapeViolations holds the request to its shape.
var applierPolicy = policy{
	selectors: map[string][]string{
		machineryClient:  {"Client", "New", "NodeError", "WithConfigContext", "WithEndpoints"},
		machineryMachine: {"ApplyConfigurationRequest", "ApplyConfigurationRequest_NO_REBOOT", "ApplyConfigurationResponse"},
	},
	members: applierMembers,
	holder:  "applier",
	exempt:  []string{"ApplyConfiguration"},
}

// machineryViolations reports every use of the machinery client outside the reader allowlist in one
// source file.
func machineryViolations(t *testing.T, name string, src []byte) []string {
	t.Helper()
	return policyViolations(t, name, src, readerPolicy)
}

// policyViolations reports every use of the machinery client outside pol in one source file.
func policyViolations(t *testing.T, name string, src []byte, pol policy) []string {
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
		if _, ok := pol.selectors[p]; !ok {
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

	// Identifiers bound from client.New's result. client.New appears only as the one call of an
	// assignment to a plain variable, whose every use the next walk checks: an alias of the
	// constructor, a var declaration, or a call returned or passed on would hand the client out
	// unchecked.
	bound := map[*ast.Object]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		s, ok := n.(*ast.SelectorExpr)
		if !ok || !isPkgSel(s, local, machineryClient, "New") {
			return true
		}
		call, _ := parents[s].(*ast.CallExpr)
		a, _ := parents[call].(*ast.AssignStmt)
		if call == nil || call.Fun != s || a == nil || len(a.Rhs) != 1 {
			flag(s, "client.New is used other than as `c, err := client.New(…)`")
			return true
		}
		if id, ok := a.Lhs[0].(*ast.Ident); ok && id.Obj != nil {
			bound[id.Obj] = true
		} else {
			flag(a, "client.New's result is not bound to a plain variable")
		}
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if slices.Contains(denied, n.Name) && !slices.Contains(pol.exempt, n.Name) {
				flag(n, "%s is denied", n.Name)
			}
			if n.Obj != nil && bound[n.Obj] {
				if _, isDef := parents[n].(*ast.AssignStmt); isDef && isLHS(parents[n].(*ast.AssignStmt), n) {
					break
				}
				switch p := parents[n].(type) {
				case *ast.SelectorExpr:
					if p.X == n && slices.Contains(pol.members, p.Sel.Name) && p.Sel.Name != "COSI" {
						break
					}
					flag(n, "the client from client.New is used as %s.%s", n.Name, p.Sel.Name)
				case *ast.KeyValueExpr:
					lit, _ := parents[p].(*ast.CompositeLit)
					if k, ok := p.Key.(*ast.Ident); ok && k.Name == "api" && p.Value == n && lit != nil && isIdent(lit.Type, pol.holder) {
						break
					}
					flag(n, "the client from client.New is stored other than in %s's api field", pol.holder)
				default:
					flag(n, "the client from client.New escapes (%T)", p)
				}
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok {
				if p, ok := local[x.Name]; ok {
					if !slices.Contains(pol.selectors[p], n.Sel.Name) {
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
				if !slices.Contains(pol.members, outer.Sel.Name) {
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

// The type-checked guards follow a method to its declaration, whatever spelling reaches it: an
// aliased constructor or type, an embedding, a helper in another file, a value returned by another
// package. They read type information from export data: `go list -export -deps -test` compiles
// the module and its dependencies, as `go test` does, and names each package's export file. A value
// converted to an interface and asserted elsewhere is dynamic dispatch, which no static check
// follows; the syntactic checks above, and Config's refusal to render, stay the guard there.

type listedPackage struct {
	ImportPath, Dir, Export, ForTest   string
	GoFiles, TestGoFiles, XTestGoFiles []string
}

var listed struct {
	once sync.Once
	pkgs []listedPackage
	err  error
}

// goList lists the source roots' packages and every dependency, with export data.
func goList(t *testing.T) []listedPackage {
	t.Helper()
	root := moduleRoot(t)
	listed.once.Do(func() {
		args := []string{"list", "-export", "-deps", "-test", "-json"}
		for _, r := range sourceRoots {
			args = append(args, "./"+r+"/...")
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			listed.err = fmt.Errorf("go list: %v\n%s", err, stderr.Bytes())
			return
		}
		for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
			var p listedPackage
			if listed.err = dec.Decode(&p); listed.err != nil {
				return
			}
			listed.pkgs = append(listed.pkgs, p)
		}
	})
	if listed.err != nil {
		t.Fatal(listed.err)
	}
	return listed.pkgs
}

// listedExports maps each listed entry's import path to its export file.
func listedExports(t *testing.T) map[string]string {
	t.Helper()
	exports := map[string]string{}
	for _, p := range goList(t) {
		exports[p.ImportPath] = p.Export
	}
	return exports
}

// typeCheck checks one package's source, reading its imports from the export files named in
// exports; under maps an import path to another entry (an external test imports the package as
// compiled for its tests), and given supplies packages checked from synthetic source.
func typeCheck(t *testing.T, fset *token.FileSet, path string, files []*ast.File, exports, under map[string]string, given map[string]*types.Package) (*types.Package, *types.Info) {
	t.Helper()
	gc := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		if v, ok := under[path]; ok {
			path = v
		}
		if exports[path] == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(exports[path])
	})
	conf := types.Config{Importer: importerFunc(func(path string) (*types.Package, error) {
		if p, ok := given[path]; ok {
			return p, nil
		}
		return gc.Import(path)
	})}
	info := &types.Info{
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Uses:       map[*ast.Ident]types.Object{},
	}
	pkg, err := conf.Check(path, fset, files, info)
	if err != nil {
		t.Fatalf("type-checking %s: %v", path, err)
	}
	return pkg, info
}

type importerFunc func(string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

func parseSources(t *testing.T, fset *token.FileSet, srcs map[string][]byte) []*ast.File {
	t.Helper()
	var files []*ast.File
	for name, src := range srcs {
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return files
}

// memberAllowlist are, per package, the only fields and methods of its types this package may
// select: of the machinery client, those of clientMembers; of COSI's state, none (COSI only
// reaches safe.StateGetByID).
var memberAllowlist = map[string][]string{
	machineryClient: clientMembers,
	"github.com/cosi-project/runtime/pkg/state": nil,
}

// typedMachineryViolations reports every selection of a machinery client or COSI state member
// outside memberAllowlist, by the member's declaration.
func typedMachineryViolations(t *testing.T, srcs map[string][]byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	_, info := typeCheck(t, fset, thisPackage, parseSources(t, fset, srcs), listedExports(t), nil, nil)
	var out []string
	for sel, s := range info.Selections {
		obj := s.Obj()
		if obj.Pkg() == nil {
			continue
		}
		allowed, ok := memberAllowlist[obj.Pkg().Path()]
		if ok && obj.Pkg().Path() == machineryClient && filepath.Base(fset.Position(sel.Pos()).Filename) == applyFile {
			allowed = applierMembers
		}
		if ok && !slices.Contains(allowed, obj.Name()) {
			out = append(out, fmt.Sprintf("%s: %s member %s is not allowlisted", fset.Position(sel.Sel.Pos()), obj.Pkg().Path(), obj.Name()))
		}
	}
	slices.Sort(out)
	return out
}

// applierShapeViolations holds the one file that sends a request to its shape: exactly one
// ApplyConfiguration call, exactly one request literal, whose only keys are Data and Mode, the mode
// being the no-reboot constant, and the call's one option `grpc.MaxRetryRPCBufferSize(0)`, which
// commits the attempt when the request is sent so grpc-go cannot replay it (it re-sends a first
// attempt whose stream was reset with REFUSED_STREAM, and the dial option that disables retry does
// not stop that). The policy check bounds what the file may name; this bounds what it may send
// with it (a dry run, a try-mode timeout and another mode are all fields or constants of the
// allowed request type).
func applierShapeViolations(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	flag := func(n ast.Node, format string, args ...any) {
		out = append(out, fset.Position(n.Pos()).String()+": "+fmt.Sprintf(format, args...))
	}
	isSel := func(e ast.Expr, pkg, sel string) bool {
		s, ok := e.(*ast.SelectorExpr)
		if !ok || s.Sel.Name != sel {
			return false
		}
		x, ok := s.X.(*ast.Ident)
		return ok && x.Name == pkg
	}
	// inline is the request literal of a call's second argument: `&machine.ApplyConfigurationRequest{…}`
	// written in the call, so no variable holds the request between its construction and the send.
	inline := func(e ast.Expr) bool {
		u, ok := e.(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			return false
		}
		lit, ok := u.X.(*ast.CompositeLit)
		return ok && isSel(lit.Type, "machine", "ApplyConfigurationRequest")
	}
	// noReplay is the call option `grpc.MaxRetryRPCBufferSize(0)`: any other size leaves the request
	// in the replay buffer, where a refused stream is sent again.
	noReplay := func(e ast.Expr) bool {
		c, ok := e.(*ast.CallExpr)
		if !ok || !isSel(c.Fun, "grpc", "MaxRetryRPCBufferSize") || len(c.Args) != 1 {
			return false
		}
		lit, ok := c.Args[0].(*ast.BasicLit)
		return ok && lit.Kind == token.INT && lit.Value == "0"
	}
	selections, calls, literals := 0, 0, 0
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if n.Sel.Name == "ApplyConfiguration" {
				selections++
			}
		case *ast.CallExpr:
			// The one send: the member called directly on the stored client (`a.api.ApplyConfiguration`),
			// never a method value that is called later or twice, with the request built in the call.
			s, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || s.Sel.Name != "ApplyConfiguration" {
				return true
			}
			if x, ok := s.X.(*ast.SelectorExpr); !ok || x.Sel.Name != "api" || len(n.Args) != 3 || !inline(n.Args[1]) || !noReplay(n.Args[2]) {
				flag(n, "ApplyConfiguration is called other than as `x.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{…}, grpc.MaxRetryRPCBufferSize(0))`")
				return true
			}
			calls++
		case *ast.CompositeLit:
			if !isSel(n.Type, "machine", "ApplyConfigurationRequest") {
				return true
			}
			literals++
			keys := map[string]bool{}
			for _, e := range n.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				k, isIdent := ast.Expr(nil), false
				if ok {
					k, isIdent = kv.Key, true
				}
				id, ok2 := k.(*ast.Ident)
				if !isIdent || !ok2 || (id.Name != "Data" && id.Name != "Mode") {
					flag(e, "the request sets a field other than Data and Mode")
					continue
				}
				keys[id.Name] = true
				if id.Name == "Mode" && !isSel(kv.Value, "machine", "ApplyConfigurationRequest_NO_REBOOT") {
					flag(e, "the request's mode is not machine.ApplyConfigurationRequest_NO_REBOOT")
				}
			}
			if !keys["Data"] || !keys["Mode"] {
				flag(n, "the request does not set both Data and Mode")
			}
		}
		return true
	})
	if selections != 1 || calls != 1 {
		out = append(out, fmt.Sprintf("%s: %d ApplyConfiguration selections and %d direct calls with an inline request; want exactly one of each", name, selections, calls))
	}
	if literals != 1 {
		out = append(out, fmt.Sprintf("%s: %d request literals; want exactly one", name, literals))
	}
	return out
}

func TestMachineryCallsAllowlisted(t *testing.T) {
	sawApplier := false
	for name, src := range parsePackage(t) {
		if name == applyFile {
			sawApplier = true
			for _, v := range append(policyViolations(t, name, src, applierPolicy), applierShapeViolations(t, name, src)...) {
				t.Error(v)
			}
			continue
		}
		for _, v := range machineryViolations(t, name, src) {
			t.Error(v)
		}
	}
	if !sawApplier {
		t.Errorf("%s is not in the package: the applier guard checked nothing", applyFile)
	}
	for _, v := range typedMachineryViolations(t, parsePackage(t)) {
		t.Error(v)
	}

	// Controls for the applier's file: each is a body of apply.go the real one must not become.
	applierHead := "package talos\nimport (\n\t\"context\"\n\t\"github.com/siderolabs/talos/pkg/machinery/api/machine\"\n\t\"github.com/siderolabs/talos/pkg/machinery/client\"\n\t\"google.golang.org/grpc\"\n)\n" +
		"var _ = context.Background\nvar _ = grpc.MaxRetryRPCBufferSize\nvar _ machine.ApplyConfigurationRequest\ntype applier struct{ api *client.Client }\n"
	const noReplay = ", grpc.MaxRetryRPCBufferSize(0)"
	okBody := "func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}" + noReplay + ") }\n"
	if got := append(policyViolations(t, applyFile, []byte(applierHead+okBody), applierPolicy), applierShapeViolations(t, applyFile, []byte(applierHead+okBody))...); len(got) != 0 {
		t.Errorf("the clean applier control is flagged: %q", got)
	}
	for name, c := range map[string]struct {
		body string
		want string
	}{
		"dry run":       {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT, DryRun: true}" + noReplay + ") }\n", "a field other than Data and Mode"},
		"try mode":      {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_TRY}" + noReplay + ") }\n", "is not machine.ApplyConfigurationRequest_NO_REBOOT"},
		"reboot mode":   {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_REBOOT}" + noReplay + ") }\n", "is not machine.ApplyConfigurationRequest_NO_REBOOT"},
		"mode left out": {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b}" + noReplay + ") }\n", "does not set both Data and Mode"},
		"positional":    {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{}" + noReplay + ") }\n", "does not set both Data and Mode"},
		"a second call": {okBody + strings.Replace(okBody, "f(", "g(", 1), "2 ApplyConfiguration selections"},
		"a method value sent twice": {"func (a *applier) f(ctx context.Context, b []byte) {\n\tsend := a.api.ApplyConfiguration\n" +
			"\treq := &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}\n\treq.DryRun = true\n\tsend(ctx, req)\n\tsend(ctx, req)\n}\n", "0 direct calls"},
		"a request held in a variable": {"func (a *applier) f(ctx context.Context, b []byte) {\n" +
			"\treq := &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}\n\treq.TryModeTimeout = nil\n\ta.api.ApplyConfiguration(ctx, req" + noReplay + ")\n}\n", "is called other than as"},
		"no replay guard":          {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}) }\n", "is called other than as"},
		"a retry buffer left open": {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}, grpc.MaxRetryRPCBufferSize(1024)) }\n", "is called other than as"},
		"another call option":      {"func (a *applier) f(ctx context.Context, b []byte) { a.api.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{Data: b, Mode: machine.ApplyConfigurationRequest_NO_REBOOT}, grpc.WaitForReady(true)) }\n", "is called other than as"},
		"a reboot":                 {okBody + "func (a *applier) r(ctx context.Context) { a.api.Reboot(ctx) }\n", "Reboot is denied"},
		"a reset":                  {okBody + "func (a *applier) r(ctx context.Context) { a.api.Reset(ctx, false) }\n", "Reset is denied"},
		"a read":                   {okBody + "func (a *applier) r(ctx context.Context) { a.api.Version(ctx) }\n", "api.Version is not allowlisted"},
		"a sub-client":             {okBody + "func (a *applier) r() { _ = a.api.MachineClient }\n", "MachineClient is denied"},
		"client escapes":           {okBody + "func g(a *applier) any { return a.api }\n", "the api field is used bare"},
	} {
		src := []byte(applierHead + c.body)
		got := strings.Join(append(policyViolations(t, applyFile, src, applierPolicy), applierShapeViolations(t, applyFile, src)...), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("applier control %s: no violation %q in:\n%s", name, c.want, got)
		}
		t.Logf("applier control %s: flagged", name)
	}
	// The exemption is the file's name: the same sending body anywhere else is the denied call, and
	// a typed selection of the call in another file is outside the reader's members.
	if got := machineryViolations(t, "other.go", []byte(applierHead+okBody)); len(got) == 0 {
		t.Error("control: a sender outside apply.go is not flagged")
	}
	if got := typedMachineryViolations(t, map[string][]byte{"control.go": []byte("package talos\nimport (\n\t\"context\"\n\t\"github.com/siderolabs/talos/pkg/machinery/client\"\n)\nfunc f(ctx context.Context, c *client.Client) { c.ApplyConfiguration(ctx, nil) }\n")}); len(got) != 1 {
		t.Errorf("typed control: ApplyConfiguration outside apply.go: %q, want one violation", got)
	}
	if got := typedMachineryViolations(t, map[string][]byte{applyFile: []byte("package talos\nimport (\n\t\"context\"\n\t\"github.com/siderolabs/talos/pkg/machinery/client\"\n)\nfunc f(ctx context.Context, c *client.Client) { c.RebootWithResponse(ctx) }\n")}); len(got) != 1 {
		t.Errorf("typed control: another member in apply.go: %q, want one violation", got)
	}
	// Typed controls: each type-checks, and reaches a member outside the allowlist by a spelling
	// the syntactic checks do not resolve.
	typedHead := "package talos\nimport (\n\t\"context\"\n\t\"github.com/siderolabs/talos/pkg/machinery/client\"\n)\nvar _ = context.Background\n"
	for name, body := range map[string]string{
		"constructor aliased": "func f(ctx context.Context) { mk := client.New; c, _ := mk(ctx); c.RebootWithResponse(ctx) }\n",
		"client embedded":     "type wrap struct{ *client.Client }\nfunc (w wrap) f(ctx context.Context) { w.Kubeconfig(ctx) }\n",
		"helper in a file":    "func get(ctx context.Context) *client.Client { c, _ := client.New(ctx); return c }\nfunc f(ctx context.Context) { get(ctx).EtcdStatus(ctx) }\n",
	} {
		got := typedMachineryViolations(t, map[string][]byte{"control.go": []byte(typedHead + body)})
		if len(got) != 1 {
			t.Errorf("typed control %s: %q, want one violation", name, got)
		}
		t.Logf("typed control %s: %q", name, got)
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
		"talosconfig path":      {"var _ = client.WithConfigFromFile\n", []string{"client.WithConfigFromFile is not allowlisted"}},
		"node metadata":         {"func f(ctx context.Context) { _ = client.WithNode(ctx, \"n\"); _ = client.WithNodes(ctx, \"n\") }\n", []string{"client.WithNode is not allowlisted", "client.WithNodes is not allowlisted"}},
		"client.New escapes":    {"func f(ctx context.Context) any { c, _ := client.New(ctx); return c }\n", []string{"escapes"}},
		"client.New elsewhere":  {"type other struct{ api any }\nfunc f(ctx context.Context) any { c, _ := client.New(ctx); return other{api: c} }\n", []string{"stored other than in reader's api field"}},
		"client.New sub-client": {"func f(ctx context.Context) { c, _ := client.New(ctx); _ = c.MachineClient }\n", []string{"MachineClient is denied", "used as c.MachineClient"}},
		"client type elsewhere": {"var other *client.Client\n", []string{"client.Client used other than"}},
		"constructor aliased":   {"func f(ctx context.Context) { mk := client.New; c, _ := mk(ctx); c.RebootWithResponse(ctx) }\n", []string{"client.New is used other than"}},
		"client.New in a var":   {"var c, _ = client.New(nil)\n", []string{"client.New is used other than"}},
		"client.New passed on":  {"func f(ctx context.Context) any { return must(client.New(ctx)) }\nfunc must(a any, _ error) any { return a }\n", []string{"client.New is used other than"}},
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

const modulePath = "github.com/ginsys/bronzeward"

// bytesViolations reports every reference to this package's Bytes method (Config.Bytes) — a call,
// a method value or a method expression, by any spelling of the receiver — in the package at
// path, unless it is this package or internal/ingest.
func bytesViolations(fset *token.FileSet, path string, info *types.Info) []string {
	for _, ok := range []string{thisPackage, modulePath + "/internal/ingest"} {
		if path == ok || strings.HasPrefix(path, ok+"/") {
			return nil
		}
	}
	var out []string
	for sel, s := range info.Selections {
		if f, ok := s.Obj().(*types.Func); ok && f.Pkg() != nil && f.Pkg().Path() == thisPackage && f.Name() == "Bytes" {
			out = append(out, fmt.Sprintf("%s: Config.Bytes in %s", fset.Position(sel.Sel.Pos()), path))
		}
	}
	return out
}

// senderFile reports whether pos is in internal/api's dispatch sender, the one file outside
// internal/talos and internal/provider that may send a mutating request or read the artifact.
func senderFile(fset *token.FileSet, path string, pos token.Pos) bool {
	return path == modulePath+"/internal/api" && filepath.Base(fset.Position(pos).Filename) == "sender.go"
}

// artifactBytesViolations reports every reference to provider.ArtifactBytes — the one way out of a
// decrypted artifact, a function so that no interface can reach it — by any spelling (a call, a
// function value, an alias of the package), in the package at path, unless it is that package or
// the dispatch sender's file in internal/api: the plaintext reaches a request only there.
func artifactBytesViolations(fset *token.FileSet, path string, info *types.Info) []string {
	providerPkg := modulePath + "/internal/provider"
	if path == providerPkg {
		return nil
	}
	var out []string
	for id, obj := range info.Uses {
		f, ok := obj.(*types.Func)
		if !ok || f.Pkg() == nil || f.Pkg().Path() != providerPkg || f.Name() != "ArtifactBytes" {
			continue
		}
		if senderFile(fset, path, id.Pos()) {
			continue
		}
		out = append(out, fmt.Sprintf("%s: provider.ArtifactBytes in %s", fset.Position(id.Pos()), path))
	}
	return out
}

// applyNoRebootViolations reports every selection of a method named ApplyNoReboot — a call, a
// method value or a method expression, on Applier, its implementation or any interface or type
// that mirrors it — in the package at path, unless it is internal/talos (and its tests) or the
// dispatch sender's file in internal/api. Constructing an Applier is already held to the sender
// (dialApplierViolations); this holds the use of one, which a value passed on would otherwise
// reach from anywhere. The name is matched, not the package, so a local interface with the same
// method cannot stand in for the real one.
func applyNoRebootViolations(fset *token.FileSet, path string, info *types.Info) []string {
	if path == thisPackage || strings.HasPrefix(path, thisPackage+"/") {
		return nil
	}
	var out []string
	for sel, s := range info.Selections {
		if s.Obj().Name() != "ApplyNoReboot" || senderFile(fset, path, sel.Sel.Pos()) {
			continue
		}
		out = append(out, fmt.Sprintf("%s: ApplyNoReboot in %s", fset.Position(sel.Sel.Pos()), path))
	}
	return out
}

// packageViolations type-checks a listed package, with its in-package tests, and its external test
// package, reading imports from exports, and reports their references to Config.Bytes. The
// external test imports the `p [p.test]` variant when the listing has one (go list -test makes it
// for a package with in-package tests, and for a main package even without them); otherwise it
// imports p as built.
func packageViolations(t *testing.T, exports map[string]string, p listedPackage) []string {
	t.Helper()
	var out []string
	for path, names := range map[string][]string{p.ImportPath: append(slices.Clone(p.GoFiles), p.TestGoFiles...), p.ImportPath + "_test": p.XTestGoFiles} {
		if len(names) == 0 {
			continue
		}
		srcs := map[string][]byte{}
		for _, name := range names {
			src, err := os.ReadFile(filepath.Join(p.Dir, name))
			if err != nil {
				t.Fatal(err)
			}
			srcs[filepath.Join(p.Dir, name)] = src
		}
		fset := token.NewFileSet()
		var under map[string]string
		if variant := p.ImportPath + " [" + p.ImportPath + ".test]"; path != p.ImportPath && exports[variant] != "" {
			under = map[string]string{p.ImportPath: variant}
		}
		_, info := typeCheck(t, fset, path, parseSources(t, fset, srcs), exports, under, nil)
		out = append(out, bytesViolations(fset, p.ImportPath, info)...)
		out = append(out, artifactBytesViolations(fset, p.ImportPath, info)...)
		out = append(out, applyNoRebootViolations(fset, p.ImportPath, info)...)
	}
	return out
}

func TestConfigBytesCallers(t *testing.T) {
	pkgs := goList(t)
	exports := listedExports(t)
	n := 0
	for _, p := range pkgs {
		if p.ForTest != "" || !strings.HasPrefix(p.ImportPath, modulePath+"/") || strings.ContainsAny(p.ImportPath, " ") || strings.HasSuffix(p.ImportPath, ".test") {
			continue
		}
		n++
		for _, v := range packageViolations(t, exports, p) {
			t.Error(v)
		}
	}
	if n < 10 {
		t.Fatalf("checked %d of the module's packages: is the listing rooted at the module?", n)
	}

	// Controls: an external test package of internal/api, from synthetic source. With the listing's
	// `api [api.test]` entry it imports that variant, which alone has the in-package tests'
	// TestReplay; with the entry taken out, as for a package whose tests are all external, it
	// imports api as built. Each still has its Config.Bytes call flagged.
	api := modulePath + "/internal/api"
	xtest := func(src string) listedPackage {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return listedPackage{ImportPath: api, Dir: dir, XTestGoFiles: []string{"x_test.go"}}
	}
	leak := "package api_test\nimport (\n\t\"" + api + "\"\n\t\"" + thisPackage + "\"\n)\nfunc leak(c talos.Config) []byte { return c.Bytes() }\n"
	if got := packageViolations(t, exports, xtest(leak+"var _ = api.TestReplay\n")); len(got) != 1 {
		t.Errorf("control: an external test using an in-package test's name: %q", got)
	}
	external := maps.Clone(exports)
	delete(external, api+" ["+api+".test]")
	if got := packageViolations(t, external, xtest(leak+"var _ = api.New\n")); len(got) != 1 {
		t.Errorf("control: an external test of a package without in-package tests: %q", got)
	}

	// Controls: each type-checks and reaches Config.Bytes without naming it in a file that imports
	// this package, or in a package that imports it at all.
	imp := "import \"" + thisPackage + "\"\n"
	check := func(path string, given map[string]*types.Package, srcs ...string) ([]string, *types.Package) {
		files := map[string][]byte{}
		for i, s := range srcs {
			files[fmt.Sprintf("control%d.go", i)] = []byte(s)
		}
		fset := token.NewFileSet()
		pkg, info := typeCheck(t, fset, path, parseSources(t, fset, files), exports, nil, given)
		return bytesViolations(fset, path, info), pkg
	}
	ingest, holder := modulePath+"/internal/ingest", modulePath+"/internal/holder"
	alias := []string{"package api\n" + imp + "type observed = talos.Config\n", "package api\nfunc leak(c observed) []byte { return c.Bytes() }\n"}
	if got, _ := check(api, nil, alias...); len(got) != 1 {
		t.Errorf("control: an alias in another file: %q", got)
	}
	if got, _ := check(api, nil, "package api\n"+imp+"var leak = talos.Config.Bytes\n"); len(got) != 1 {
		t.Errorf("control: a method expression: %q", got)
	}
	_, hp := check(holder, nil, "package holder\n"+imp+"type Holder struct{ talos.Config }\nfunc Get() Holder { return Holder{} }\n")
	if got, _ := check(api, map[string]*types.Package{holder: hp}, "package api\nimport \""+holder+"\"\nfunc leak() []byte { return holder.Get().Bytes() }\n"); len(got) != 1 {
		t.Errorf("control: an embedding from a package that does not import talos: %q", got)
	}
	if got, _ := check(ingest, nil, strings.ReplaceAll(alias[0], "package api", "package ingest"), strings.ReplaceAll(alias[1], "package api", "package ingest")); len(got) != 0 {
		t.Errorf("internal/ingest flagged: %q", got)
	}
	t.Logf("checked %d packages; controls: an external test with and without in-package tests, an alias in another file, a method expression and an embedded Config from another package are flagged; internal/ingest is not", n)

	// Controls for provider.ArtifactBytes and ApplyNoReboot: the decrypted artifact's bytes leave
	// provider, and a mutating request is sent, only in the dispatch sender's file. Each control
	// type-checks against the real provider and talos packages.
	providerPkg := modulePath + "/internal/provider"
	typed := func(path string, files map[string]string, check func(*token.FileSet, string, *types.Info) []string) []string {
		srcs := map[string][]byte{}
		for name, s := range files {
			srcs[name] = []byte(s)
		}
		fset := token.NewFileSet()
		_, info := typeCheck(t, fset, path, parseSources(t, fset, srcs), exports, nil, nil)
		return check(fset, path, info)
	}
	use := "package api\nimport \"" + providerPkg + "\"\nfunc Send(a provider.Artifact) []byte { return provider.ArtifactBytes(a) }\n"
	if got := typed(api, map[string]string{"sender.go": use}, artifactBytesViolations); len(got) != 0 {
		t.Errorf("control: internal/api's sender.go is flagged: %q", got)
	}
	for name, files := range map[string]map[string]string{
		"another file in internal/api": {"other.go": use},
		"a function value":             {"other.go": "package api\nimport \"" + providerPkg + "\"\nvar leak = provider.ArtifactBytes\n"},
		"an aliased import":            {"other.go": "package api\nimport p \"" + providerPkg + "\"\nfunc leak(a p.Artifact) []byte { return p.ArtifactBytes(a) }\n"},
		"an aliased type":              {"sender.go": "package api\n", "other.go": "package api\nimport \"" + providerPkg + "\"\ntype a2 = provider.Artifact\nfunc leak(a a2) []byte { return provider.ArtifactBytes(a) }\n"},
	} {
		if got := typed(api, files, artifactBytesViolations); len(got) != 1 {
			t.Errorf("control %s: %q, want one violation", name, got)
		}
	}
	if got := typed(modulePath+"/internal/ingest", map[string]string{"sender.go": strings.Replace(use, "package api", "package ingest", 1)}, artifactBytesViolations); len(got) != 1 {
		t.Errorf("control: a file named sender.go outside internal/api: %q, want one violation", got)
	}

	// An Applier's use: the sender's file only; any other file, a function that takes one, a method
	// value or expression, and an interface that mirrors it are flagged.
	send := "package api\nimport (\n\t\"context\"\n\t\"" + thisPackage + "\"\n)\nvar _ = context.Background\n"
	sendCall := send + "func Send(ctx context.Context, a talos.Applier, b []byte) (talos.Response, error) { return a.ApplyNoReboot(ctx, b) }\n"
	if got := typed(api, map[string]string{"sender.go": sendCall}, applyNoRebootViolations); len(got) != 0 {
		t.Errorf("control: internal/api's sender.go is flagged for ApplyNoReboot: %q", got)
	}
	for name, files := range map[string]map[string]string{
		"a function that takes an Applier": {"sender.go": "package api\n", "adopter.go": sendCall},
		"a method value":                   {"adopter.go": send + "func leak(a talos.Applier) any { return a.ApplyNoReboot }\n"},
		"a method expression":              {"adopter.go": send + "var leak = talos.Applier.ApplyNoReboot\n"},
		"a mirroring interface": {"adopter.go": send + "type sink interface {\n\tApplyNoReboot(context.Context, []byte) (talos.Response, error)\n}\n" +
			"func leak(ctx context.Context, s sink) { s.ApplyNoReboot(ctx, nil) }\n"},
	} {
		if got := typed(api, files, applyNoRebootViolations); len(got) != 1 {
			t.Errorf("control %s: %q, want one violation", name, got)
		}
	}
	if got := typed(thisPackage, map[string]string{"x.go": "package talos\nimport \"context\"\ntype sender interface{ ApplyNoReboot(context.Context, []byte) error }\nfunc leak(ctx context.Context, a sender) { a.ApplyNoReboot(ctx, nil) }\n"}, applyNoRebootViolations); len(got) != 0 {
		t.Errorf("control: internal/talos is flagged: %q", got)
	}
}

// dialApplierViolations reports every reference to this package's DialApplier — the one way to
// obtain a client that sends a mutating request — outside internal/talos and the dispatch sender's
// file, and every dot-import of this package outside internal/talos (which would hide the
// qualifier).
func dialApplierViolations(t *testing.T, rel string, src []byte) []string {
	t.Helper()
	if strings.HasPrefix(rel, "internal/talos/") || rel == "internal/api/sender.go" {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	local := ""
	var out []string
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p != thisPackage {
			continue
		}
		local = "talos"
		if im.Name != nil {
			local = im.Name.Name
		}
		if local == "." {
			out = append(out, rel+" dot-imports "+thisPackage)
		}
	}
	if local == "" {
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "DialApplier" {
			out = append(out, fmt.Sprintf("%s: DialApplier outside internal/api/sender.go", fset.Position(s.Sel.Pos())))
		}
		return true
	})
	return out
}

func TestDialApplierCallers(t *testing.T) {
	walkGo(t, moduleRoot(t), func(rel string, src []byte) {
		for _, v := range dialApplierViolations(t, rel, src) {
			t.Error(v)
		}
	})
	call := "package api\nimport \"" + thisPackage + "\"\nvar _ = talos.DialApplier\n"
	for name, c := range map[string]struct{ rel, src string }{
		"another file in internal/api": {"internal/api/adopter.go", call},
		"a command":                    {"cmd/bronzeward/main.go", strings.Replace(call, "package api", "package main", 1)},
		"an aliased import":            {"internal/ingest/x.go", "package ingest\nimport tc \"" + thisPackage + "\"\nvar _ = tc.DialApplier\n"},
		"a dot import":                 {"internal/ingest/x.go", "package ingest\nimport . \"" + thisPackage + "\"\n"},
		"a test file":                  {"internal/api/sender_test.go", call},
	} {
		if got := dialApplierViolations(t, c.rel, []byte(c.src)); len(got) == 0 {
			t.Errorf("control %s: not flagged", name)
		}
	}
	if got := dialApplierViolations(t, "internal/api/sender.go", []byte(call)); len(got) != 0 {
		t.Errorf("control: the sender's file is flagged: %q", got)
	}
	if got := dialApplierViolations(t, "internal/api/adopter.go", []byte("package api\nimport \""+thisPackage+"\"\nvar _ = talos.Dial\n")); len(got) != 0 {
		t.Errorf("control: Dial is flagged: %q", got)
	}
}

// Command bronzewardctl is the operator's client for a Bronzeward server's API
// (docs/spec/operator-experience.md §4, docs/spec/client-contracts.md). It holds no server code:
// internal/client/closure_test.go checks its dependency closure.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/ginsys/bronzeward/internal/client"
)

const usage = `usage: bronzewardctl [global flags] <command> [arguments]

commands:
  context list | use <name> | remove <name>
  context add <name> --server <url> [--certificate-authority <file>] [--automation-token-file <path>]
  context add <name> --server <url> --issuer <url> --client-id <id> [--audience <aud>] [--scopes a,b]
              [--oidc-certificate-authority <file>] [--oidc-token-file <path>]
  context add <name> --from <url> [--server <url>] [--certificate-authority <file>] [--scopes a,b]
              [--oidc-certificate-authority <file>] [--oidc-token-file <path>]
              the issuer, audience, CLI client ID and scopes from <url>/bronzeward-client.json
  version     this client's version, the server's and its API revision
  whoami      the identity, roles and epoch the server sees, and this client's reading of a sign-in
  login [--device]
              sign in to an oidc context in this host's browser, or with --device on any device
  logout      remove an oidc context's stored sign-in and revoke its refresh token
  status      start mode, recovery mode, open drift and unresolved operations
  cluster list | show <id>
  cluster add --name <name> --endpoint <https url> --contract <v1.x> --talos-cluster-id <id>
  machine list [--cluster <id>] | show <id> | timeline <id> | observations <id>
  machine add --cluster <id> (--talos-node-id <id> | --smbios-uuid <uuid>) [--serial <s>]
              --platform metal|container|cloud --talos-endpoint <ip>[:<port>]
  machine set-endpoint <id> --talos-endpoint <ip>[:<port>]
              writes, by a person signed in as author, repeat a write whose outcome was unknown
              exactly with --idempotency-key, --expect-epoch and --expect-principal as printed
  fragment | profile | assignment show <head or revision ID> | history <head ID>
  draft list [--cluster <id>] | show <draft>
  draft create --cluster <id> --title <title>
  draft discard <draft>
  profile set <name> --draft <draft> --fragments <name or frv_ ID>[,...]
              a name pins the revision the draft proposes, else the fragment's head revision
  assignment set <machine> --draft <draft> [--profiles <name>[,...]]
              [--fragment <layer>=<name>[,...]]... the layer global, site, cluster, role,
              workload or override
  profile | assignment rm <name or machine> --draft <draft>
              a write to a draft sends the ETag of the last draft show or write of that draft, or
              --if-match <etag>; if the draft changed since, it saves nothing and says so
              draft writes take author; repeat one with the outcome-unknown block's flags,
              --if-match included
  release list [--cluster <id>] [--draft <id>] | show <release>
  plan list [--state <s>] [--machine <id>] | show <plan>
  operation list [--plan <id>] [--machine <id>] [--state <s>] [--kind <k>] | show <operation>
  act list    the audit record
              lists, timeline, observations and history read one page: --limit <n>, --cursor <next>;
              --all reads every page, which -o json and yaml print as one {"items": [...]}; the
              draft and release filters apply to the items read

global flags, before or after the command:
  --context <name>      else BRONZEWARD_CONTEXT, else the current context
  -o table|json|yaml    json and yaml print the API resource unchanged
  --token-file <path>   the automation token, from an owner-only file; else the context's
                        automation.tokenFile, else the path in BRONZEWARD_TOKEN_FILE
  --token-stdin         the automation token, from standard input
  --no-input            never prompt
  -v                    name a proxy a request goes through

exit codes: 0 success; 1 refused or failed; 2 usage error or refused repeat; 3 outcome unknown`

type usageError struct{ error }

type env struct {
	context, output, tokenFile   string
	tokenStdin, noInput, verbose bool
	out, err                     io.Writer
}

func main() {
	if err := harden(); err != nil {
		fmt.Fprintln(os.Stderr, "bronzewardctl: disabling core dumps and tracebacks:", err)
		os.Exit(1)
	}
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "bronzewardctl: internal error")
			os.Exit(1)
		}
	}()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// harden runs before any token is read (client-contracts.md §11.2 item 5). The traceback level
// cannot be lowered below the one the process started with, so the process starts again with
// GOTRACEBACK=none, and a panic in any goroutine prints no stack values. Core dumps are disabled
// after that start, since starting again makes a process dumpable.
func harden() error {
	if os.Getenv("GOTRACEBACK") != "none" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		env := slices.DeleteFunc(os.Environ(), func(v string) bool { return strings.HasPrefix(v, "GOTRACEBACK=") })
		return syscall.Exec(exe, os.Args, append(env, "GOTRACEBACK=none"))
	}
	// net/http logs to the standard logger text a peer sent, an unsolicited response quoted for one.
	// bronzewardctl itself does not use that logger.
	log.SetOutput(io.Discard)
	return noCoreDumps()
}

func run(args []string, stdout, stderr io.Writer) int {
	e := &env{output: "table", out: stdout, err: stderr}
	commands := map[string]func([]string) error{"context": e.contextCmd, "version": e.versionCmd, "whoami": e.whoamiCmd, "login": e.loginCmd, "logout": e.logoutCmd,
		"status": e.statusCmd, "cluster": e.clusterCmd, "machine": e.machineCmd,
		"fragment": e.fragmentCmd, "profile": e.profileCmd, "assignment": e.assignmentCmd,
		"draft": e.draftCmd, "release": e.releaseCmd, "plan": e.planCmd, "operation": e.operationCmd, "act": e.actCmd}
	pos, err := e.parse("bronzewardctl", args, -1, nil)
	if err == nil && len(pos) == 0 {
		err = usageError{errors.New("no command")}
	} else if err == nil && commands[pos[0]] == nil {
		err = usageError{fmt.Errorf("unknown command %q", pos[0])}
	} else if err == nil {
		err = commands[pos[0]](pos[1:])
	}
	var u usageError
	var refused client.RefusedRepeat
	var unknown *client.Unknown
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintln(stdout, usage)
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(stderr, "bronzewardctl: %v\n\n%s\n", linesShown{err}, usage)
		return 2
	}
	// The last guard: an error that reaches here from a path nobody escaped shows no control
	// character but the line feeds of the CLI's own blocks.
	fmt.Fprintln(stderr, "bronzewardctl:", linesShown{err})
	switch {
	case errors.As(err, &unknown):
		return 3
	case errors.As(err, &refused):
		return 2
	}
	return 1
}

// parse reads the global flags and extra's anywhere among args and wants n positional arguments;
// with n < 0 it stops at the first one, the command, whose own flags follow.
func (e *env) parse(name string, args []string, n int, extra func(*flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&e.context, "context", e.context, "")
	fs.StringVar(&e.output, "o", e.output, "")
	fs.StringVar(&e.tokenFile, "token-file", e.tokenFile, "")
	fs.BoolVar(&e.tokenStdin, "token-stdin", e.tokenStdin, "")
	fs.BoolVar(&e.noInput, "no-input", e.noInput, "")
	fs.BoolVar(&e.verbose, "v", e.verbose, "")
	if extra != nil {
		extra(fs)
	}
	var pos []string
	for {
		if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
			return nil, err
		} else if err != nil {
			return nil, usageError{err}
		}
		if fs.NArg() == 0 || n < 0 {
			pos = append(pos, fs.Args()...)
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	switch {
	case e.output != "table" && e.output != "json" && e.output != "yaml":
		return nil, usageError{fmt.Errorf("-o %q: give table, json or yaml", e.output)}
	case e.tokenStdin && e.tokenFile != "":
		return nil, usageError{errors.New("give --token-file or --token-stdin, not both")}
	case n >= 0 && len(pos) != n:
		return nil, usageError{fmt.Errorf("%s takes %d argument(s), got %d", name, n, len(pos))}
	}
	return pos, nil
}

// open selects the context and reads its token (client-contracts.md §5 item 1, §9.4).
func (e *env) open() (*client.Client, error) {
	x, err := e.selected()
	if err != nil {
		return nil, err
	}
	return client.Open(x, client.TokenInput{NoInput: e.noInput, File: e.tokenFile, Stdin: e.tokenStdin}, e.err, e.verboseOut())
}

// selected is the context §5 item 1 selects.
func (e *env) selected() (*client.Context, error) {
	cfg, err := client.LoadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Select(e.context)
}

// verboseOut is where -v output goes: standard error, or nowhere.
func (e *env) verboseOut() io.Writer {
	if e.verbose {
		return e.err
	}
	return nil
}

// version is the client's version when the build sets it, as a release build does
// (-ldflags "-X main.version=…"); otherwise clientVersion reads the main module's version from the
// build information.
var version string

func clientVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

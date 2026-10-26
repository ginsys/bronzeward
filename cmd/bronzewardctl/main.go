// Command bronzewardctl is the operator's client for a Bronzeward server's API
// (docs/spec/operator-experience.md §4, docs/spec/client-contracts.md). It holds no server code:
// internal/client/closure_test.go checks its dependency closure.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/ginsys/bronzeward/internal/client"
)

const usage = `usage: bronzewardctl [global flags] <command> [arguments]

commands:
  context list | use <name> | remove <name>
  context add <name> --server <url> [--certificate-authority <file>] [--automation-token-file <path>]
  version     this client's version, the server's and its API revision
  whoami      the identity, roles and epoch the server sees

global flags, before or after the command:
  --context <name>      else BRONZEWARD_CONTEXT, else the current context
  -o table|json|yaml    json and yaml print the API resource unchanged
  --token-file <path>   the automation token, from an owner-only file; else the context's
                        automation.tokenFile, else the path in BRONZEWARD_TOKEN_FILE
  --token-stdin         the automation token, from standard input
  --no-input            never prompt
  -v                    name a proxy a request goes through

exit codes: 0 success; 1 refused or failed; 2 usage error; 3 outcome unknown`

type usageError struct{ error }

type env struct {
	context, output, tokenFile   string
	tokenStdin, noInput, verbose bool
	out, err                     io.Writer
}

// main disables core dumps before any token is read, and a panic prints a fixed message and no
// stack values (client-contracts.md §11.2 item 5).
func main() {
	debug.SetTraceback("none")
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "bronzewardctl: internal error")
			os.Exit(1)
		}
	}()
	if err := noCoreDumps(); err != nil {
		fmt.Fprintln(os.Stderr, "bronzewardctl: disabling core dumps:", err)
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	e := &env{output: "table", out: stdout, err: stderr}
	commands := map[string]func([]string) error{"context": e.contextCmd, "version": e.versionCmd, "whoami": e.whoamiCmd}
	pos, err := e.parse("bronzewardctl", args, -1, nil)
	if err == nil && len(pos) == 0 {
		err = usageError{errors.New("no command")}
	} else if err == nil && commands[pos[0]] == nil {
		err = usageError{fmt.Errorf("unknown command %q", pos[0])}
	} else if err == nil {
		err = commands[pos[0]](pos[1:])
	}
	var u usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintln(stdout, usage)
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(stderr, "bronzewardctl: %v\n\n%s\n", err, usage)
		return 2
	}
	fmt.Fprintln(stderr, "bronzewardctl:", err)
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
	cfg, err := client.LoadConfig()
	if err != nil {
		return nil, err
	}
	x, err := cfg.Select(e.context)
	if err != nil {
		return nil, err
	}
	return client.Open(x, client.TokenInput{File: e.tokenFile, Stdin: e.tokenStdin}, e.err, e.verboseOut())
}

// verboseOut is where -v output goes: standard error, or nowhere.
func (e *env) verboseOut() io.Writer {
	if e.verbose {
		return e.err
	}
	return nil
}

// clientVersion is the module version the build information records.
func clientVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// Command bronzewardctl is the operator's client for a Bronzeward server's API
// (docs/spec/operator-experience.md §4, docs/spec/client-contracts.md). It holds no server code:
// internal/client/closure_test.go checks its dependency closure.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"reflect"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

type usageError struct{ error }

// operationFailed is a waited-for operation that ended failed, rejected or cancelled: exit 4
// (operator-experience.md §4).
type operationFailed struct{ error }

// notSent is a write the client refuses before sending, from what it read: exit 2.
type notSent struct{ error }

type env struct {
	context, output, tokenFile   string
	tokenStdin, noInput, verbose bool
	out, err                     io.Writer

	// heldCtrlC is the Ctrl-C context of a command that arms for its whole run (hold); ownsCtrlC: the
	// command has a handler of its own, so its reads arm none (interrupt.go).
	heldCtrlC context.Context
	ownsCtrlC bool

	// cut is the first Ctrl-C a read took in this run (cutNow): it ends the command with exit 130,
	// also when a caller dropped the error of that read.
	cutMu sync.Mutex
	cut   *client.Interrupted

	// runEpoch is the epoch a run of several writes began under, set by cluster onboard from its first read
	// and empty for every other command: writeWith binds each write of the run to it.
	runEpoch string

	// collect is set while a composite prints one object for -o json or yaml: a write then hands its
	// answer to it and prints nothing (composite.go).
	collect *collector

	// names is who the principals a table output prints name, read by GET /principals (names.go);
	// an identifier asked and not answered is nil. Held for this run only.
	names map[string]*apitypes.PrincipalBody

	// bodies are the drafts, releases, plans, ingestions and machines read for a title (titles.go), by
	// path, and titles the titles built from them, by object; both are held for this run only.
	bodies map[string]any
	titles map[string]string

	// where is the command path parse was last called with (machine add): the help a usage error prints.
	// helped is the flag set of that command, read for the flags its help lists.
	where  string
	helped *flag.FlagSet
}

func main() {
	if err := harden(); err != nil {
		fmt.Fprintln(client.GuardWriter(os.Stderr), "bronzewardctl: disabling core dumps and tracebacks:", client.Shown(err))
		os.Exit(1)
	}
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "bronzewardctl: internal error")
			os.Exit(1)
		}
	}()
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	endByCause()
	os.Exit(code)
}

// endedBy is the signal that ended a command (report sets it): the process is to end by it, so that
// a parent sees the signal and not an exit code.
var endedBy syscall.Signal

// endByCause ends the process by endedBy, if a command was ended by a signal, after the command has
// restored what it changed. The default action of the signal then applies.
func endByCause() {
	if endedBy == 0 {
		return
	}
	signal.Reset(endedBy)
	_ = syscall.Kill(os.Getpid(), endedBy)
	// The signal is delivered at once or within moments; the exit code only backs a refusal.
	time.Sleep(5 * time.Second)
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
	// Every byte that reaches the terminal passes client.GuardWriter: standard error always (errors,
	// notices, prompts and the -v lines), and standard output in -o table, whose text is the CLI's own
	// sentences and tables. json and yaml are the API resource, which the guard would change.
	stderr = client.GuardWriter(stderr)
	e := &env{output: "table", err: stderr}
	e.out = tableGuard{e, stdout, client.GuardWriter(stdout)}
	commands := e.handlers()
	pos, err := e.parse("bronzewardctl", args, -1, nil)
	if err == nil && len(pos) == 0 {
		err = usageError{errors.New("no command")}
	} else if err == nil && commands[pos[0]] == nil {
		err = usageError{fmt.Errorf("unknown command \"%s\"", client.Quoted(pos[0]))}
	} else if err == nil {
		err = commands[pos[0]](pos[1:])
	}
	err = e.afterCut(err, stderr)
	// The help a request or a usage error prints is the table's entry for the command parse last read.
	var u usageError
	if errors.Is(err, flag.ErrHelp) {
		err = helpRequest{e.helpText()}
	} else if errors.As(err, &u) {
		err = withHelp{err, e.helpText()}
	}
	return report(err, stdout, stderr)
}

// handlers is the function of each top-level command; its keys are the table's top-level names.
func (e *env) handlers() map[string]func([]string) error {
	return map[string]func([]string) error{"help": e.helpCmd, "completion": e.completionCmd, "context": e.contextCmd, "version": e.versionCmd, "whoami": e.whoamiCmd, "login": e.loginCmd, "logout": e.logoutCmd,
		"status": e.statusCmd, "cluster": e.clusterCmd, "machine": e.machineCmd,
		"fragment": e.fragmentCmd, "profile": e.profileCmd, "assignment": e.assignmentCmd,
		"draft": e.draftCmd, "import": e.importCmd, "import-base": e.importBaseCmd, "publish": e.publish, "release": e.releaseCmd, "plan": e.planCmd,
		"apply": e.apply, "approve": e.approve, "approval": e.approvalCmd, "operation": e.operationCmd, "act": e.actCmd,
		"drift": e.driftCmd, "recovery": e.recoveryCmd, "identity": e.identityCmd}
}

// helpRequest is --help or help as the error run returns: it carries the text report prints on standard output.
type helpRequest struct{ text string }

func (helpRequest) Error() string        { return flag.ErrHelp.Error() }
func (helpRequest) Is(target error) bool { return target == flag.ErrHelp }

// withHelp is a usage error with the help of the command it was raised in, which report prints after it.
type withHelp struct {
	error
	text string
}

func (w withHelp) Unwrap() error { return w.error }

// afterCut makes a Ctrl-C that a read took end the command, also when a caller dropped the error of
// that read (a best-effort lookup, say): the stop line and exit 130 win. An error the command also
// returned keeps its own line, printed first, because a failure is never hidden. The outcome of a
// write that is unknown keeps its exit 3, the line to follow an operation its own.
func (e *env) afterCut(err error, stderr io.Writer) error {
	cut := e.cutNow(nil)
	var stopped interrupted
	var unknown *client.Unknown
	var settled outcomeUnknown
	var signalled endedBySignal
	if cut == nil || errors.As(err, new(*client.Interrupted)) || errors.As(err, &stopped) || errors.As(err, &unknown) || errors.As(err, &settled) || errors.As(err, &signalled) {
		return err
	}
	if err != nil {
		fmt.Fprintln(stderr, "bronzewardctl:", client.Shown(err))
	}
	return cut
}

// report prints the error of a command, the one place that does, and returns the exit code.
func report(err error, stdout, stderr io.Writer) int {
	var u usageError
	var failed operationFailed
	var settled outcomeUnknown
	var refused client.RefusedRepeat
	var declined notConfirmed
	var unknown *client.Unknown
	var signalled endedBySignal
	var waiting notTerminal
	var stopped interrupted
	var problem *client.Problem
	var lost outputFailed
	var refusedHere notSent
	switch {
	case err == nil:
		return 0
	case errors.As(err, &signalled):
		endedBy = signalled.sig
		return 128 + int(signalled.sig)
	case errors.Is(err, flag.ErrHelp):
		text := indexText()
		var h helpRequest
		if errors.As(err, &h) {
			text = h.text
		}
		// Help is output like any other: when it cannot be written, code 6 takes the place of 0.
		if _, werr := fmt.Fprintln(stdout, text); werr != nil {
			fmt.Fprintln(stderr, "bronzewardctl:", client.Shown(werr))
			return 6
		}
		return 0
	case errors.As(err, &u):
		text := indexText()
		var w withHelp
		if errors.As(err, &w) {
			text = w.text
		}
		fmt.Fprintf(stderr, "bronzewardctl: %s\n\n%s\n", client.Shown(err), text)
		return 2
	}
	// operation watch printed its stop line and the line to follow it again; nothing follows them.
	if errors.As(err, &stopped) {
		return 130
	}
	// A Ctrl-C ended a read or its wait before a retry (#643). It says nothing of what an earlier step of
	// the command wrote: a command may write and then read. An error that the command joined with it is
	// printed first, so that the stop line ends the output, and an unknown outcome keeps its exit 3 and its
	// text: the sink below prints them with the stop of the read.
	var cut *client.Interrupted
	if errors.As(err, &cut) && !errors.As(err, &unknown) && !errors.As(err, &settled) {
		for _, other := range besideCut(err) {
			fmt.Fprintln(stderr, "bronzewardctl:", client.Shown(other))
		}
		fmt.Fprintf(stderr, "Stopped at your request while reading %s.\n", client.Printable(cut.Route))
		return 130
	}
	// The sink of every error: client.Shown escapes all of it but the line breaks of the client's own
	// multi-line errors, so no error from outside can start a line of its own.
	fmt.Fprintln(stderr, "bronzewardctl:", client.Shown(err))
	switch {
	case errors.As(err, &unknown), errors.As(err, &settled):
		return 3
	case errors.As(err, &failed):
		return 4
	case errors.As(err, &waiting):
		return 5
	case errors.As(err, &refused), errors.As(err, &declined), errors.As(err, &refusedHere):
		return 2
	// The server's own answer says more than a lost report, so a refusal keeps 1 beside a failed write.
	case errors.As(err, &problem):
		return 1
	case errors.As(err, &lost):
		return 6
	}
	return 1
}

// besideCut lists the errors that a command joined with the Ctrl-C of a read, in the order it joined
// them. An error that wraps the Ctrl-C itself is not one of them: the stop line says it. It goes down a
// join, and down a wrapper that holds the Ctrl-C (#670), so a join under a wrapper still lists its other
// members. It skips a nil member and lists one error value once.
func besideCut(err error) []error {
	var listed []error
	for _, other := range besideCutAll(err) {
		if !listedAlready(listed, other) {
			listed = append(listed, other)
		}
	}
	return listed
}

func besideCutAll(err error) []error {
	switch wrapped := err.(type) {
	case nil:
		return nil
	case interface{ Unwrap() []error }:
		var others []error
		for _, member := range wrapped.Unwrap() {
			others = append(others, besideCutAll(member)...)
		}
		return others
	case interface{ Unwrap() error }:
		// A wrapper that holds the Ctrl-C says only what it adds to it: what it wraps tells the rest. One
		// that does not hold it is an error of its own, listed as it is.
		if errors.As(err, new(*client.Interrupted)) {
			return besideCutAll(wrapped.Unwrap())
		}
	}
	if errors.As(err, new(*client.Interrupted)) {
		return nil
	}
	return []error{err}
}

// listedAlready reports whether err is the same error value as one in listed. A value whose type cannot
// be compared is never taken for a repeat: == on it would panic. That holds for a slice type, and for a
// struct whose interface field holds one, which only the value can tell.
func listedAlready(listed []error, err error) bool {
	if !reflect.ValueOf(err).Comparable() {
		return false
	}
	for _, have := range listed {
		if reflect.TypeOf(have) == reflect.TypeOf(err) && have == err {
			return true
		}
	}
	return false
}

// anyArgs asks parse for every positional argument, however many.
const anyArgs = -2

// parse reads the global flags and extra's anywhere among args and wants n positional arguments,
// or with anyArgs any number; with n = -1 it stops at the first one, the command, whose own flags
// follow.
func (e *env) parse(name string, args []string, n int, extra func(*flag.FlagSet)) ([]string, error) {
	e.where = name
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
	e.helped = fs
	var pos []string
	for {
		if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
			return nil, err
		} else if err != nil {
			return nil, usageError{flagError(err)}
		}
		if fs.NArg() == 0 || n == -1 {
			pos = append(pos, fs.Args()...)
			break
		}
		pos, args = append(pos, fs.Arg(0)), fs.Args()[1:]
	}
	switch {
	case e.output != "table" && e.output != "json" && e.output != "yaml":
		return nil, usageError{fmt.Errorf("-o \"%s\": give table, json or yaml", client.Quoted(e.output))}
	case e.tokenStdin && e.tokenFile != "":
		return nil, usageError{errors.New("give --token-file or --token-stdin, not both")}
	case n >= 0 && len(pos) != n:
		return nil, usageError{fmt.Errorf("%s takes %d argument(s), got %d", name, n, len(pos))}
	}
	return pos, nil
}

// rejectedValue is the start of the parser's error for a value that a flag cannot take. It quotes
// the value with %q, the Go form (`\t`, `\\`), where the contract's form is `\x09` and `\`. The
// quoted part is a Go string literal, so it matches exactly.
var rejectedValue = regexp.MustCompile(`^(invalid (?:boolean )?value )("(?:[^"\\]|\\.)*")(.*)$`)

// flagError is the parser's error with every argument in the contract's escape form. The parser
// puts the name of a flag as given in its messages; a rejected value is rebuilt from the value itself.
func flagError(err error) error {
	text := err.Error()
	if m := rejectedValue.FindStringSubmatch(text); m != nil {
		if v, uerr := strconv.Unquote(m[2]); uerr == nil {
			return errors.New(m[1] + "\"" + client.Quoted(v) + "\"" + client.Printable(m[3]))
		}
	}
	return errors.New(client.Printable(text))
}

// open selects the context and reads its token (client-contracts.md §5 item 1, §9.4).
func (e *env) open() (*client.Client, error) { return e.openTo(e.err, e.verboseOut()) }

// openTo is open with the writers of the client's notices and -v output given, for a command that
// must stop them (operation watch).
func (e *env) openTo(notices, verbose io.Writer) (*client.Client, error) {
	x, err := e.selected()
	if err != nil {
		return nil, err
	}
	c, err := client.Open(x, client.TokenInput{NoInput: e.noInput, File: e.tokenFile, Stdin: e.tokenStdin}, notices, verbose)
	if c != nil {
		c.Arm, c.Cut = e.arm, e.cutNow
	}
	return c, err
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

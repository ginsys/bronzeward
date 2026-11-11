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
  cluster discover --talosconfig <file> [--endpoint <ip[:port]>]   read a cluster, print the commands that record it
  machine list [--cluster <id>] | show <id> | timeline <id> | observations <id>
  machine timeline <id> --follow [--limit <n>]
              the newest entries, then each new one as the server records it, read every 5s;
              Ctrl-C stops it with exit 130; -o json prints one entry per line, -o yaml one
              document per entry
  machine add --cluster <id> (--talos-node-id <id> | --smbios-uuid <uuid>) [--serial <s>]
              --platform metal|container|cloud --talos-endpoint <ip>[:<port>]
  machine set-endpoint <id> --talos-endpoint <ip>[:<port>]
              writes, by a person signed in as author, repeat a write whose outcome was unknown
              exactly with --idempotency-key, --expect-epoch and --expect-principal as printed
  machine freeze | unfreeze <id> --reason <text>
              a freeze stops new commitments and attempts, not an attempt already recorded, and
              an adoption can still be recorded; freeze takes author, publisher, approver or
              recovery-admin, unfreeze only an approver; a server started for recovery, or a scope
              still pre-restore unaccounted, refuses an unfreeze
  drift show <machine>
              the open drift: its observations, late landing and the choices; the time shown is
              that of the observation, the API serves no opening time; there is no ignore command
  drift adopt <machine> --draft <draft> [--review] [--staging encrypted|transient] [--marks]
              import as a drift-adoption (author, a person), publish (publisher), then plan create
              --operation adopt (publisher); stops at a missing role or a pause and prints the
              rest; never approves; its repeat flags repeat the import
  drift revert <machine>
              publisher; plan create of Desired as apply-config, bound to the open drift record;
              it reads the machine first and takes no repeat flag; a machine with no Applied
              configuration is pointed at an adopt plan, and nothing is sent
  fragment | profile | assignment show <head or revision ID> | history <head ID>
  draft list [--cluster <id>] | show <draft>
  draft create --cluster <id> --title <title>
  draft discard <draft>
  fragment set <name> --draft <draft> --layer <layer> --file <path or -> [--declarations <path>]
              [--marks] [--ingest-wait <duration>] the document from a file, or - for standard
              input; --marks reads one mark per line from a prompt or standard input until an
              empty line; Linux only; a repeat waits while the first request still ingests, for
              --ingest-wait (2m) or the server's claim lease, whichever is longer
  profile set <name> --draft <draft> --fragments <name or frv_ ID>[,...]
              a name pins the revision the draft proposes, else the fragment's head revision
  assignment set <machine> --draft <draft> [--profiles <name>[,...]]
              [--fragment <layer>=<name>[,...]]... the layer global, site, cluster, role,
              workload or override
  fragment | profile | assignment rm <name or machine> --draft <draft>
              a write to a draft sends the ETag of the last draft show or write of that draft, or
              --if-match <etag>; if the draft changed since, it saves nothing and says so
              draft writes take author; repeat one with the outcome-unknown block's flags,
              --if-match included
  import start --machine <id> --draft <draft> [--file <path or ->] [--staging encrypted|transient]
              [--review] [--marks] [--no-wait]
              the machine's configuration, or with --file a document, into the draft, sending its
              kept ETag or --if-match; waits until the import pauses for review or ends
  import review <ingestion>
              a paused import's staged document, only on a terminal, in a pager (space or f
              forward, b back, q quit) that writes nothing else; then marks from the prompt
  import mark <ingestion> [--no-wait]
              mark values as secret by their paths, one per line from the prompt or standard
              input, never an argument; waits until the import pauses again
  import continue | takeover <ingestion> [--no-wait]
              continue an import paused for review, or take over one whose lease lapsed; waits
  import abandon <ingestion>
  import status [<ingestion>] | [--state <s>] [--machine <id>]
              one import's state, lease and next steps, or a list: --state paused or held finds
              imports to continue or take over
  publish <draft> [--no-wait]
              publisher; sends the draft's kept ETag or --if-match, waits for the publication
              and names the release and each machine's plan create command; nothing is sent to
              any machine
  release list [--cluster <id>] [--draft <id>] | show <release>
  release review | provenance <release> <machine>
              the rendered, redacted configuration, or the secret references it was built from
  plan create --release <id> --machine <id> --operation apply-config|adopt [--mode no-reboot]
              [--expires-in <d>] [--max-observation-age <d>] [--check-validity <d>]
              [--transport-deadline <d>] [--verification-deadline <d>] [--max-attempts <n>]
              publisher; a duration in whole seconds, such as 90s or 24h; an omitted value takes
              the server's default; an adopt plan takes no mode, deadline or attempt limit; the
              plan is not approved and nothing runs until an approver approves
  plan list [--state <s>] [--machine <id>] | show <plan>
              show prints the plan's evidence, its state and what can be done next
  plan cancel <plan> --reason <text>
              a cancelled plan cannot be committed; a committed plan's operation ends only while
              no attempt is recorded, and an attempt already recorded is not stopped
  approve <plan>
              approver, a person signed in; prints the plan and the predicted self-approval on
              standard error, then sends only when the plan ID is typed back, on the terminal or
              as one line of standard input; there is no --yes, and --no-input refuses; an
              approval starts no operation, so approve does not wait
  approval show <approval>
  approval revoke <approval> --reason <text>
              a revocation ends an uncommitted approval and undoes no commitment; an attempt
              already recorded is not stopped
  operation list [--plan <id>] [--machine <id>] [--state <s>] [--kind <k>] | show <operation>
  operation watch <operation> [--timeout <d>]
              one line per event until the operation ends, then its outcome; goes on past
              unresolved; --timeout (30m) ends the watch, not the operation, with exit 5;
              Ctrl-C stops only the watch, with exit 130
  act list    the audit record
  recovery status
              the epoch, recovery mode, the entry and exit, and each scope's next step
  recovery enter --restored family=<f>,backup=<id>,taken-at=<time> [--restored ...]
              --stopped-at <time> --reason <text>
              on a server started for recovery; sent only when enter is typed back; a repeat
              of an unknown outcome takes --idempotency-key and --expect-principal, no epoch
  recovery exit [--fact name=value]...
              once every scope is released
  recovery scope-account <machine> --start <observation> --end <observation> [--explanation <text>]
              after a restore: accounts every unaccounted attempt on the machine; shows the window
              and the risk, and is sent only when the machine ID is typed back
  recovery account <operation> <attempt> --start <observation> --end <observation> [--explanation <text>]
              accounts one attempt whose response was lost; the same window, risk and typed ID
  recovery takeover <operation> --reason <text>
              makes the serving controller the owner of a non-terminal operation; sends nothing to the node;
              a repeat of an unknown outcome also takes --generation as printed
  recovery resolve <operation> --reason <text>
              ends an unresolved operation with no attempt as cancelled; records no remote outcome;
              a repeat of an unknown outcome sends the same body without the state checks
  recovery mark <machine> [--fact name=value]...
              records the state the evidence supports: ready, blocked or unresolved
  recovery release <machine> [--fact name=value]...
              releases a ready scope; a release is not an approval
  identity revoke (--identity <id> | --iss <iss> --sub <sub>) --reason <text>
              permanent; sent only when the identity (or the subject) is typed back
              these writes need a person signed in as recovery-admin; times are RFC 3339
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

exit codes: 0 success; 1 refused or failed; 2 usage error, refused repeat or approval not
confirmed; 3 outcome unknown, or
an import's lease lapsed while its operation runs;
4 a waited-for operation ended failed, rejected or cancelled; 5 operation watch's --timeout
ended with the operation not ended; 6 the output cannot be written (a full disk, say), unless
a code above applies or the server refused the request (1); 130 a read command or operation watch stopped by Ctrl-C`

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

	// collect is set while a composite prints one object for -o json or yaml: a write then hands its
	// answer to it and prints nothing (composite.go).
	collect *collector
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
	commands := map[string]func([]string) error{"context": e.contextCmd, "version": e.versionCmd, "whoami": e.whoamiCmd, "login": e.loginCmd, "logout": e.logoutCmd,
		"status": e.statusCmd, "cluster": e.clusterCmd, "machine": e.machineCmd,
		"fragment": e.fragmentCmd, "profile": e.profileCmd, "assignment": e.assignmentCmd,
		"draft": e.draftCmd, "import": e.importCmd, "publish": e.publish, "release": e.releaseCmd, "plan": e.planCmd,
		"approve": e.approve, "approval": e.approvalCmd, "operation": e.operationCmd, "act": e.actCmd,
		"drift": e.driftCmd, "recovery": e.recoveryCmd, "identity": e.identityCmd}
	pos, err := e.parse("bronzewardctl", args, -1, nil)
	if err == nil && len(pos) == 0 {
		err = usageError{errors.New("no command")}
	} else if err == nil && commands[pos[0]] == nil {
		err = usageError{fmt.Errorf("unknown command \"%s\"", client.Quoted(pos[0]))}
	} else if err == nil {
		err = commands[pos[0]](pos[1:])
	}
	return report(e.afterCut(err, stderr), stdout, stderr)
}

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
		// Help is output like any other: when it cannot be written, code 6 takes the place of 0.
		if _, werr := fmt.Fprintln(stdout, usage); werr != nil {
			fmt.Fprintln(stderr, "bronzewardctl:", client.Shown(werr))
			return 6
		}
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(stderr, "bronzewardctl: %s\n\n%s\n", client.Shown(err), usage)
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

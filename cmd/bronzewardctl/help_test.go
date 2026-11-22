package main

import (
	"errors"
	"flag"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// leavesOfTheSpecification are the leaves of operator-experience.md §4 (docs/spec), with help itself. The
// table must hold exactly these: a leaf missing from it, or an extra one, fails here.
var leavesOfTheSpecification = []string{
	"help",
	"context list", "context use", "context add", "context remove",
	"version", "whoami", "login", "logout", "status",
	"cluster list", "cluster show", "cluster add", "cluster discover", "cluster first-key", "cluster onboard",
	"machine list", "machine show", "machine timeline", "machine observations", "machine add", "machine set-endpoint",
	"machine rename", "machine freeze", "machine unfreeze",
	"drift show", "drift adopt", "drift revert",
	"fragment show", "fragment history", "fragment set", "fragment rm",
	"profile show", "profile history", "profile set", "profile rm",
	"assignment show", "assignment history", "assignment set", "assignment rm",
	"draft list", "draft show", "draft create", "draft discard",
	"import start", "import status", "import review", "import mark", "import continue", "import takeover", "import abandon",
	"import-base document",
	"publish",
	"release list", "release show", "release review", "release provenance",
	"plan list", "plan show", "plan create", "plan cancel",
	"approve", "approval show", "approval revoke",
	"operation list", "operation show", "operation watch",
	"act list",
	"recovery status", "recovery enter", "recovery exit", "recovery scope-account", "recovery account", "recovery takeover",
	"recovery resolve", "recovery mark", "recovery release",
	"identity revoke",
}

func tableLeaves() []cmdEntry {
	var leaves []cmdEntry
	for _, x := range cmdTable {
		if x.synopsis != "" {
			leaves = append(leaves, x)
		}
	}
	return leaves
}

// TestCommandTableHoldsEveryLeaf: the table has the leaves of the specification and no other, each with
// its roles, each group with a purpose line, and no path twice.
func TestCommandTableHoldsEveryLeaf(t *testing.T) {
	var have []string
	for _, x := range tableLeaves() {
		have = append(have, x.path)
		if x.roles == "" || x.line == "" {
			t.Errorf("%s: roles %q, purpose %q: every leaf names its roles and its purpose", x.path, x.roles, x.line)
		}
	}
	for _, want := range leavesOfTheSpecification {
		if !slices.Contains(have, want) {
			t.Errorf("the table lacks the leaf %q", want)
		}
	}
	for _, h := range have {
		if !slices.Contains(leavesOfTheSpecification, h) {
			t.Errorf("the table has %q, which the specification's command tree does not", h)
		}
	}
	seen := map[string]bool{}
	for _, x := range cmdTable {
		if seen[x.path] {
			t.Errorf("the table holds %q twice", x.path)
		}
		seen[x.path] = true
		if x.synopsis == "" && x.line == "" {
			t.Errorf("group %q has no purpose line", x.path)
		}
		if i := strings.IndexByte(x.path, ' '); i > 0 && lookup(x.path[:i]) == nil {
			t.Errorf("%q has no group entry %q", x.path, x.path[:i])
		}
	}
}

// TestTopLevelHandlersAreTheTable: the first words the client dispatches are the table's top-level names.
func TestTopLevelHandlersAreTheTable(t *testing.T) {
	var keys []string
	for k := range (&env{}).handlers() {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := slices.Clone(topLevel())
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("the handlers are %v, the table's top-level names %v", keys, want)
	}
}

// TestEveryLeafAnswersHelp is the proof that help reads the flag set of the real command: for each leaf of
// the table, the dispatcher with its path and --help ends with the help error, builds a flag set named by
// the path, and sends no request; `help <path>` prints the same text as `<path> --help`.
func TestEveryLeafAnswersHelp(t *testing.T) {
	seen := recoveryServer(t, map[string]string{}, "")
	for _, x := range tableLeaves() {
		words := strings.Fields(x.path)
		probe := &env{output: "table", err: io.Discard, out: io.Discard}
		err := probe.handlers()[words[0]](append(slices.Clone(words[1:]), "--help"))
		if !errors.Is(err, flag.ErrHelp) || probe.helped == nil || probe.helped.Name() != x.path {
			name := "no flag set"
			if probe.helped != nil {
				name = probe.helped.Name()
			}
			t.Errorf("%s --help: error %v, flag set %q; want the help error and a flag set named %q", x.path, err, name, x.path)
			continue
		}
		code, out, errOut := runDrift(t, append(slices.Clone(words), "--help")...)
		if code != 0 || errOut != "" || !strings.HasPrefix(out, "usage: bronzewardctl "+words[0]) {
			t.Errorf("%s --help: exit %d, stdout %q, stderr %q", x.path, code, out, errOut)
		}
		if !strings.Contains(out, "Roles: ") {
			t.Errorf("%s --help does not name its roles:\n%s", x.path, out)
		}
		if x.person && !strings.Contains(out, "Needs a person signed in, not an automation token.") {
			t.Errorf("%s needs a person and its help does not say so:\n%s", x.path, out)
		}
		if !x.person && strings.Contains(out, "Needs a person") {
			t.Errorf("%s needs no person and its help says it does:\n%s", x.path, out)
		}
		if code, viaHelp, errOut := runDrift(t, append([]string{"help"}, words...)...); code != 0 || errOut != "" || viaHelp != out {
			t.Errorf("help %s: exit %d, stderr %q; its text differs from %s --help:\n%s\n---\n%s", x.path, code, errOut, x.path, viaHelp, out)
		}
	}
	if got := seen(); len(got) != 0 {
		t.Errorf("help sent requests: %v", got)
	}
}

// TestGroupHelp: a group's help, by --help or by help, lists its leaves one line each.
func TestGroupHelp(t *testing.T) {
	for _, g := range cmdTable {
		if g.synopsis != "" || strings.Contains(g.path, " ") {
			continue
		}
		verbs := verbsOf(g.path)
		if len(verbs) == 0 {
			continue // a leaf with no group, such as login
		}
		code, out, errOut := runDrift(t, g.path, "--help")
		if code != 0 || errOut != "" {
			t.Errorf("%s --help: exit %d, stderr %q", g.path, code, errOut)
		}
		for _, v := range verbs {
			if !strings.Contains(out, "\n  "+g.path+" "+v+" ") {
				t.Errorf("%s --help does not list %s:\n%s", g.path, v, out)
			}
		}
		if _, viaHelp, _ := runDrift(t, "help", g.path); viaHelp != out {
			t.Errorf("help %s differs from %s --help", g.path, g.path)
		}
	}
}

// TestIndexShape pins the two shapes of the owner's comment on ginsys/bronzeward#402: --help and help print
// the groups and their leaves, one line per top-level command, with no flags, in one screen.
func TestIndexShape(t *testing.T) {
	code, out, errOut := runDrift(t, "--help")
	if code != 0 || errOut != "" {
		t.Fatalf("--help: exit %d, stderr %q", code, errOut)
	}
	if _, viaHelp, _ := runDrift(t, "help"); viaHelp != out {
		t.Errorf("help and --help differ:\n%s\n---\n%s", viaHelp, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[0] != "usage: bronzewardctl [global flags] <command> [arguments]" {
		t.Errorf("the first line is %q", lines[0])
	}
	if len(lines) > 30 {
		t.Errorf("the index is %d lines, over one screen of 30", len(lines))
	}
	for _, name := range topLevel() {
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "  "+name+" ") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("the index has %d lines for %q, want one", n, name)
		}
	}
	for _, l := range lines[1:] {
		if strings.Contains(strings.TrimPrefix(l, "Run bronzewardctl"), " --") {
			t.Errorf("the index shows a flag: %q", l)
		}
	}
	if !strings.Contains(out, "help global") {
		t.Errorf("the index does not point to help global")
	}
}

// TestHelpGlobalAndUnknown: help global carries the global flags and the exit codes; help of nothing
// is a usage error.
func TestHelpGlobalAndUnknown(t *testing.T) {
	code, out, _ := runDrift(t, "help", "global")
	for _, want := range []string{"--no-input", "-o table|json|yaml", "exit codes: 0 success", "130 a read command"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("help global: exit %d, lacks %q:\n%s", code, want, out)
		}
	}
	if code, out, errOut := runDrift(t, "help", "nosuch"); code != 2 || out != "" || !strings.HasPrefix(errOut, "bronzewardctl: help: unknown command \"nosuch\"\n\nusage: bronzewardctl help [<command>]") {
		t.Errorf("help nosuch: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

// TestUsageErrorPrintsTheCommandsHelp: a usage error prints its message and then the help of the command it
// was raised in, in place of the whole index; the exit code 2, the stream (standard error) and the first line
// of the help, usage: bronzewardctl, are the ones the whole text had.
func TestUsageErrorPrintsTheCommandsHelp(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		line string // the first line of the help
		not  string // a command the help must not list
	}{
		{"a bad flag", []string{"machine", "add", "--bogus"}, "usage: bronzewardctl machine add --cluster", "commands:"},
		{"a missing verb", []string{"machine"}, "usage: bronzewardctl [global flags] machine <command>", "commands:"},
		{"no command", nil, "usage: bronzewardctl [global flags] <command> [arguments]", ""},
		{"an unknown command", []string{"nosuch"}, "usage: bronzewardctl [global flags] <command> [arguments]", ""},
	} {
		code, out, errOut := runDrift(t, c.args...)
		_, help, ok := strings.Cut(errOut, "\n\n")
		if code != 2 || out != "" || !ok || !strings.HasPrefix(errOut, "bronzewardctl: ") || !strings.HasPrefix(help, c.line) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 2, nothing, the error, a blank line and %q", c.name, code, out, errOut, c.line)
		}
		if c.not != "" && strings.Contains(errOut, c.not) {
			t.Errorf("%s: the usage error prints the whole index:\n%s", c.name, errOut)
		}
	}
}

// TestMachineAddHelpHasTheRule: the help of machine add says which of --smbios-uuid and --talos-node-id to use, and
// which cluster ID --cluster takes (the owner's second comment on ginsys/bronzeward#402).
func TestMachineAddHelpHasTheRule(t *testing.T) {
	_, out, _ := runDrift(t, "machine", "add", "--help")
	for _, want := range []string{
		"if the node reports an SMBIOS UUID (read it with `talosctl get systeminformation`),\nuse --smbios-uuid",
		"If it reports none, as a container node does, use --talos-node-id",
		"--cluster is Bronzeward's cluster ID (cl_...), not the Talos cluster ID.",
		"--smbios-uuid", "--talos-node-id", "--idempotency-key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("machine add --help lacks %q:\n%s", want, out)
		}
	}
}

// TestHelpOfAPathInOneArgument: help "machine add" names the command as machine add does, and a path no
// handler answers is an error, not a panic.
func TestHelpOfAPathInOneArgument(t *testing.T) {
	_, two, _ := runDrift(t, "help", "machine", "add")
	if code, one, errOut := runDrift(t, "help", "machine add"); code != 0 || errOut != "" || one != two || one == "" {
		t.Errorf("help \"machine add\": exit %d, stderr %q, text differs from help machine add", code, errOut)
	}
	if code, out, errOut := runDrift(t, "help", "nosuch add"); code != 2 || out != "" || !strings.Contains(errOut, "unknown command \"nosuch add\"") {
		t.Errorf("help \"nosuch add\": exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

// TestHelpListsOnlyTheFlagsACommandTakes: a flag a leaf registers only to refuse it is not in its help, and
// the flags a leaf takes are, a usage error's help included.
func TestHelpListsOnlyTheFlagsACommandTakes(t *testing.T) {
	flags := func(args ...string) string {
		_, out, _ := runDrift(t, append(args, "--help")...)
		_, after, _ := strings.Cut(out, "\nFlags: ")
		line, _, _ := strings.Cut(after, "\n")
		return line
	}
	for _, c := range []struct {
		path      []string
		has, lack []string
	}{
		{[]string{"cluster", "onboard"}, []string{"--talosconfig", "--review"}, []string{"--no-wait", "--idempotency-key", "--expect-epoch", "--if-match"}},
		{[]string{"drift", "revert"}, nil, []string{"--idempotency-key", "--expect-principal", "--if-match"}},
		{[]string{"fragment", "show"}, nil, []string{"--limit", "--cursor", "--all"}},
		{[]string{"fragment", "history"}, []string{"--limit", "--cursor", "--all"}, nil},
		{[]string{"machine", "add"}, []string{"--idempotency-key", "--cluster"}, []string{"--if-match"}},
		{[]string{"fragment", "set"}, []string{"--if-match", "--idempotency-key"}, nil},
		{[]string{"publish"}, []string{"--if-match", "--no-wait"}, nil},
		{[]string{"recovery", "enter"}, []string{"--idempotency-key", "--expect-principal", "--restored"}, []string{"--expect-epoch", "--if-match"}},
	} {
		got := flags(c.path...)
		for _, h := range c.has {
			if !strings.Contains(got+",", h+",") {
				t.Errorf("%v help lacks %s: %q", c.path, h, got)
			}
		}
		for _, l := range c.lack {
			if strings.Contains(got+",", l+",") {
				t.Errorf("%v help lists %s, which it refuses: %q", c.path, l, got)
			}
		}
	}
	if _, _, errOut := runDrift(t, "act", "list", "--bogus"); !strings.Contains(errOut, "Flags: --all, --cursor, --limit") {
		t.Errorf("the usage error of act list carries no flags:\n%s", errOut)
	}
}

// TestDraftWritesShareTheirNote: every leaf that writes to a draft says how the write is guarded and
// repeated, and fragment set says it runs on Linux only.
func TestDraftWritesShareTheirNote(t *testing.T) {
	for _, path := range []string{"fragment set", "fragment rm", "profile set", "profile rm", "assignment set", "assignment rm", "draft discard"} {
		_, out, _ := runDrift(t, append(strings.Fields(path), "--help")...)
		for _, want := range []string{"A write to a draft sends the ETag", "--if-match <etag>", "Draft writes take author"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s --help lacks %q:\n%s", path, want, out)
			}
		}
	}
	if _, out, _ := runDrift(t, "fragment", "set", "--help"); !strings.Contains(out, "fragment set runs on Linux only.") {
		t.Errorf("fragment set --help does not say it runs on Linux only:\n%s", out)
	}
}

// TestSynopsisNamesRegisteredFlags: every --name a leaf's synopsis or notes writes is a flag its own flag set
// registers (the global flags included), so a flag renamed in code cannot leave a stale name in the text.
// A leaf's text may name a flag it does not take only to say so (--yes) and the built-in --help. A wildcard
// (--first-key-*) must be the prefix of at least one registered flag.
func TestSynopsisNamesRegisteredFlags(t *testing.T) {
	notTaken := map[string]bool{"yes": true, "help": true}
	for _, x := range tableLeaves() {
		words := strings.Fields(x.path)
		probe := &env{output: "table", err: io.Discard, out: io.Discard}
		_ = probe.handlers()[words[0]](append(slices.Clone(words[1:]), "--help"))
		names, wild, bad := flagsWritten(x.synopsis + "\n" + x.notes)
		for _, b := range bad {
			t.Errorf("%s writes %q, which is no complete flag name", x.path, b)
		}
		for _, name := range names {
			if !notTaken[name] && (probe.helped == nil || probe.helped.Lookup(name) == nil) {
				t.Errorf("%s writes --%s in its synopsis or notes, which its flag set does not register", x.path, name)
			}
		}
		for _, prefix := range wild {
			found := false
			if probe.helped != nil {
				probe.helped.VisitAll(func(f *flag.Flag) { found = found || strings.HasPrefix(f.Name, prefix+"-") })
			}
			if !found {
				t.Errorf("%s writes --%s-*, but its flag set registers no flag with that prefix", x.path, prefix)
			}
		}
	}
}

// flagsWritten reads the --names in a text: complete names, wildcard prefixes written --prefix-*, and the
// spellings that are neither (a name followed by _, a letter, a digit or a hyphen that is no wildcard).
func flagsWritten(text string) (names, wild, bad []string) {
	re := regexp.MustCompile(`--([a-z](?:[a-z0-9-]*[a-z0-9])?)(-\*)?`)
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[2]:m[3]]
		if m[1] < len(text) && strings.ContainsRune("_-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", rune(text[m[1]])) {
			bad = append(bad, text[m[0]:m[1]+1])
			continue
		}
		if m[4] >= 0 {
			wild = append(wild, name)
		} else {
			names = append(names, name)
		}
	}
	return names, wild, bad
}

// TestFlagsWrittenReadsOnlyCompleteNames: the spellings the synopsis test must not let through.
func TestFlagsWrittenReadsOnlyCompleteNames(t *testing.T) {
	names, wild, bad := flagsWritten("--a-b, --no-input_old, --no-prompt- x, --first-key-* y, --c.")
	if !slices.Equal(names, []string{"a-b", "c"}) || !slices.Equal(wild, []string{"first-key"}) || len(bad) != 2 {
		t.Errorf("names %v, wild %v, bad %v", names, wild, bad)
	}
}

// TestRefusedFlagsAreRegistered: every flag a table entry hides is one the leaf registers, so a flag that
// is renamed in code cannot leave a stale name behind in the table.
func TestRefusedFlagsAreRegistered(t *testing.T) {
	for _, x := range tableLeaves() {
		if x.not == "" {
			continue
		}
		words := strings.Fields(x.path)
		probe := &env{output: "table", err: io.Discard, out: io.Discard}
		_ = probe.handlers()[words[0]](append(slices.Clone(words[1:]), "--help"))
		for _, name := range strings.Fields(x.not) {
			if probe.helped == nil || probe.helped.Lookup(name) == nil {
				t.Errorf("%s hides --%s, which it does not register", x.path, name)
			}
		}
	}
}

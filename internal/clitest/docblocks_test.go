//go:build linux

// The client journey pages' command blocks, run from the pages (ginsys/bronzeward#405). A block opened
// by exactly "```bash {.bw-run .bw-db}" needs a secret provider and a machine, which the clean-install
// run (scripts/release/run-doc-blocks.sh) does not have: here each runs against the test server with a
// Talos stand-in, with the source-built client. Nothing in this file holds a copy of a command: each is
// read from its page, and only its <placeholders> are filled in, as a person fills them in.

package clitest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	dbOpener = "```bash {.bw-run .bw-db}"
	pagesDir = "../../docs/operator/clients"
	// commandLimit bounds one command, so that one that never ends fails by name instead of using up
	// the package's time limit.
	commandLimit = 2 * time.Minute
)

// docBlocks reads dir's pages and answers the marked blocks by id, "<page name>-db-<n>", the ids
// run-doc-blocks.sh account-db computes. A line that mentions the mark in any other form is refused,
// since the block it was meant to mark would be run by nothing. So is a "#" or an "&" in a marked
// block, since the block is run a command at a time: a comment could hide a command a shell would
// run, and a command put in the background would count as run before it ended.
func docBlocks(dir string) (map[string]string, error) {
	pages, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	blocks := map[string]string{}
	for _, page := range pages {
		b, err := os.ReadFile(page)
		if err != nil {
			return nil, err
		}
		name, n, id := strings.TrimSuffix(filepath.Base(page), ".md"), 0, ""
		for i, line := range strings.Split(string(b), "\n") {
			switch {
			case id == "" && line == dbOpener:
				n++
				id = fmt.Sprintf("%s-db-%d", name, n)
				blocks[id] = ""
			case id != "" && strings.ContainsAny(line, "#&"):
				return nil, fmt.Errorf("%s:%d: %s holds a # or an &; a marked block takes no comment and no background command", page, i+1, id)
			case id != "" && line == "```":
				if strings.TrimSpace(blocks[id]) == "" {
					return nil, fmt.Errorf("%s: %s is empty", page, id)
				}
				id = ""
			case id != "":
				blocks[id] += line + "\n"
			case strings.Contains(line, "bw-db"):
				return nil, fmt.Errorf("%s:%d: bw-db outside the opener %q at the start of a line", page, i+1, dbOpener)
			}
		}
		if id != "" {
			return nil, fmt.Errorf("%s: %s is not closed", page, id)
		}
	}
	return blocks, nil
}

// commands are a block's command lines, a line ending in a backslash joined to the next and a blank
// line left out.
func commands(block string) []string {
	var out []string
	var joined string
	for _, line := range strings.Split(block, "\n") {
		if before, ok := strings.CutSuffix(line, `\`); ok {
			joined += before
			continue
		}
		line, joined = joined+line, ""
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

var placeholder = regexp.MustCompile(`<[^<>]*>`)

// capture takes a placeholder's value from the output of a command that holds after, as a person
// copies the identifier the command printed.
type capture struct {
	after, placeholder string
	pattern            *regexp.Regexp
}

// pageRun is one scenario's session at the command line: the pages' commands run as written in dir,
// with the client on PATH and the current context of the fixture's home.
type pageRun struct {
	f      *fixture
	blocks map[string]string
	ran    map[string]bool
	dir    string
	// values fills the placeholders, in order for one that occurs more than once; the last value
	// goes on repeating.
	values map[string][]string
	secret func(string) []string // names the secrets an output holds, or nil
	limit  time.Duration         // bounds one command
}

func newPageRun(f *fixture, blocks map[string]string, ran map[string]bool) *pageRun {
	return &pageRun{f: f, blocks: blocks, ran: ran, dir: f.t.TempDir(), values: map[string][]string{}, limit: commandLimit}
}

func (p *pageRun) set(name string, values ...string) { p.values[name] = values }

// fill replaces command's placeholders; one without a value is an error.
func (p *pageRun) fill(command string) (string, error) {
	used := map[string]int{}
	var missing []string
	filled := placeholder.ReplaceAllStringFunc(command, func(name string) string {
		v := p.values[name]
		if len(v) == 0 {
			missing = append(missing, name)
			return name
		}
		i := min(used[name], len(v)-1)
		used[name]++
		return v[i]
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("no value for %s", strings.Join(missing, ", "))
	}
	return filled, nil
}

// block runs the marked block id, command by command, with stdin as each command's standard input,
// and answers what they printed. It stops at the first command that does not exit 0. A block counts
// as run, and is recorded, only when every one of its commands did.
func (p *pageRun) block(id, stdin string, captures ...capture) (string, error) {
	text, ok := p.blocks[id]
	if !ok {
		return "", fmt.Errorf("%s: no page marks this block", id)
	}
	var transcript strings.Builder
	lines := commands(text)
	if len(lines) == 0 {
		return "", fmt.Errorf("%s: the block has no command", id)
	}
	for _, written := range lines {
		command, err := p.fill(written)
		if err != nil {
			return transcript.String(), fmt.Errorf("%s: %q: %w", id, written, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), p.limit)
		cmd := exec.CommandContext(ctx, "sh", "-ec", command)
		cmd.Dir = p.dir
		cmd.Env = []string{"HOME=" + p.f.home, "XDG_CONFIG_HOME=" + p.f.home, "PATH=" + p.f.bin + ":" + os.Getenv("PATH")}
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		// The limit ends the shell's whole process group, and the wait for its output is bounded too: a
		// process left holding the pipes would otherwise keep the command from ever returning.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 5 * time.Second
		err = cmd.Run()
		timedOut := ctx.Err() != nil
		cancel()
		fmt.Fprintf(&transcript, "$ %s\n%s%s", command, errb.String(), out.String())
		if p.f.token != "" {
			p.f.scan(result{out: out.String(), err: errb.String()}, []string{command})
		}
		if p.secret != nil {
			if l := p.secret(command + out.String() + errb.String()); len(l) > 0 {
				p.f.t.Errorf("%s: %q: argv or output holds %v", id, command, l)
			}
		}
		var exit *exec.ExitError
		switch {
		case timedOut:
			return transcript.String(), fmt.Errorf("%s: %q did not end within %s", id, command, p.limit)
		case errors.As(err, &exit):
			return transcript.String(), fmt.Errorf("%s: %q exited %d:\n%s%s", id, command, exit.ExitCode(), errb.String(), out.String())
		case err != nil:
			return transcript.String(), fmt.Errorf("%s: %q: %w", id, command, err)
		}
		for _, c := range captures {
			if !strings.Contains(command, c.after) {
				continue
			}
			v := c.pattern.FindString(out.String() + errb.String())
			if v == "" {
				return transcript.String(), fmt.Errorf("%s: %q printed no %s", id, command, c.placeholder)
			}
			p.set(c.placeholder, v)
		}
	}
	if err := record(id, transcript.String()); err != nil {
		return transcript.String(), err
	}
	p.ran[id] = true
	return transcript.String(), nil
}

// must is block, failing the test on an error.
func (p *pageRun) must(id, stdin string, captures ...capture) string {
	p.f.t.Helper()
	out, err := p.block(id, stdin, captures...)
	if err != nil {
		p.f.t.Fatal(err)
	}
	return out
}

// record keeps a block's output and notes that it ran, in the directory BW_DOC_RECORD names, which
// mise run go-db checks against the pages' marks. Without that variable nothing is written.
func record(id, transcript string) error {
	dir := os.Getenv("BW_DOC_RECORD")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, id+".out"), []byte(transcript), 0o644); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "ran.list"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, id)
	return errors.Join(err, f.Close())
}

// recordHeader writes what the record was made from: the revision, the toolchain and the platform.
func recordHeader() error {
	dir := os.Getenv("BW_DOC_RECORD")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rev, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		rev = []byte("unknown\n")
	}
	header := fmt.Sprintf("revision %sgo %s\nhost %s/%s\nclient built from source; server with OpenBao and a Talos stand-in\n",
		rev, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return os.WriteFile(filepath.Join(dir, "doc-journeys.log"), []byte(header), 0o644)
}

// unrun are the marked blocks that no scenario ran.
func unrun(blocks map[string]string, ran map[string]bool) []string {
	var missing []string
	for id := range blocks {
		if !ran[id] {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	return missing
}

// TestDocBlocksControls plants each way a run could pass without having run what the pages mark, and
// wants every one of them refused. It needs no database, so it runs wherever the package is tested.
func TestDocBlocksControls(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	block := func(command string) string { return dbOpener + "\n" + command + "\n```\n" }
	write("page.md", "# A page\n\n"+block("bronzewardctl context list")+block("bronzewardctl context list --bogus")+
		block("bronzewardctl contxt list")+block("bronzewardctl draft show <draft>")+block("bronzewardctl context list"))
	blocks, err := docBlocks(dir)
	if err != nil || len(blocks) != 5 {
		t.Fatalf("docBlocks: %d blocks, %v; want 5", len(blocks), err)
	}
	t.Setenv("BW_DOC_RECORD", "")
	p := newPageRun(&fixture{t: t, bin: binaries(t), home: t.TempDir()}, blocks, map[string]bool{})
	if _, err := p.block("page-db-1", ""); err != nil {
		t.Fatalf("a good block: %v", err)
	}
	for name, c := range map[string]struct{ id, want string }{
		"a wrong flag in a marked block":    {"page-db-2", "exited 2"},
		"a wrong command in a marked block": {"page-db-3", "exited 2"},
		"a placeholder with no value":       {"page-db-4", "no value for <draft>"},
		"a block no page marks":             {"page-db-9", "no page marks this block"},
		"a block of another page's name":    {"other-db-1", "no page marks this block"},
	} {
		if _, err := p.block(c.id, ""); err == nil || !strings.Contains(err.Error(), c.id) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want an error naming %s with %q", name, err, c.id, c.want)
		}
		if p.ran[c.id] {
			t.Errorf("%s: counted as run", name)
		}
	}
	if got, want := unrun(blocks, p.ran), []string{"page-db-2", "page-db-3", "page-db-4", "page-db-5"}; !slices.Equal(got, want) {
		t.Errorf("unrun = %v, want %v", got, want)
	}

	// A command that never ends is stopped, the processes it started included, and fails by name; a
	// block with nothing to run counts as nothing.
	write("page.md", block("sleep 40; true")+block(`\`))
	hang, err := docBlocks(dir)
	if err != nil || len(hang) != 2 {
		t.Fatalf("docBlocks: %d blocks, %v; want 2", len(hang), err)
	}
	p.blocks, p.limit = hang, 2*time.Second
	for name, c := range map[string]struct{ id, want string }{
		"a command that does not end": {"page-db-1", "did not end within 2s"},
		"a block with no command":     {"page-db-2", "has no command"},
	} {
		p.ran = map[string]bool{}
		start := time.Now()
		if _, err := p.block(c.id, ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want %q", name, err, c.want)
		}
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("%s: it took %s", name, d)
		}
		if p.ran[c.id] {
			t.Errorf("%s: counted as run", name)
		}
	}

	for name, body := range map[string]string{
		"a comment that would hide a command": dbOpener + "\n# example \\\nbronzewardctl context list --bogus\n```\n",
		"a command left in the background":    dbOpener + "\nsh -c 'sleep 300; exit 7' >/dev/null 2>&1 &\n```\n",
		"a block that is only a comment":      dbOpener + "\n# disabled for now\n```\n",
		"another spelling of the mark":        "```bash {.bw-db .bw-run}\ntrue\n```\n",
		"an indented mark":                    "  " + dbOpener + "\n  true\n  ```\n",
		"an unclosed block":                   dbOpener + "\ntrue\n",
		"an empty block":                      dbOpener + "\n\n```\n",
	} {
		write("page.md", body)
		if _, err := docBlocks(dir); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

//go:build linux

package clitest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"go.yaml.in/yaml/v3"
)

// unmarked is a synthetic secret in a file's content of the stand-in's configuration, which the
// schema does not recognise, so only an operator's mark extracts it (#256 T3); unmarkedPath marks it.
const (
	unmarked     = "BWSYNTH-unmarked-secret"
	unmarkedPath = "doc[0]/machine/files/0/content"
)

// winsize is struct winsize of <asm-generic/termios.h>.
type winsize struct{ rows, cols, x, y uint16 }

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// terminal is bronzewardctl running on a pseudo-terminal. What the command writes to it is kept in
// screen, in memory only: it is never written to a file or into a failure message, and it is
// cleared once matched (client-contracts.md §11.5 item 1).
type terminal struct {
	t      *testing.T
	master *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	screen []byte
	read   chan struct{}
	once   sync.Once
	code   int
	before syscall.Termios // the modes before the command started
}

// modes reads the pair's terminal modes through the master.
func (tm *terminal) modes() syscall.Termios {
	tm.t.Helper()
	var m syscall.Termios
	if err := ioctl(tm.master, syscall.TCGETS, unsafe.Pointer(&m)); err != nil {
		tm.t.Errorf("reading the terminal modes: %v", err)
	}
	return m
}

// restored checks that the command put the terminal back in the modes it started in: line
// editing, echo and signal keys on again, as before the pager's raw mode.
func (tm *terminal) restored(when string) {
	tm.t.Helper()
	m := tm.modes()
	if m.Iflag != tm.before.Iflag || m.Oflag != tm.before.Oflag || m.Cflag != tm.before.Cflag || m.Lflag != tm.before.Lflag {
		tm.t.Errorf("%s, the terminal modes are iflag %#x oflag %#x cflag %#x lflag %#x; want the modes before, %#x %#x %#x %#x",
			when, m.Iflag, m.Oflag, m.Cflag, m.Lflag, tm.before.Iflag, tm.before.Oflag, tm.before.Cflag, tm.before.Lflag)
	}
}

// onTerminal starts bronzewardctl in the human context on a new pseudo-terminal of 24 rows and 80
// columns, as its standard input and error, and as its standard output unless out is given. It is
// killed if it has not ended after two minutes.
func (e *importEnv) onTerminal(out io.Writer, args ...string) *terminal {
	t := e.t
	t.Helper()
	argv := strings.Join(args, " ")
	if l := append(protectedLeaks(argv), e.st.leaks(argv)...); len(l) > 0 {
		t.Errorf("bronzewardctl %v: argv holds %v", args, l)
	}
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var unlock int32
	var n uint32
	for _, c := range []struct {
		req uintptr
		arg unsafe.Pointer
	}{{syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)}, {syscall.TIOCGPTN, unsafe.Pointer(&n)}, {syscall.TIOCSWINSZ, unsafe.Pointer(&winsize{rows: 24, cols: 80})}} {
		if err := ioctl(m, c.req, c.arg); err != nil {
			t.Fatalf("setting up the pseudo-terminal: %v", err)
		}
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := e.f.command([]string{"TMPDIR=" + e.tmp}, append([]string{"--context", "human"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
	if out != nil {
		cmd.Stdout = out
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	tm := &terminal{t: t, master: m, cmd: cmd, read: make(chan struct{})}
	tm.before = tm.modes()
	if tm.before.Lflag&(syscall.ICANON|syscall.ECHO|syscall.ISIG) != syscall.ICANON|syscall.ECHO|syscall.ISIG {
		t.Fatalf("the new pseudo-terminal is not in line mode with echo and signal keys: lflag %#x", tm.before.Lflag)
	}
	err = cmd.Start()
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(tm.read)
		b := make([]byte, 4096)
		for {
			n, err := m.Read(b)
			tm.mu.Lock()
			tm.screen = append(tm.screen, b[:n]...)
			tm.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	kill := time.AfterFunc(2*time.Minute, func() { cmd.Process.Kill() })
	t.Cleanup(func() { kill.Stop(); tm.wait(); tm.wipe() })
	return tm
}

// until waits up to 30 s for the screen to satisfy ok; a failure names only what was awaited.
func (tm *terminal) until(what string, ok func([]byte) bool) bool {
	tm.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		tm.mu.Lock()
		done := ok(tm.screen)
		tm.mu.Unlock()
		select {
		case <-tm.read:
			if !done {
				tm.mu.Lock()
				done = ok(tm.screen)
				tm.mu.Unlock()
			}
			if !done {
				tm.t.Errorf("the terminal closed before it showed %s", what)
			}
			return done
		default:
		}
		if done {
			return true
		}
		if time.Now().After(deadline) {
			tm.t.Errorf("the terminal did not show %s", what)
			return false
		}
	}
}

// count is how many times the screen holds s.
func (tm *terminal) count(s string) int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return bytes.Count(tm.screen, []byte(s))
}

// more waits until the screen holds s more than n times.
func (tm *terminal) more(what, s string, n int) bool {
	tm.t.Helper()
	return tm.until(what, func(b []byte) bool { return bytes.Count(b, []byte(s)) > n })
}

// shows waits until the screen holds s.
func (tm *terminal) shows(what, s string) bool {
	tm.t.Helper()
	return tm.more(what, s, 0)
}

func (tm *terminal) send(s string) {
	if _, err := tm.master.WriteString(s); err != nil {
		tm.t.Errorf("typing on the terminal: %v", err)
	}
}

// wait waits for the command to end and returns its exit code. The screen stays in memory.
func (tm *terminal) wait() int {
	tm.once.Do(func() {
		var exit *exec.ExitError
		if err := tm.cmd.Wait(); errors.As(err, &exit) {
			tm.code = exit.ExitCode()
		} else if err != nil {
			tm.t.Errorf("bronzewardctl: %v", err)
			tm.code = -1
		}
		select {
		case <-tm.read:
		case <-time.After(10 * time.Second):
			tm.t.Error("the terminal stayed open after the command ended")
		}
		tm.master.Close()
	})
	return tm.code
}

// wipe clears the screen.
func (tm *terminal) wipe() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	clear(tm.screen)
	tm.screen = nil
}

const (
	reviewNotice = "[ protected review: this client writes it nowhere but this terminal. Terminal scrollback,"
	keyLine      = "space or f forward, b back, q quit"
)

// review runs import review -v of ing on a terminal: it pages to the end, back once, quits with the
// key quit (q or Ctrl-C), and types marks at the prompt and then an empty line. It checks that the
// terminal modes at the prompt are those before the pager, that the screen shows the notice before
// the pager, the review in the alternate screen, the unmarked secret only when secret is set and
// never before or after the pager, where the -v lines are, and each of want, and returns the exit
// code.
func (e *importEnv) review(ing, quit string, secret bool, marks []string, want ...string) int {
	e.t.Helper()
	tm := e.onTerminal(nil, "-v", "import", "review", ing)
	defer tm.wipe()
	if !tm.shows("the notice", reviewNotice) || !tm.shows("the pager", keyLine) {
		return tm.wait()
	}
	for n := 1; tm.count("-- (end) --") == 0; n++ {
		if n > 60 {
			e.t.Error("the pager did not reach the end in 60 pages")
			break
		}
		tm.send("f")
		if !tm.more("the next page", keyLine, n) {
			return tm.wait()
		}
	}
	n := tm.count(keyLine)
	tm.send("b")
	tm.more("the page before", keyLine, n)
	tm.send(quit)
	for i, m := range append(slices.Clone(marks), "") {
		if !tm.more("the marks prompt", "mark> ", i) {
			break
		}
		if i == 0 {
			tm.restored(fmt.Sprintf("after the pager quit on %q", quit))
		}
		tm.send(m + "\n")
	}
	code := tm.wait()
	tm.mu.Lock()
	defer tm.mu.Unlock()
	s := string(tm.screen)
	notice, on, off := strings.Index(s, reviewNotice), strings.Index(s, "\x1b[?1049h"), strings.LastIndex(s, "\x1b[?1049l")
	if notice < 0 || on < notice || off < on {
		e.t.Errorf("the screen does not show the notice, then the review in the alternate screen")
	}
	if shown := on >= 0 && off > on && strings.Contains(s[on:off], unmarked); shown != secret {
		e.t.Errorf("the review shows the unmarked secret: %v; want %v", shown, secret)
	}
	if on >= 0 && strings.Contains(s[:on], unmarked) {
		e.t.Error("the unmarked secret is shown before the pager")
	}
	if off >= 0 && strings.Contains(s[off:], unmarked) {
		e.t.Error("the unmarked secret is shown after the pager")
	}
	for _, w := range want {
		if !strings.Contains(s, w) {
			e.t.Errorf("the screen does not show %q", w)
		}
	}
	return code
}

// reviews counts the review reads of ing the injector saw since its last set.
func (e *importEnv) reviews(ing string) int {
	n := 0
	for _, r := range e.in.requests() {
		if r == "GET /api/v1/ingestions/"+ing+"/review" {
			n++
		}
	}
	return n
}

// mark runs import mark -v of ing with marks on standard input.
func (e *importEnv) mark(ing string, marks ...string) result {
	return e.ctl([]string{"TMPDIR=" + e.tmp}, strings.Join(marks, "\n")+"\n", "--context", "human", "-v", "import", "mark", ing)
}

// lapse waits up to 30 s until import status shows the claim's lease lapsed.
func (e *importEnv) lapse(ing string) {
	e.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !strings.Contains(e.run("import", "status", ing).out, "Its run stopped renewing the lease."); time.Sleep(300 * time.Millisecond) {
		if time.Now().After(deadline) {
			e.t.Fatal("the claim's lease did not lapse")
		}
	}
}

// TestProtected runs the protected review and marks through the CLI against a server with a
// provider, short claim timers and a Talos stand-in whose configuration holds an unmarked
// synthetic secret: the cases of testdata/protected-cases.yaml (PROT-01 to PROT-05, PROT-06's
// first), the journey task T3's import part, and the leak scan. A review runs on a
// pseudo-terminal, its output matched in memory and never retained.
func TestProtected(t *testing.T) {
	b, err := os.ReadFile("testdata/protected-cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(b, &records); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{"PROT-01", "PROT-02", "PROT-03", "PROT-04", "PROT-05", "PROT-06"}) {
		t.Fatalf("protected-cases.yaml holds %v; want the records PROT-01 to PROT-06", ids)
	}
	f, bao := startTimers(t, "viewer", importTimers)
	e := newImportEnv(t, f, bao)
	e.tmp = t.TempDir()
	e.st = f.newStandIn(t, bao.b, e.s, func(args ...string) result { return e.ctl(nil, "", args...) }, unmarked)

	t.Run("PROT-01", func(t *testing.T) {
		e := e.sub(t)
		r, ing := e.start("", "--review")
		e.f.want(r, 0, "Paused for review (generation 1", "bronzewardctl import review "+ing)
		if code := e.review(ing, "q", true, []string{unmarkedPath}, "Mark a value as secret by its path, one per line; empty line to finish:",
			"Marked 1 path; re-staged and paused again (generation 2)."); code != 0 {
			t.Errorf("the review and mark exited %d", code)
		}
		// Reviewed again, the marked value is a reference; Ctrl-C quits the pager as q does.
		if code := e.review(ing, "\x03", false, nil, "No marks given."); code != 0 {
			t.Errorf("the second review exited %d", code)
		}
		e.f.want(e.run("import", "continue", ing), 0, "Import "+ing+" released into draft "+e.s.draft)
	})

	t.Run("PROT-02", func(t *testing.T) {
		e := e.sub(t)
		_, ing := e.start("", "--review")
		var out bytes.Buffer
		tm := e.onTerminal(&out, "import", "review", ing)
		code := tm.wait()
		tm.shows("the refusal", "only when standard input and standard output are a terminal, with no override; nothing was read or written")
		tm.wipe()
		if code != 2 || out.Len() != 0 {
			t.Errorf("standard output redirected: exit %d, %d bytes written; want 2 and none", code, out.Len())
		}
		e.f.want(e.run("import", "review", ing), 2, "nothing was read or written")
		e.f.want(e.run("import", "review", ing, "-o", "json"), 2, "has no -o json or yaml form")
		if n := e.reviews(ing); n != 0 {
			t.Errorf("the review was read %d times", n)
		}
		e.f.want(e.run("import", "abandon", ing), 0)
	})

	t.Run("PROT-03", func(t *testing.T) {
		e := e.sub(t)
		_, ing := e.start("", "--review")
		r := e.mark(ing, unmarkedPath, "BWSYNTH-not-a-path")
		e.f.want(r, 1, "answered 422 ", "(validation-failed)", "Refused: position 1.")
		for _, c := range []struct {
			name, mark string
			gen        int
			fail       string
			want       string
		}{
			{"compilation refuses it", "doc[0]/machine/BWSYNTH-absent", 2, "",
				"was not applied: it was refused (422 validation-failed, rule mark-unaddressed, the mark at position 0 (the first is 0)). The import is paused again at generation 2."},
			{"the provider fails", unmarkedPath, 3, "/v1/secret/data/", "was not applied: it was refused (503 dependency-unavailable). The import is paused again at generation 3."},
		} {
			if c.fail != "" {
				e.bao.fail.Store(&c.fail)
			}
			r := e.mark(ing, c.mark)
			e.bao.fail.Store(nil)
			e.f.want(r, 1, fmt.Sprintf("  marked (generation %d)\n", c.gen), fmt.Sprintf("  mark-refused (generation %d)\n", c.gen), c.want,
				"bronzewardctl import review "+ing)
			if strings.Contains(r.out+r.err, "Marked") || strings.Contains(r.out+r.err, c.mark) {
				t.Errorf("%s: reported as accepted, or the path shown", c.name)
			}
		}
		if state, staged, op := e.claim(ing); state != "paused" || !staged || op != "running" {
			t.Errorf("claim %s (staged %v), operation %s; want paused, staged, running", state, staged, op)
		}
		e.f.want(e.run("import", "abandon", ing), 0)
	})

	t.Run("PROT-04", func(t *testing.T) {
		e := e.sub(t)
		_, ing := e.start("", "--review")
		var cut atomic.Bool
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if !on("/ingestions/"+ing+"/review")(r) || !cut.CompareAndSwap(false, true) {
				return false
			}
			// The connection drops part way through the answer.
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"ingestion":"`+ing+`","document":"`)
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}, nil)
		if code := e.review(ing, "q", true, nil, "No marks given."); code != 0 {
			t.Errorf("the review exited %d", code)
		}
		if n := e.reviews(ing); n != 2 {
			t.Errorf("the review was read %d times; want 2, the dropped read and the one after it", n)
		}
		e.f.want(e.run("import", "abandon", ing), 0)
	})

	t.Run("PROT-05", func(t *testing.T) {
		e := e.sub(t)
		_, ing := e.start("", "--review")
		decrypt := "/v1/transit/decrypt/"
		e.bao.fail.Store(&decrypt)
		r := e.mark(ing, unmarkedPath)
		e.bao.fail.Store(nil)
		e.f.want(r, 1, "  marked (generation 2)\n", "  resume-failed: dependency-unavailable\n",
			"the mark on import "+ing+" was not applied: its staged input could not be decrypted (resume-failed, dependency-unavailable)",
			"bronzewardctl import takeover "+ing, "bronzewardctl import abandon "+ing)
		if state, staged, op := e.claim(ing); state != "held" || !staged || op != "running" {
			t.Errorf("claim %s (staged %v), operation %s; want held, staged, running", state, staged, op)
		}
		// Once the lease lapses the takeover resumes without the mark and pauses for the review.
		e.lapse(ing)
		e.f.want(e.run("import", "takeover", ing), 0, "Paused for review (generation 3")
		e.f.want(e.run("import", "abandon", ing), 0)
	})

	t.Run("PROT-06, a mark and the serving process killed", func(t *testing.T) {
		e := e.sub(t)
		_, ing := e.start("", "--review")
		hold := make(chan struct{})
		e.bao.hold.Store(&hold)
		released := false
		release := func() {
			if !released {
				released = true
				e.bao.hold.Store(nil)
				close(hold)
			}
		}
		defer release()
		held := e.bao.held.Load()
		done := make(chan result, 1)
		began := time.Now()
		go func() { done <- e.mark(ing, unmarkedPath) }()
		err := e.crash(held)
		r := <-done
		if err != nil {
			t.Fatal(err)
		}
		e.f.want(r, 3, "  marked (generation 2)\n", "the mark on import "+ing+": its claim is held and its lease lapsed", "still runs: the outcome is unknown",
			"bronzewardctl import status "+ing, "bronzewardctl import takeover "+ing, "bronzewardctl import abandon "+ing)
		if strings.Contains(r.err, "not applied") {
			t.Error("the outcome was reported as not applied")
		}
		if took := time.Since(began); took > time.Minute {
			t.Errorf("the wait took %v; it must end once the lease lapsed", took)
		}
		release()
		e.f.want(e.run("import", "takeover", ing), 0, "Paused for review (generation 3")
		e.f.want(e.run("import", "abandon", ing), 0)
	})

	t.Run("T3", func(t *testing.T) { e.sub(t).journey(t) })

	t.Run("leak scan", func(t *testing.T) {
		e := e.sub(t)
		plant := filepath.Join(t.TempDir(), "planted")
		for _, v := range append(slices.Clone(sentinels), unmarked) {
			if err := os.WriteFile(plant, []byte("x "+v+" y"), 0o600); err != nil {
				t.Fatal(err)
			}
			if leaked := e.leakFiles(filepath.Dir(plant)); !slices.Equal(leaked, []string{plant}) {
				t.Fatalf("the scan misses the planted %s: %v", v, leaked)
			}
		}
		if entries, err := os.ReadDir(e.tmp); err != nil || len(entries) != 0 {
			t.Errorf("the CLI's temporary directory holds %v (%v)", entries, err)
		}
		files := 0
		for _, dir := range []string{filepath.Join(e.f.home, ".local", "state", "bronzeward"), filepath.Join(e.f.home, "bronzeward")} {
			if leaked := e.leakFiles(dir); len(leaked) > 0 {
				t.Errorf("%v hold a sentinel or a secret of the stand-in", leaked)
			}
			entries, _ := os.ReadDir(dir)
			files += len(entries)
		}
		if files == 0 {
			t.Error("the scan read no file")
		}
	})
	f.scanFiles()
}

// leakFiles names the files under dir that hold a sentinel or a secret of the stand-in.
func (e *importEnv) leakFiles(dir string) []string {
	var leaked []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil || len(protectedLeaks(string(b))) > 0 || len(e.st.leaks(string(b))) > 0 {
			leaked = append(leaked, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	return leaked
}

// expiresAt is the claim's expiry a pause prints, which differs on every run.
var expiresAt = regexp.MustCompile(`expires \d{4}-\d\d-\d\d \d\d:\d\d:\d\d \S+?\)`)

// journey runs the journey task T3 (operator-experience.md §13) as a script through the CLI: a
// draft, an import with review, the review on a terminal marking the unmarked secret, the
// continuation, the machine's assignment and the publication, then the handover (#354): the adopt
// plan, its approval by the plan ID typed back, the adoption's operation followed to its end, and
// the machine read, whose Applied is the release, set by the adoption. The import leaves nothing to
// assign (the draft holds the import base only), so a fragment is saved for the assignment to name.
// Its probe "Is the secret in the draft?" is answered by the release's provenance, which names the
// marked path as a reference. The output is kept in testdata/journeys/t3.txt, except the review's,
// which stays in memory.
func (e *importEnv) journey(t *testing.T) {
	var draft, ing, rel, plan, op, show string
	var out strings.Builder
	labels := filepath.Join(e.dir, "worker-labels.yaml")
	if err := os.WriteFile(labels, []byte("machine:\n  nodeLabels:\n    tier: workers\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, step := range [][]string{
		{"draft", "create", "--cluster", "{cluster}", "--title", "import worker"},
		{"import", "start", "--machine", "{machine}", "--draft", "{draft}", "--review"},
		{"import", "review", "{ingestion}"},
		{"import", "continue", "{ingestion}"},
		{"fragment", "set", "worker-labels", "--draft", "{draft}", "--layer", "role", "--file", "worker-labels.yaml"},
		{"assignment", "set", "{machine}", "--draft", "{draft}", "--fragment", "role=worker-labels"},
		{"publish", "{draft}"},
		{"release", "provenance", "{release}", "{machine}"},
		{"plan", "create", "--release", "{release}", "--machine", "{machine}", "--operation", "adopt"},
		{"approve", "{plan}"},
		{"plan", "show", "{plan}"},
		{"operation", "watch", "{operation}"},
		{"machine", "show", "{machine}"},
	} {
		args := slices.Clone(step)
		for i, a := range args {
			args[i] = strings.NewReplacer("worker-labels.yaml", labels, "{draft}", draft, "{ingestion}", ing, "{release}", rel, "{machine}", e.s.machine, "{cluster}", e.s.cluster,
				"{plan}", plan, "{operation}", op).Replace(a)
		}
		fmt.Fprintf(&out, "$ bronzewardctl --context human %s\n", strings.Join(step, " "))
		if step[1] == "review" {
			code := e.review(ing, "q", true, []string{unmarkedPath}, "Marked 1 path; re-staged and paused again (generation 2).")
			fmt.Fprintf(&out, "(the protected review, on a terminal: matched in memory, not kept)\n[exit %d]\n\n", code)
			continue
		}
		var r result
		if step[0] == "approve" {
			r = e.ctl(nil, plan+"\n", append([]string{"--context", "human"}, args...)...)
		} else {
			r = e.run(args...)
		}
		switch {
		case step[1] == "create" && step[0] == "draft":
			draft = regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(r.out)
		case step[1] == "start":
			ing = ingID.FindString(r.out)
		case step[0] == "publish":
			rel = regexp.MustCompile(`rel_[a-z2-7]{26}`).FindString(r.out)
		case step[1] == "create":
			plan = planID.FindString(r.out)
		case step[0] == "approve":
			e.committed(plan)
		case step[1] == "show" && step[0] == "plan":
			op = opID.FindString(r.out)
		case step[1] == "show" && step[0] == "machine":
			show = r.out
		}
		if r.code != 0 {
			t.Errorf("bronzewardctl %v: exit %d\n%s%s", args, r.code, r.out, r.err)
		}
		fmt.Fprintf(&out, "%s%s[exit %d]\n\n", r.out, r.err, r.code)
	}
	// #350 lists the assignment between the continuation and the publication.
	if i, j := strings.Index(out.String(), "Assignment saved"), strings.Index(out.String(), "Publication succeeded"); i < 0 || j < i {
		t.Errorf("the journey has no assignment before its publication:\n%s", out.String())
	}
	if !strings.Contains(out.String(), ", "+unmarkedPath+" ") {
		t.Errorf("the provenance does not name %s as a reference", unmarkedPath)
	}
	if want := "adopted; the adoption of plan " + plan; !strings.Contains(out.String(), want) {
		t.Errorf("the handover shows no %q", want)
	}
	// The approve step prints the same release as a prediction, so only the machine read proves it.
	if want := "attributed to " + rel + ", which is the Applied release (by adoption)"; !strings.Contains(show, want) {
		t.Errorf("the machine read shows no %q:\n%s", want, show)
	}
	kept := expiresAt.ReplaceAllString(out.String(), "expires <time>)")
	kept = localTimes.ReplaceAllString(eventTimes.ReplaceAllString(kept, "$1 <time>  "), "<local time>")
	kept = controllerID.ReplaceAllString(kept, "<controller>")
	// approve's prompt ends in a space, which the repository's whitespace check refuses in a kept file.
	keepJourney(t, "t3", regexp.MustCompile(`(?m)[ \t]+$`).ReplaceAllString(kept, ""), e.dir)
}

var (
	// controllerID is a controller's identifier in the CLI's output: its instance, process and a
	// random part, which differ on every run.
	controllerID = regexp.MustCompile(`[^,\s]+/\d+/[A-Z2-7]{26}`)
	// eventTimes are operation watch's event times, the time this client received each event.
	eventTimes = regexp.MustCompile(`(?m)^(received) \d\d:\d\d:\d\d  `)
)

// committed waits until plan is committed, as the controller commits an approved adopt plan.
func (e *importEnv) committed(plan string) {
	e.t.Helper()
	var state string
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if err := e.f.db.QueryRow(`SELECT state FROM plan_state WHERE plan = $1`, plan).Scan(&state); err == nil && state == "committed" {
			return
		}
	}
	e.t.Fatalf("plan %s is %q after two minutes; want committed", plan, state)
}

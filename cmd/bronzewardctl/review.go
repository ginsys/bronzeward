package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// reviewNotice comes before the review (client-contracts.md §11.3 item 2).
const reviewNotice = "[ protected review: this client writes it nowhere but this terminal. Terminal scrollback,\n" +
	"  session recording and screen capture are outside its control. ]"

// importReview shows a paused import's staged document (GET /ingestions/{id}/review, author, human
// only) through the in-process pager, then reads marks from the prompt and sends them as import
// mark does (client-contracts.md §11.3, #256 T14). It is refused, with nothing read or written,
// unless standard input and output are a terminal, and it has no -o json, yaml, save or export
// form. The review is held in memory only.
func (e *env) importReview(args []string) (err error) {
	pos, err := e.parse("import review", args, 1, nil)
	if err != nil {
		return err
	}
	if err := protectedOn(runtime.GOOS); err != nil {
		return err
	}
	out := e.rawFile()
	switch {
	case e.output != "table":
		return usageError{errors.New("import review has no -o json or yaml form: it shows the review on a terminal only; nothing was read")}
	case e.noInput || e.tokenStdin:
		return usageError{errors.New("import review reads keys and marks from the terminal on standard input, so it takes neither --no-input nor --token-stdin; nothing was read")}
	case out == nil || !isTerminal(out) || !isTerminal(stdin):
		return usageError{errors.New("import review shows the review only when standard input and standard output are a terminal, with no override; nothing was read or written")}
	}
	rows, cols, err := screenSize(out)
	if err != nil {
		return err
	}
	ing := pos[0]
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	// GET /server checks the server's revision on the context's use (§7 item 3) before the review
	// is read.
	if _, _, err := c.Server(); err != nil {
		return err
	}
	r, err := c.Get("/ingestions/" + url.PathEscape(ing) + "/review")
	if err != nil {
		return err
	}
	var rv apitypes.IngestionReviewBody
	err = json.Unmarshal(r.Body, &rv)
	clear(r.Body)
	if err != nil {
		return fmt.Errorf("GET /api/v1/ingestions/%s/review cannot be read: %w", url.PathEscape(ing), err)
	}
	if _, err := fmt.Fprintln(out, reviewNotice); err != nil {
		return err
	}
	if err := e.pager(out, reviewText(rv), rows, cols); err != nil {
		return err
	}
	marks, err := e.readMarks(false)
	if err != nil {
		return err
	}
	if len(marks) == 0 {
		return e.say(append([]string{"No marks given. Review again, continue or abandon it:"}, remedies(ing, "review", "continue", "abandon")...)...)
	}
	return e.held(func() error { return e.sendMarks(c, ing, marks, nil, false) })
}

// rawFile is standard output as the file it is beneath the table guard, or nil when it is no file.
// The pager writes its own terminal control sequences, which the guard would escape, and it escapes
// the review's text itself.
func (e *env) rawFile() *os.File {
	w := e.out
	if g, ok := w.(tableGuard); ok {
		w = g.raw
	}
	f, _ := w.(*os.File)
	return f
}

// screenSize is the terminal out's rows and columns, 24 by 80 when it does not say. A terminal
// smaller than the pager needs is refused before anything is read.
func screenSize(out *os.File) (rows, cols int, err error) {
	cols, rows, err = term.GetSize(int(out.Fd())) // width, then height
	if err != nil {
		rows, cols = 24, 80
	}
	if rows < minRows || cols < minCols {
		return 0, 0, usageError{fmt.Errorf("import review needs a terminal at least %d columns wide and %d rows high, and this one is %d by %d; nothing was read",
			minCols, minRows, cols, rows)}
	}
	return rows, cols, nil
}

// makeRaw is term.MakeRaw, and pageKeys the keys the pager reads; a test replaces them to act in
// the window before the terminal is raw and after the key that quits.
var (
	makeRaw  = term.MakeRaw
	pageKeys = func(k keySource) keySource { return k }
)

// endedBySignal is the error of a pager that SIGINT, SIGTERM or SIGHUP ended: the terminal is back
// in line mode and off the alternate screen, and the process is to end by that signal (endByCause).
type endedBySignal struct{ sig syscall.Signal }

func (e endedBySignal) Error() string { return "ended by signal " + e.sig.String() }

// pager shows lines on the terminal out, of rows by cols, in the alternate screen, with standard
// input in raw mode for the keys, and restores both before it returns, so the review leaves the
// screen. It takes SIGINT, SIGTERM and SIGHUP from before the terminal is raw until it is restored,
// so none falls in the window between and ends the process with the terminal raw, and it returns
// endedBySignal for the first one, on every return after that, also one that came too late to reach
// a key wait or while the pager was being set up. A signal the process inherited as ignored stays
// ignored and is not taken. The pager is the only writer to out and writes from one goroutine, so
// the restore never races a redraw. A SIGWINCH reads the size again and shows the page that starts
// at the same line; it has a channel of its own, so a burst of resizes cannot displace a
// termination signal.
func (e *env) pager(out *os.File, lines []string, rows, cols int) (err error) {
	fd := int(stdin.Fd())
	terms := make(chan os.Signal, 4)
	winch := make(chan os.Signal, 1)
	// A signal the process inherited as ignored (nohup, a shell's background job) stays ignored: Go
	// would restore that disposition after Stop, so the process could not end by it.
	var take []os.Signal
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			take = append(take, sig)
		}
	}
	if len(take) > 0 { // Notify with no signals takes all of them
		signal.Notify(terms, take...)
	}
	signal.Notify(winch, syscall.SIGWINCH)
	keys, keysErr := newLiveKeys(fd)
	var old *term.State
	raw := false
	// The one exit: restore the terminal, then stop delivery and take what was already sent, so that
	// a signal queued at any return is reported, and it wins over the result.
	defer func() {
		if raw {
			_ = term.Restore(fd, old)
		}
		signal.Stop(terms)
		signal.Stop(winch)
		close(terms)
		close(winch)
		var ended os.Signal
		if keys != nil {
			<-keys.done
			ended = keys.endedBy()
			keys.close()
		} else {
			ended = <-terms
		}
		if s, ok := ended.(syscall.Signal); ok {
			err = endedBySignal{s}
		}
	}()
	if keysErr != nil {
		return fmt.Errorf("preparing the review's keys: %w; nothing was shown", keysErr)
	}
	go keys.watch(terms, winch)
	if old, err = makeRaw(fd); err != nil {
		return fmt.Errorf("putting the terminal in raw mode for the review: %w; nothing was shown", err)
	}
	raw = true
	if _, err := fmt.Fprint(out, altScreenOn); err != nil {
		return err
	}
	// A resize to less than the pager needs is 0 by 0: the pager shows a notice until it is usable.
	size := func() (int, int) {
		r, c, err := screenSize(out)
		if err != nil {
			return 0, 0
		}
		return r, c
	}
	err = pageOn(lines, size, pageKeys(keys), out)
	_, _ = fmt.Fprint(out, clearScreen+altScreenOff)
	return err
}

// reviewText is the review as the pager shows it, as its lines: the staged document's lines, then
// its declarations, one line each. Only the document's own line feeds start a line. A declared
// path, kind or format is put in its line as it came, and the pager escapes within each line, so a
// line feed in one shows as \x0a inside its line, and the pager's wrap never cuts that escape.
func reviewText(rv apitypes.IngestionReviewBody) []string {
	lines := strings.Split(strings.TrimSuffix(rv.Document, "\n"), "\n")
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range rv.Declarations.References {
			if !yield(p) {
				return
			}
		}
	})
	lines = append(lines, "", fmt.Sprintf("# declared references: %d", len(paths)))
	for _, p := range paths {
		ref := rv.Declarations.References[p]
		lines = append(lines, fmt.Sprintf("#   %s: %s version %d", p, ref.Kind, ref.Version))
	}
	lines = append(lines, fmt.Sprintf("# declared embedded documents: %d", len(rv.Declarations.Embedded)))
	for _, d := range rv.Declarations.Embedded {
		lines = append(lines, fmt.Sprintf("#   %s: %s", d.Path, d.Format))
	}
	return lines
}

// importMark marks values of a paused import as secret by their paths (POST
// /ingestions/{id}/marks, author, human only), the paths read from the prompt or standard input,
// never from an argument or a file, and, unless --no-wait, follows the run the mark starts
// (client-contracts.md §11.3 item 3, #256 T15).
func (e *env) importMark(args []string) (err error) {
	var noWait bool
	var rf repeatFlags
	pos, err := e.parse("import mark", args, 1, func(fs *flag.FlagSet) {
		fs.BoolVar(&noWait, "no-wait", false, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	if err := protectedOn(runtime.GOOS); err != nil {
		return err
	}
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	marks, err := e.readMarks(false)
	if err != nil {
		return err
	}
	return e.held(func() error { return e.sendMarks(c, pos[0], marks, rp, noWait) })
}

// sendMarks sends marks on ing and, unless noWait, follows the run.
func (e *env) sendMarks(c *client.Client, ing string, marks []string, rp *client.Repeat, noWait bool) error {
	if len(marks) == 0 {
		return usageError{errors.New("no marks were given; nothing was sent")}
	}
	body, err := json.Marshal(apitypes.MarkInput{Marks: marks})
	if err == nil && len(body) > maxBody {
		err = fmt.Errorf("the request body is %d bytes, over the server's 1 MiB limit; nothing was sent", len(body))
	}
	if err != nil {
		return err
	}
	a := accepted{marks: len(marks)}
	err = e.writeWith(c, nil, &client.Write{Method: http.MethodPost, Path: "/ingestions/" + url.PathEscape(ing) + "/marks", Body: body,
		Repeat: rp, HumanOnly: true, Check: "import status " + client.ShellWord(ing), Read: ingestionRead(ing)},
		func(_ *client.Client, r *client.Response) ([]string, error) {
			err := json.Unmarshal(r.Body, &a)
			lines := []string{sf("Sent %s for import %s (operation %s, generation %d).", paths(a.marks), a.Ingestion, a.Operation, a.Generation)}
			if noWait {
				lines = append(lines, "Follow it with: bronzewardctl import status "+pr(a.Ingestion))
			}
			return lines, err
		})
	if err != nil || noWait {
		return err
	}
	if a.Generation == 0 {
		return &client.Blocked{Err: fmt.Errorf("%s: the server accepted the mark without naming its generation. Read the import with:", c.Context.Where()),
			More: remedies(ing, "status")}
	}
	return e.follow(c, a, 0)
}

func paths(n int) string {
	if n == 1 {
		return "1 path"
	}
	return fmt.Sprintf("%d paths", n)
}

// markOutcome reports how the run of a mark settled (client-contracts.md §11.3, #256 T15): paused
// at the mark's generation is applied; a mark-refused event there, a resume-failed event or a
// later takeover leaves it not applied, exit 1; a failed operation is exit 4; and a lease that
// lapsed with none of these leaves it unknown, exit 3. Only the paused event applies it, so an
// operation that succeeded without one is not applied either, unless its last events were not
// shown (unshown): then the outcome is unknown, exit 3. An operation read terminal ends the import
// whatever the run settled before: ended reports it, a failure exit 4, a success with the draft's
// ETag kept as after any import this client started, and its record of the import ends (§8.3). A
// mark not applied before a success is reported beside it, with the draft to read, not the
// commands of a paused import.
func (e *env) markOutcome(c *client.Client, a accepted, b *apitypes.IngestionBody, o *apitypes.OperationBody, s settlement, unshown bool) error {
	what := "the mark on import " + b.Id
	if client.Terminal(o.State) {
		var why error
		switch {
		case o.State != "succeeded":
			return e.ended(c, b, o)
		case s.how == "paused":
			// Applied at its pause, then continued and released since.
			if err := e.say(sf("Marked %s; re-staged and paused at generation %d, and released since.", paths(a.marks), a.Generation)); err != nil {
				return err
			}
			return e.ended(c, b, o)
		case s.how == "mark-refused":
			why = fmt.Errorf("%s: %s was not applied: it was refused (%s), and operation %s then released the import without it.",
				c.Context.Where(), what, refusal(s.problem), o.Id)
		case s.how == "resume-failed":
			why = fmt.Errorf("%s: %s was not applied: its staged input could not be decrypted (resume-failed, %s), and operation %s then released the import without it.",
				c.Context.Where(), what, pr(or(&s.code, "no code")), o.Id)
		case s.how == "taken-over":
			why = fmt.Errorf("%s: %s was not applied: a takeover at generation %d resumed the import without it, and operation %s released it.",
				c.Context.Where(), what, s.gen, o.Id)
		case unshown:
			why = outcomeUnknown{fmt.Errorf("%s: %s: operation %s succeeded, but its last events were not shown, so whether the mark was applied before the release is unknown.",
				c.Context.Where(), what, o.Id)}
		default:
			why = fmt.Errorf("%s: %s was not applied: operation %s succeeded without pausing at the mark's generation %d.",
				c.Context.Where(), what, o.Id, a.Generation)
		}
		blocked := &client.Blocked{Err: fmt.Errorf("%w Read the draft before marking again:", why),
			More: []string{"  bronzewardctl draft show " + pr(client.ShellWord(b.Draft))}}
		return errors.Join(blocked, e.ended(c, b, o))
	}
	switch {
	case s.how == "paused":
		return e.say(append([]string{sf("Marked %s; re-staged and paused again (generation %d). Review again, continue or abandon it:", paths(a.marks), a.Generation)},
			remedies(b.Id, "review", "continue", "abandon")...)...)
	case s.how == "mark-refused":
		return &client.Blocked{Err: fmt.Errorf("%s: %s was not applied: it was refused (%s). The import is paused again at generation %d. "+
			"Review it again and mark again, continue it, or abandon it:", c.Context.Where(), what, refusal(s.problem), a.Generation),
			More: remedies(b.Id, "review", "continue", "abandon")}
	case s.how == "resume-failed":
		return resumeFailed(c, b, what, s.code)
	case s.how == "taken-over":
		return &client.Blocked{Err: fmt.Errorf("%s: %s was not applied: a takeover at generation %d resumed the import without it. Once the import pauses again, review it and mark again:",
			c.Context.Where(), what, s.gen), More: remedies(b.Id, "status")}
	}
	return lapsed(c, b, o, what)
}

// refusal names a mark-refused event's problem by its status, code, rule and the mark's position,
// never a path (compilation §3.6 item 4). A problem without a position, 503 or 500, is named by its
// code alone.
func refusal(p map[string]any) string {
	if p == nil {
		return "the event carries no problem"
	}
	s := bare(p["status"]) + " " + bare(strings.TrimPrefix(fmt.Sprint(p["type"]), "urn:bronzeward:problem:"))
	if r, ok := p["rule"]; ok {
		s += ", rule " + bare(r)
	}
	if n, ok := p["position"]; ok {
		s += ", the mark at position " + bare(n) + " (the first is 0)"
	}
	return s
}

// bare is a value the server chose, JSON-escaped so it holds no control character, and unquoted.
func bare(v any) string { return strings.Trim(compact(v), `"`) }

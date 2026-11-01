package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"

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
func (e *env) importReview(args []string) error {
	pos, err := e.parse("import review", args, 1, nil)
	if err != nil {
		return err
	}
	if err := protectedOn(runtime.GOOS); err != nil {
		return err
	}
	out, _ := e.out.(*os.File)
	switch {
	case e.output != "table":
		return usageError{errors.New("import review has no -o json or yaml form: it shows the review on a terminal only; nothing was read")}
	case e.noInput || e.tokenStdin:
		return usageError{errors.New("import review reads keys and marks from the terminal on standard input, so it takes neither --no-input nor --token-stdin; nothing was read")}
	case out == nil || !isTerminal(out) || !isTerminal(stdin):
		return usageError{errors.New("import review shows the review only when standard input and standard output are a terminal, with no override; nothing was read or written")}
	}
	ing := pos[0]
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
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
	if err := e.pager(out, reviewText(rv)); err != nil {
		return err
	}
	marks, err := e.readMarks(false)
	if err != nil {
		return err
	}
	if len(marks) == 0 {
		return e.say("No marks given. Review again, continue or abandon it:", "  bronzewardctl import review "+ing,
			"  bronzewardctl import continue "+ing, "  bronzewardctl import abandon "+ing)
	}
	return e.sendMarks(c, ing, marks, nil, false)
}

// pager shows text on the terminal out in the alternate screen, with standard input in raw mode
// for the keys, and restores both before it returns, so the review leaves the screen.
func (e *env) pager(out *os.File, text string) error {
	rows, cols, err := term.GetSize(int(out.Fd()))
	if err != nil {
		rows, cols = 24, 80
	}
	old, err := term.MakeRaw(int(stdin.Fd()))
	if err != nil {
		return fmt.Errorf("putting the terminal in raw mode for the review: %w; nothing was shown", err)
	}
	defer func() { _ = term.Restore(int(stdin.Fd()), old) }()
	if _, err := fmt.Fprint(out, altScreenOn); err != nil {
		return err
	}
	err = page(text, rows, cols, stdin, out)
	_, _ = fmt.Fprint(out, clearScreen+altScreenOff)
	return err
}

// reviewText is the review as the pager shows it: the staged document, then its declarations.
func reviewText(rv apitypes.IngestionReviewBody) string {
	var b strings.Builder
	b.WriteString(rv.Document)
	if !strings.HasSuffix(rv.Document, "\n") {
		b.WriteString("\n")
	}
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range rv.Declarations.References {
			if !yield(p) {
				return
			}
		}
	})
	fmt.Fprintf(&b, "\n# declared references: %d\n", len(paths))
	for _, p := range paths {
		ref := rv.Declarations.References[p]
		fmt.Fprintf(&b, "#   %s: %s version %d\n", p, ref.Kind, ref.Version)
	}
	fmt.Fprintf(&b, "# declared embedded documents: %d\n", len(rv.Declarations.Embedded))
	for _, d := range rv.Declarations.Embedded {
		fmt.Fprintf(&b, "#   %s: %s\n", d.Path, d.Format)
	}
	return b.String()
}

// importMark marks values of a paused import as secret by their paths (POST
// /ingestions/{id}/marks, author, human only), the paths read from the prompt or standard input,
// never from an argument or a file, and, unless --no-wait, follows the run the mark starts
// (client-contracts.md §11.3 item 3, #256 T15).
func (e *env) importMark(args []string) error {
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
	return e.sendMarks(c, pos[0], marks, rp, noWait)
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
			lines := []string{fmt.Sprintf("Sent %s for import %s (operation %s, generation %d).", paths(a.marks), a.Ingestion, a.Operation, a.Generation)}
			if noWait {
				lines = append(lines, "Follow it with: bronzewardctl import status "+a.Ingestion)
			}
			return lines, err
		})
	if err != nil || noWait {
		return err
	}
	if a.Generation == 0 {
		return fmt.Errorf("%s: the server accepted the mark without naming its generation; read the import with: bronzewardctl import status %s", c.Context.Where(), client.ShellWord(ing))
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
// operation that succeeded without one is not applied either.
func (e *env) markOutcome(c *client.Client, a accepted, b *apitypes.IngestionBody, o *apitypes.OperationBody, s settlement) error {
	what := "the mark on import " + b.Id
	again := "Review it again and mark again, continue it, or abandon it:\n  bronzewardctl import review " + b.Id +
		"\n  bronzewardctl import continue " + b.Id + "\n  bronzewardctl import abandon " + b.Id
	switch {
	case s.how == "paused":
		return e.say(fmt.Sprintf("Marked %s; re-staged and paused again (generation %d). Review again, continue or abandon it:", paths(a.marks), a.Generation),
			"  bronzewardctl import review "+b.Id, "  bronzewardctl import continue "+b.Id, "  bronzewardctl import abandon "+b.Id)
	case s.how == "mark-refused":
		return fmt.Errorf("%s: %s was not applied: it was refused (%s). The import is paused again at generation %d. %s",
			c.Context.Where(), what, refusal(s.problem), a.Generation, again)
	case s.how == "resume-failed":
		return resumeFailed(c, b, what, s.code)
	case s.how == "taken-over":
		return fmt.Errorf("%s: %s was not applied: a takeover at generation %d resumed the import without it. Once the import pauses again, review it and mark again:\n  bronzewardctl import status %s",
			c.Context.Where(), what, s.gen, b.Id)
	case client.Terminal(o.State) && o.State != "succeeded":
		return e.ended(c, b, o)
	case client.Terminal(o.State):
		return fmt.Errorf("%s: %s was not applied: operation %s succeeded without pausing at the mark's generation %d. Read the draft before marking again:\n  bronzewardctl draft show %s",
			c.Context.Where(), what, o.Id, a.Generation, client.ShellWord(b.Draft))
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

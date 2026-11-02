package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// maxDocument is the server's limit on an ingestion's document (IngestionInput.document).
const maxDocument = 512 << 10

// readDocument reads import start's --file document, from the path or from standard input for -,
// refused when it is not UTF-8 text or over the server's 512 KiB document limit.
func readDocument(file string) (string, error) {
	src := stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("--file: %w; nothing was sent", err)
		}
		defer f.Close()
		src = f
	}
	doc, err := readInput(src, "the document")
	switch {
	case err != nil:
		return "", fmt.Errorf("--file: %w; nothing was sent", err)
	case len(doc) > maxDocument:
		return "", errors.New("--file: the document is over the server's 512 KiB document limit; nothing was sent")
	}
	return string(doc), nil
}

// leaseMargin is how long after the lease it last read the client reads the ingestion again,
// for the difference between this host's clock and the server's (T15 of #256).
var leaseMargin = 10 * time.Second

// outcomeUnknown is a wait that ended with the operation still running and its outcome unknown:
// exit 3 (operator-experience.md §4).
type outcomeUnknown struct{ error }

// accepted is the 202 answer of an ingestion start, continuation or takeover.
type accepted struct {
	Ingestion string `json:"ingestion"`
	Operation string `json:"operation"`
}

// importCmd runs import start, status, continue, takeover and abandon (operator-experience.md §4,
// §5.3; decisions T11 and T15 of #256).
func (e *env) importCmd(args []string) error {
	v, rest, err := e.verb("import", args, "start", "status", "continue", "takeover", "abandon")
	if err != nil {
		return err
	}
	switch v {
	case "start":
		return e.importStart(rest)
	case "status":
		return e.importStatus(rest)
	case "abandon":
		return e.importAbandon(rest)
	}
	return e.importResume(v, rest)
}

// importStart starts an import of a machine's configuration into a draft (POST /ingestions,
// author, human only). The source is the machine, or with --file a document from a path or
// standard input; marks come as fragment set reads them (T12). Staging is encrypted unless
// --staging transient, which a review cannot use (internal/api/ingestions.go:121-124). It sends
// the draft's kept ETag or --if-match (client-contracts.md §8.3) and, unless --no-wait, waits
// until the ingestion pauses for review or ends.
func (e *env) importStart(args []string) error {
	var machine, draft, file string
	staging := "encrypted"
	var review, marks, noWait bool
	var rf repeatFlags
	if _, err := e.parse("import start", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&machine, "machine", "", "")
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&file, "file", "", "")
		fs.StringVar(&staging, "staging", staging, "")
		fs.BoolVar(&review, "review", false, "")
		fs.BoolVar(&marks, "marks", false, "")
		fs.BoolVar(&noWait, "no-wait", false, "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	switch {
	case machine == "" || draft == "":
		return usageError{errors.New("import start needs --machine and --draft")}
	case staging != "encrypted" && staging != "transient":
		return usageError{fmt.Errorf("--staging %q: give encrypted or transient", staging)}
	case review && staging != "encrypted":
		return usageError{errors.New("--review needs encrypted staging, which keeps the input until the review is continued; nothing was sent")}
	}
	if u := stdinUsers(file, "", e.tokenStdin); len(u) > 1 {
		return usageError{fmt.Errorf("%s read standard input, which only one of them can: give the token with --token-file", strings.Join(u, " and "))}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	if file != "" || marks {
		if err := protectedOn(runtime.GOOS); err != nil {
			return err
		}
	}
	in := apitypes.IngestionInput{Kind: "import", Machine: machine, Draft: draft, Source: "machine", Staging: apitypes.IngestionInputStaging(staging)}
	if review {
		in.Review = &review
	}
	var c *client.Client
	var a accepted
	w := &client.Write{Method: http.MethodPost, Path: "/ingestions", Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, HumanOnly: true,
		Check: "import status --machine " + client.ShellWord(machine), Read: ingestionsRead(machine, draft)}
	return e.held(func() error {
		err := e.writeAfter(func(cl *client.Client) error {
			c = cl
			if file != "" {
				doc, err := readDocument(file)
				if err != nil {
					return err
				}
				in.Source, in.Document = "document", &doc
			}
			if marks {
				m, err := e.readMarks(len(stdinUsers(file, "", false)) > 0)
				if err != nil {
					return err
				}
				in.Marks = &m
			}
			var err error
			if w.Body, err = json.Marshal(in); err == nil && len(w.Body) > maxBody {
				err = fmt.Errorf("the request body is %d bytes, %s; nothing was sent", len(w.Body), overBodyLimit)
			}
			return err
		}, w, func(_ *client.Client, r *client.Response) ([]string, error) {
			err := json.Unmarshal(r.Body, &a)
			lines := []string{sf("Import %s started (operation %s). Staging: %s. Review: %s.", a.Ingestion, a.Operation, staging,
				map[bool]string{true: "requested", false: "not requested"}[review])}
			if noWait {
				lines = append(lines, "Follow it with: bronzewardctl import status "+pr(a.Ingestion))
			}
			return lines, err
		})
		if err != nil || noWait {
			return err
		}
		return e.follow(c, a, 0)
	})
}

// importResume continues an import paused for review, or takes over a claim whose lease lapsed
// (POST /ingestions/{id}/continuations or /takeovers, author, human only), then, unless --no-wait,
// waits as start does, for the run at the claim's generation once the server accepted the request.
func (e *env) importResume(verb string, args []string) error {
	var noWait bool
	var rf repeatFlags
	pos, err := e.parse("import "+verb, args, 1, func(fs *flag.FlagSet) {
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
	ing := pos[0]
	var c *client.Client
	var a accepted
	done := map[string]string{"continue": "Continued", "takeover": "Took over"}[verb]
	return e.held(func() error {
		err := e.writeAfter(func(cl *client.Client) error {
			c = cl
			return nil
		}, &client.Write{Method: http.MethodPost, Path: "/ingestions/" + url.PathEscape(ing) + "/" + map[string]string{"continue": "continuations", "takeover": "takeovers"}[verb],
			Body: []byte("{}"), Repeat: rp, HumanOnly: true, Check: "import status " + client.ShellWord(ing), Read: ingestionRead(ing)},
			func(_ *client.Client, r *client.Response) ([]string, error) {
				err := json.Unmarshal(r.Body, &a)
				lines := []string{sf("%s import %s (operation %s).", done, a.Ingestion, a.Operation)}
				if noWait {
					lines = append(lines, "Follow it with: bronzewardctl import status "+pr(a.Ingestion))
				}
				return lines, err
			})
		if err != nil || noWait {
			return err
		}
		// The server takes the claim at its next generation in the transaction that accepts the
		// request, so the claim read after the answer shows this command's run, or one that took the
		// claim since. A read before the send could show a generation another client took next, and
		// a replayed answer started no run at all.
		return e.follow(c, a, latestRun)
	})
}

// held runs an import write and its wait with standard output held back, and prints it only once
// the exit is not 3: standard output stays empty when the outcome is unknown (#256's output rule).
func (e *env) held(run func() error) error {
	out, buf := e.out, &bytes.Buffer{}
	e.out = buf
	err := run()
	e.out = out
	var settled outcomeUnknown
	var unknown *client.Unknown
	if errors.As(err, &settled) || errors.As(err, &unknown) || buf.Len() == 0 {
		return err
	}
	_, werr := out.Write(buf.Bytes())
	return errors.Join(err, werr)
}

// latestRun, as follow's before, follows the run at the claim's owner generation when follow
// starts: the run the continuation or takeover started, or one that took the claim since.
const latestRun = -1

// importAbandon abandons an import (POST /ingestions/{id}/abandonments, author, human only): its
// staged input is cleared and its running operation fails ingestion-abandoned.
func (e *env) importAbandon(args []string) error {
	var rf repeatFlags
	pos, err := e.parse("import abandon", args, 1, rf.flags)
	if err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	return e.write(&client.Write{Method: http.MethodPost, Path: "/ingestions/" + url.PathEscape(pos[0]) + "/abandonments", Body: []byte("{}"),
		Repeat: rp, HumanOnly: true, Check: "import status " + client.ShellWord(pos[0]), Read: ingestionRead(pos[0])},
		func(_ *client.Client, r *client.Response) ([]string, error) {
			var b apitypes.IngestionBody
			err := json.Unmarshal(r.Body, &b)
			return []string{sf("Import %s abandoned: its staged input is cleared, and nothing of it reaches draft %s.", b.Id, b.Draft)}, err
		})
}

// importStatus shows an ingestion: its state, owner generation, lease, expiry and what its state
// allows (GET /ingestions/{id}, journeys §5.3). With no ingestion it lists them, --state paused or
// held finding the paused and lapsed ones (GET /ingestions).
func (e *env) importStatus(args []string) error {
	var l listing
	var state, machine string
	f := map[string]*string{"state": &state, "machine": &machine}
	pos, err := e.paged("import status", args, anyArgs, &l, filters(f))
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return list(e, l, "/ingestions", values(f), nil, "INGESTION\tSTATE\tLEASE\tMACHINE\tDRAFT\tOPERATION", "", func(w *tabwriter.Writer, b *apitypes.IngestionBody) {
			tf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", b.Id, b.State, lease(b), or(b.Machine, "-"), b.Draft, or(b.Operation, "-"))
		})
	}
	if len(pos) > 1 || state != "" || machine != "" || l != (listing{}) {
		return usageError{errors.New("import status takes one ingestion, or the list's --state, --machine and paging flags")}
	}
	return show(e, "/ingestions/"+url.PathEscape(pos[0]), func(w *tabwriter.Writer, b *apitypes.IngestionBody) {
		tf(w, "Import %s\tstate %s\t%s, %s staging\n", b.Id, b.State, b.Kind, b.Mode)
		tf(w, "Machine\t%s\tdraft %s, operation %s\n", or(b.Machine, "-"), b.Draft, or(b.Operation, "-"))
		tf(w, "Owner generation\t%d\treview %s\n", b.OwnerGeneration, or(b.Review, "not requested"))
		tf(w, "Lease\t%s\texpires %s\n", lease(b), when(b.ExpiresAt))
		for _, l := range allowed(b) {
			fmt.Fprintln(w, l)
		}
	})
}

// lease says whether a live claim's lease runs or lapsed; an ended or paused claim's is "-".
func lease(b *apitypes.IngestionBody) string {
	switch {
	case b.State != "held" && b.State != "resumed":
		return "-"
	case time.Now().After(b.LeaseUntil):
		return "lapsed " + ago(b.LeaseUntil)
	}
	return "until " + when(b.LeaseUntil)
}

// allowed is what an ingestion's state allows, as commands to run.
func allowed(b *apitypes.IngestionBody) []string {
	cmd := func(verb string) string { return "  bronzewardctl import " + verb + " " + pr(b.Id) }
	switch b.State {
	case "paused":
		return []string{"Paused for review. Continue it, or abandon it:", cmd("continue"), cmd("abandon")}
	case "held", "resumed":
		switch {
		case b.Mode != "encrypted" && time.Now().After(b.LeaseUntil):
			return []string{"Its run stopped renewing the lease. Transient staging cannot be taken over: abandon it and import again:", cmd("abandon"), restart(b)}
		case b.Mode != "encrypted":
			return []string{"Its run holds the claim. Transient staging cannot be taken over; it can be abandoned now:", cmd("abandon")}
		case time.Now().After(b.LeaseUntil):
			return []string{"Its run stopped renewing the lease. Take it over to resume, or abandon it:", cmd("takeover"), cmd("abandon")}
		}
		return []string{"Its run holds the claim. Once the lease lapses it can be taken over; it can be abandoned now:", cmd("abandon")}
	case "released":
		return []string{"Released into draft " + pr(b.Draft) + ". Nothing is left to do."}
	}
	return []string{"Ended: " + pr(b.State) + ". Import again:", restart(b)}
}

// restart is the command that imports the ingestion's machine into its draft again.
func restart(b *apitypes.IngestionBody) string {
	return "  bronzewardctl import start --machine " + client.ShellWord(or(b.Machine, "<machine>")) + " --draft " + client.ShellWord(b.Draft)
}

// follow waits for the import a started (T15, client-contracts.md §11.3), its events on standard
// error. It ends when the operation's state is terminal; when the run of this command, whose events
// carry a generation above before, pauses for review or records resume-failed; or when a read of
// the ingestion, made once the lease it last showed passed leaseMargin ago by this host's clock,
// shows the claim held or resumed with its lease passed (PROT-06). A claim read paused, released
// or abandoned while the operation runs settles nothing: the operation does.
func (e *env) follow(c *client.Client, a accepted, before int) (err error) {
	if a.Ingestion == "" || a.Operation == "" {
		return fmt.Errorf("%s: the server accepted the import without naming its ingestion and operation; find it with: bronzewardctl import status --state held", c.Context.Where())
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	b, err := readIngestion(c, a.Ingestion)
	if err != nil {
		return err
	}
	// Following the latest run, a mark's new generation starts it too.
	latest := before == latestRun
	if latest {
		before = b.OwnerGeneration - 1
	}
	gen, ours, settled, code := before, before == 0, "", ""
	next := b.LeaseUntil.Add(leaseMargin)
	// shown is set by an event and cleared by the Until that follows it: Until asked right after an
	// event can have more of the backlog behind it, so it reads no lease. A keep-alive, which the
	// server sends only after it sent everything it had, and a read of the operation can.
	shown := false
	o, err := c.Watch(a.Operation, client.Watch{
		Show: func(ev client.Event) {
			shown = true
			var d struct {
				Generation int    `json:"generation"`
				Code       string `json:"code"`
			}
			_ = json.Unmarshal(ev.Data, &d)
			note := ""
			if d.Generation != 0 {
				note = fmt.Sprintf(" (generation %d)", d.Generation)
			}
			if d.Code != "" {
				note += ": " + pr(d.Code)
			}
			tf(e.err, "  %s%s\n", ev.Type, note)
			switch {
			case (ev.Type == "continued" || ev.Type == "taken-over" || latest && ev.Type == "marked") && d.Generation > gen:
				ours, gen = true, d.Generation
			case !ours:
			case ev.Type == "paused" && d.Generation >= gen:
				settled, gen = "paused", d.Generation
			case ev.Type == "resume-failed":
				settled, code = "resume-failed", d.Code
			}
		},
		Clear: func() {
			fmt.Fprintln(e.err, "  (the events above no longer hold)")
			gen, ours, settled = before, before == 0, ""
		},
		Until: func() (bool, error) {
			after := shown
			shown = false
			if settled != "" || after || time.Now().Before(next) {
				return settled != "", nil
			}
			nb, err := readIngestion(c, a.Ingestion)
			if err != nil {
				return false, err
			}
			b, next = nb, later(nb.LeaseUntil, time.Now()).Add(leaseMargin)
			return (b.State == "held" || b.State == "resumed") && time.Now().After(b.LeaseUntil), nil
		},
	})
	if o == nil {
		return err
	}
	// A terminal operation decides the exit; a failed read of the events after it is reported beside.
	if err != nil {
		fmt.Fprintf(e.err, "bronzewardctl: %s; the operation's last events were not shown\n", client.Shown(err))
	}
	switch {
	case client.Terminal(o.State):
		return e.ended(c, b, o)
	case settled == "paused":
		return e.say(append([]string{sf("Paused for review (generation %d, expires %s).", gen, when(b.ExpiresAt)),
			"Continue it, or abandon it:"}, remedies(b.Id, "continue", "abandon")...)...)
	case settled == "resume-failed":
		return &client.Blocked{Err: fmt.Errorf("%s: import %s was not applied: its staged input could not be decrypted (resume-failed, %s). "+
			"The claim stays held until its lease lapses at %s; then take it over, or abandon it now:",
			c.Context.Where(), b.Id, or(&code, "no code"), when(b.LeaseUntil)), More: remedies(b.Id, "takeover", "abandon")}
	}
	act, verbs := "take it over, or abandon it", []string{"status", "takeover", "abandon"}
	if b.Mode != "encrypted" {
		act, verbs = "or abandon it (transient staging cannot be taken over)", []string{"status", "abandon"}
	}
	return &client.Blocked{Err: outcomeUnknown{fmt.Errorf("%s: import %s: its claim is %s and its lease lapsed %s, while operation %s still runs: the outcome is unknown. "+
		"Read it again later, %s:",
		c.Context.Where(), b.Id, b.State, ago(b.LeaseUntil), o.Id, act)}, More: remedies(b.Id, verbs...)}
}

// remedies are the lines `bronzewardctl import <verb> <id>`, one per verb, of an error's remedy.
func remedies(id string, verbs ...string) []string {
	lines := make([]string, len(verbs))
	for i, v := range verbs {
		lines[i] = "  bronzewardctl import " + v + " " + pr(id)
	}
	return lines
}

// ended reports an import operation's terminal state: on success the draft it was released into,
// whose ETag the client adopts only at the result's revision (ETAG-02, client-contracts.md §8.3);
// otherwise exit 4, a failure the draft caused offering a new start.
func (e *env) ended(c *client.Client, b *apitypes.IngestionBody, o *apitypes.OperationBody) error {
	if o.State != "succeeded" {
		var p struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(compact(o.Error)), &p)
		why := fmt.Sprintf("Import %s (operation %s) %s: %s.", b.Id, o.Id, o.State, compact(o.Error))
		var more []string
		switch strings.TrimPrefix(p.Type, "urn:bronzeward:problem:") {
		case "precondition-failed":
			why += " The draft changed after this import started, so the import was not applied and its claim is abandoned. Import again against the draft's current revision:"
			more = []string{restart(b)}
		case "ingestion-abandoned":
			why += " The ingestion was abandoned, so nothing of it reached the draft. Import again:"
			more = []string{restart(b)}
		}
		return &client.Blocked{Err: operationFailed{fmt.Errorf("%s: %s", c.Context.Where(), why)}, More: more}
	}
	var res apitypes.IngestResult
	var err error
	if o.Result != nil {
		res, err = o.Result.AsIngestResult()
	}
	if o.Result == nil || err != nil || res.Draft == "" {
		return fmt.Errorf("%s: import operation %s succeeded without naming its draft revision: %s", c.Context.Where(), o.Id, compact(o.Result))
	}
	if err := e.say(sf("Import %s released into draft %s (revision %d), import base %s.", b.Id, res.Draft, res.DraftRevision, res.ImportBaseRevision)); err != nil {
		return err
	}
	r, err := c.Get("/drafts/" + url.PathEscape(res.Draft))
	if err != nil {
		return err
	}
	var d apitypes.DraftBody
	if err := json.Unmarshal(r.Body, &d); err != nil {
		return fmt.Errorf("GET /api/v1/drafts/%s cannot be read: %w", url.PathEscape(res.Draft), err)
	}
	if d.Revision != res.DraftRevision {
		// ETAG-02: another write followed the import's, treated as a 412.
		return &client.Blocked{Err: fmt.Errorf("%s: draft %s is now revision %d: another write followed this import's revision %d. Its ETag is not kept. Review the change before writing again:",
			c.Context.Where(), res.Draft, d.Revision, res.DraftRevision),
			More: []string{"  bronzewardctl --context " + client.ShellWord(c.Context.Name) + " draft show " + client.ShellWord(res.Draft)}}
	}
	keep(e, c, res.Draft, r.Header.Get("ETag"))
	return nil
}

// say prints lines in the table output; -o json and yaml print only the answers.
func (e *env) say(lines ...string) error {
	if e.output != "table" {
		return nil
	}
	_, err := fmt.Fprintln(e.out, strings.Join(lines, "\n"))
	return err
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func readIngestion(c *client.Client, ing string) (*apitypes.IngestionBody, error) {
	r, err := c.Get("/ingestions/" + url.PathEscape(ing))
	if err != nil {
		return nil, err
	}
	var b apitypes.IngestionBody
	if err := json.Unmarshal(r.Body, &b); err != nil {
		return nil, fmt.Errorf("GET /api/v1/ingestions/%s cannot be read: %w", url.PathEscape(ing), err)
	}
	return &b, nil
}

// ingestionRead reads an ingestion after an unknown outcome and says what it shows.
func ingestionRead(ing string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		b, err := readIngestion(c, ing)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("GET /api/v1/ingestions/%s shows the state %s at owner generation %d", url.PathEscape(ing), b.State, b.OwnerGeneration), nil
	}
}

// ingestionsRead reads the machine's ingestions after an unknown start and names one into draft.
func ingestionsRead(machine, draft string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		return find(c, "/ingestions?"+url.Values{"machine": {machine}}.Encode(), "ingestion into draft "+draft, func(b apitypes.IngestionBody) string {
			return map[bool]string{true: "the ingestion " + b.Id + " (" + b.State + ") into draft " + draft}[b.Draft == draft]
		})
	}
}

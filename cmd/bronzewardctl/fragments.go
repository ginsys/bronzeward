package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// maxBody is the server's request body limit (internal/api/body.go maxBody, client-contracts.md
// §8.1): a larger body is refused before it is sent.
const maxBody = 1 << 20

// overBodyLimit is the refusal of an input that holds more than maxBody bytes. Every reader stops
// there before it reads further: the document, the declarations and the marks.
const overBodyLimit = "over the server's 1 MiB body limit"

// stdin is the process's standard input, which tests replace.
var stdin = os.Stdin

// fragmentSet puts a fragment in a draft (PUT /drafts/{id}/fragments/{name}). The document comes
// from --file, a path or - for standard input, and the declarations from --declarations; with
// --marks, marks come one per line from the prompt or standard input (decision T12 of #256), never
// from argv or a file the client reads. The guard of T13 ran in main before any of them is read.
// The write ingests, so a 409 saying the first request with the key is still ingesting is waited
// for, up to --ingest-wait or the server's claim lease, whichever is longer (client-contracts.md
// §8.4 item 2).
func (e *env) fragmentSet(args []string) error {
	var draft, layer, file, decl string
	var marks bool
	wait := 2 * time.Minute
	var rf repeatFlags
	pos, err := e.parse("fragment set", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&layer, "layer", "", "")
		fs.StringVar(&file, "file", "", "")
		fs.StringVar(&decl, "declarations", "", "")
		fs.BoolVar(&marks, "marks", false, "")
		fs.DurationVar(&wait, "ingest-wait", wait, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if draft == "" || layer == "" || file == "" {
		return usageError{errors.New("fragment set needs --draft, --layer and --file")}
	}
	if u := stdinUsers(file, decl, e.tokenStdin); len(u) > 1 {
		return usageError{fmt.Errorf("%s read standard input, which only one of them can: give the others from a file, the token with --token-file", strings.Join(u, " and "))}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	if err := protectedOn(runtime.GOOS); err != nil {
		return err
	}
	w := &client.Write{Method: http.MethodPut, Path: "/drafts/" + url.PathEscape(draft) + "/fragments/" + url.PathEscape(pos[0]),
		Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Ingests: true, Wait: wait, Check: "draft show " + client.ShellWord(draft),
		Read: entryRead(draft, apitypes.SourceEntryKindFragment, pos[0])}
	return e.writeAfter(func(*client.Client) (err error) {
		w.Body, err = e.fragmentBody(apitypes.FragmentInput{Layer: apitypes.FragmentInputLayer(layer)}, file, decl, marks)
		return err
	}, w, func(_ *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.SourceUpdate
		err := json.Unmarshal(r.Body, &x)
		return []string{sf("Fragment %s saved in draft %s (revision %s; draft revision %s).", pos[0], x.Draft, or(x.Entry.Revision, "none"),
			client.DraftRevision(r.Header.Get("ETag")))}, err
	})
}

// protectedOn refuses a protected command on a system other than Linux, where the guard is not yet
// verified (#256 BW-256-env-1: until #262).
func protectedOn(goos string) error {
	if goos != "linux" {
		return fmt.Errorf("this command reads protected input, which bronzewardctl accepts only on Linux so far; nothing was read or sent")
	}
	return nil
}

// stdinUsers names the inputs of fragment set that read standard input: --file - or a --file path
// that is standard input, a --declarations path that is, and --token-stdin. Marks read standard
// input only when none of them does.
func stdinUsers(file, decl string, token bool) []string {
	var u []string
	if file == "-" || isStdin(file) {
		u = append(u, "--file")
	}
	if decl != "" && isStdin(decl) {
		u = append(u, "--declarations")
	}
	if token {
		u = append(u, "--token-stdin")
	}
	return u
}

// isStdin reports whether path is standard input, by the file it names, not by the name: /dev/stdin
// or /dev/fd/0 reaches standard input as - does, and so does the file standard input comes from.
func isStdin(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	si, err := stdin.Stat()
	return err == nil && os.SameFile(fi, si)
}

// fragmentBody reads the inputs into in and returns the request body, refused when it is over the
// server's limit.
func (e *env) fragmentBody(in apitypes.FragmentInput, file, decl string, marks bool) ([]byte, error) {
	if decl != "" {
		d, err := declarations(decl)
		if err != nil {
			return nil, fmt.Errorf("--declarations %s: %w; nothing was sent", decl, err)
		}
		in.Declarations = d
	}
	src := stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, fmt.Errorf("--file: %w; nothing was sent", err)
		}
		defer f.Close()
		src = f
	}
	doc, err := readInput(src, "the document")
	if err != nil {
		return nil, fmt.Errorf("--file: %w; nothing was sent", err)
	}
	in.Document = string(doc)
	if marks {
		m, err := e.readMarks(len(stdinUsers(file, decl, false)) > 0)
		if err != nil {
			return nil, err
		}
		in.Marks = &m
	}
	body, err := json.Marshal(in)
	if err == nil && len(body) > maxBody {
		err = fmt.Errorf("the request body is %d bytes, %s; nothing was sent", len(body), overBodyLimit)
	}
	return body, err
}

// declarations reads a declarations file, YAML or JSON, with no member the API does not define.
// Its refusals are fixed text: the decoders' own errors quote member names, and some values, from
// the file, which stays out of the output. A second YAML document is refused: yaml.Unmarshal reads
// the first only, so a declaration in a later one would be dropped without a word (design §8).
func declarations(p string) (*apitypes.Declarations, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := readInput(f, "the declarations file")
	if err != nil {
		return nil, err
	}
	var v any
	yd := yaml.NewDecoder(bytes.NewReader(b))
	// An empty file is io.EOF here, and stays an empty declarations object, as before.
	if err := yd.Decode(&v); err != nil && !errors.Is(err, io.EOF) {
		return nil, errors.New("the file is not YAML or JSON")
	}
	var more any
	if err := yd.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file holds more than one YAML document")
	}
	j, err := json.Marshal(v)
	if err != nil {
		return nil, errors.New("the file has a member name that is not a string")
	}
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.DisallowUnknownFields()
	var d apitypes.Declarations
	unknown := errors.New("the file has a member the API does not define, or a value of the wrong type")
	if dec.Decode(&d) != nil {
		return nil, unknown
	}
	// encoding/json matches member names without regard to case, so a name in another case was
	// taken, and the same name in two cases kept one list and dropped the other without a word. The
	// declarations must encode back to the file's own members. The API takes null for references and
	// embedded, which encode back as no member, so a null under the exact name is left out of the
	// comparison.
	if v != nil {
		var got, want any
		back, err := json.Marshal(d)
		if err != nil || json.Unmarshal(back, &got) != nil || json.Unmarshal(j, &want) != nil {
			return nil, unknown
		}
		if m, ok := want.(map[string]any); ok {
			for _, k := range []string{"references", "embedded"} {
				if x, ok := m[k]; ok && x == nil {
					delete(m, k)
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			return nil, unknown
		}
	}
	return &d, nil
}

// readMarks reads marks, one per line until an empty line or the end of input (T12): from standard
// input when it is free and not a terminal, else from a prompt on the terminal, standard input
// when it is one and free, else the controlling terminal. With neither, or with --no-input and
// standard input taken or a terminal, it refuses. taken says another input read standard input,
// under any name (stdinUsers).
func (e *env) readMarks(taken bool) ([]string, error) {
	free := !taken && !e.tokenStdin
	var in io.Reader
	var prompt io.Writer
	switch {
	case free && !isTerminal(stdin):
		in = stdin
	case e.noInput:
	case free:
		in, prompt = stdin, e.err
	default:
		if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			defer tty.Close()
			in, prompt = tty, tty
		}
	}
	if in == nil {
		return nil, usageError{errors.New("--marks reads from a prompt on a terminal or from standard input, and neither is free here; nothing was sent")}
	}
	ask := func() {}
	if prompt != nil {
		fmt.Fprintln(prompt, "Mark a value as secret by its path, one per line; empty line to finish:")
		ask = func() { fmt.Fprint(prompt, "mark> ") }
	}
	return scanMarks(in, prompt != nil, ask)
}

// terminalLine is the most a Linux terminal in canonical mode keeps of one line: it drops the rest
// without an error, so a line this long from a terminal may have been cut short.
const terminalLine = 4095

// readInput reads an input the CLI takes whole from f, what naming it in a refusal. It refuses more
// than the body limit, text that is not UTF-8, and a terminal line that may have been cut short.
// The check is on the file opened, not its name: /dev/stdin or /dev/tty is a terminal too. The
// refusals quote nothing from the input. The document and the declarations are read here, and
// marks a line at a time by scanMarks, with the same terminal limit. No other input is read.
func readInput(f *os.File, what string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, maxBody+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", what, err)
	case len(b) > maxBody:
		return nil, fmt.Errorf("%s is %s", what, overBodyLimit)
	case !utf8.Valid(b):
		return nil, fmt.Errorf("%s is not UTF-8 text", what)
	}
	if err := terminalLines(b, isTerminal(f), what); err != nil {
		return nil, err
	}
	return b, nil
}

// terminalLines refuses an input read from a terminal with a line long enough that canonical mode
// may have cut it short. The refusal names the line number only.
func terminalLines(b []byte, terminal bool, what string) error {
	if !terminal {
		return nil
	}
	for n, line := range bytes.Split(b, []byte("\n")) {
		if len(line) >= terminalLine {
			return fmt.Errorf("line %d of %s is %d bytes or more, which a terminal can cut short; give it from a file or a pipe, not a terminal",
				n+1, what, terminalLine)
		}
	}
	return nil
}

// maxMarks is the most marks one request takes (MarkInput's maxItems in api/openapi.yaml).
const maxMarks = 1024

// scanMarks reads marks, one per line until an empty line or the end of input. A mark is sent as
// the text that was given (PA §9.3), so a mark that is not UTF-8, which json.Marshal would alter,
// or a terminal line that may have been cut short, or more than maxMarks marks, is refused. A
// refusal names the mark's position, never its text. ask, when not nil, prompts for each line.
func scanMarks(in io.Reader, terminal bool, ask func()) ([]string, error) {
	var marks []string
	s := bufio.NewScanner(in)
	// The API sets no length on one mark, so a line may be as long as the body limit allows.
	s.Buffer(nil, maxBody)
	s.Split(markLines)
	dropped := 0
	if terminal {
		s.Split(terminalMarkLines(&dropped))
	}
	if ask == nil {
		ask = func() {}
	}
	total := 0
	for ask(); s.Scan(); ask() {
		// All the marks together are held to the body limit too, so a long stream of lines is
		// refused before it is read whole.
		// A line feed dropped from a split CRLF was read too, so it counts.
		if total += len(s.Bytes()) + dropped; total > maxBody {
			return nil, fmt.Errorf("the marks are %s; nothing was sent", overBodyLimit)
		}
		dropped = 0
		// The length check counts the line as the terminal gave it, its carriage return included,
		// whether the line feed after it came in the same read or not.
		raw, ended := strings.CutSuffix(s.Text(), "\n")
		m := raw
		if ended {
			m = strings.TrimSuffix(raw, "\r")
		} else if r, cr := strings.CutSuffix(raw, "\r"); terminal && cr {
			// A terminal line ends at a carriage return too (terminalMarkLines).
			m = r
		}
		if m == "" {
			break
		}
		switch {
		case len(marks) == maxMarks:
			return nil, fmt.Errorf("more than %d marks were given, the most one request takes; nothing was sent", maxMarks)
		case !utf8.ValidString(m):
			return nil, fmt.Errorf("mark %d is not UTF-8 text; nothing was sent", len(marks)+1)
		case terminal && len(raw) >= terminalLine:
			return nil, fmt.Errorf("mark %d is %d bytes or more, which a terminal can cut short; give long marks on standard input; nothing was sent",
				len(marks)+1, terminalLine)
		}
		marks = append(marks, m)
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("reading marks: %w; nothing was sent", err)
	}
	return marks, nil
}

// terminalMarkLines is markLines for a terminal, where a line also ends at a carriage return: a key
// typed while the pager had the terminal in raw mode is queued untranslated, so Enter arrived as
// a carriage return and not as the line feed the line mode makes of it. A carriage return that
// goes on to a line feed (a CRLF) ends the line with both, as in markLines. A carriage return that
// ends what has been read ends its line at once, so that one typed Enter does not wait for a second
// key; if the next read then starts with a line feed, it is the other half of that CRLF and is
// dropped, and the rest of what was read is split in the same call, so that a mark already read
// is not held back for another key. The split function holds that one fact between calls, and adds
// each line feed it drops to *dropped, so that the caller counts every byte that was read.
func terminalMarkLines(dropped *int) bufio.SplitFunc {
	dropLF := false
	return func(data []byte, atEOF bool) (int, []byte, error) {
		skip := 0
		if dropLF && len(data) > 0 {
			dropLF = false
			if data[0] == '\n' {
				skip = 1
				*dropped++
			}
		}
		rest := data[skip:]
		i := bytes.IndexAny(rest, "\r\n")
		switch {
		case i < 0 && atEOF && len(rest) > 0:
			return len(data), rest, nil
		case i < 0:
			return skip, nil, nil
		case rest[i] == '\r' && i+1 < len(rest) && rest[i+1] == '\n':
			return skip + i + 2, rest[:i+2], nil
		case rest[i] == '\r' && i+1 == len(rest):
			dropLF = true
		}
		return skip + i + 1, rest[:i+1], nil
	}
}

// markLines splits marks at a line feed and keeps the line feed, so that scanMarks drops only the
// carriage return of a CRLF end. Any other carriage return belongs to the mark, as a key may end in
// one: bufio.ScanLines also drops one at the end of input.
func markLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

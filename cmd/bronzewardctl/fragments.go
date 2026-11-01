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
	"runtime"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// maxBody is the server's request body limit (internal/api/body.go maxBody, client-contracts.md
// §8.1): a larger body is refused before it is sent.
const maxBody = 1 << 20

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
	if file == "-" && e.tokenStdin {
		return usageError{errors.New("--file - and --token-stdin both read standard input: give the token with --token-file")}
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
		return []string{fmt.Sprintf("Fragment %s saved in draft %s (revision %s; draft revision %s).", pos[0], x.Draft, or(x.Entry.Revision, "none"),
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
	var src io.Reader = stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, fmt.Errorf("--file: %w; nothing was sent", err)
		}
		defer f.Close()
		src = f
	}
	doc, err := io.ReadAll(io.LimitReader(src, maxBody+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("--file: reading the document: %w; nothing was sent", err)
	case len(doc) > maxBody:
		return nil, fmt.Errorf("the document is over the server's 1 MiB body limit; nothing was sent")
	case !utf8.Valid(doc):
		return nil, fmt.Errorf("the document is not UTF-8 text; nothing was sent")
	}
	in.Document = string(doc)
	if marks {
		m, err := e.readMarks(file == "-")
		if err != nil {
			return nil, err
		}
		in.Marks = &m
	}
	body, err := json.Marshal(in)
	if err == nil && len(body) > maxBody {
		err = fmt.Errorf("the request body is %d bytes, over the server's 1 MiB limit; nothing was sent", len(body))
	}
	return body, err
}

// declarations reads a declarations file, YAML or JSON, with no member the API does not define.
// Its refusals are fixed text: the decoders' own errors quote member names, and some values, from
// the file, which stays out of the output.
func declarations(p string) (*apitypes.Declarations, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var v any
	if yaml.Unmarshal(b, &v) != nil {
		return nil, errors.New("the file is not YAML or JSON")
	}
	j, err := json.Marshal(v)
	if err != nil {
		return nil, errors.New("the file has a member name that is not a string")
	}
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.DisallowUnknownFields()
	var d apitypes.Declarations
	if dec.Decode(&d) != nil {
		return nil, errors.New("the file has a member the API does not define, or a value of the wrong type")
	}
	return &d, nil
}

// readMarks reads marks, one per line until an empty line or the end of input (T12): from standard
// input when it is free and not a terminal, else from a prompt on the terminal, standard input
// when it is one and free, else the controlling terminal. With neither, or with --no-input and
// standard input taken or a terminal, it refuses.
func (e *env) readMarks(docOnStdin bool) ([]string, error) {
	free := !docOnStdin && !e.tokenStdin
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
	if prompt != nil {
		fmt.Fprintln(prompt, "Marks, one path per line; an empty line ends them:")
	}
	var marks []string
	s := bufio.NewScanner(in)
	// The API sets no length on one mark, so a line may be as long as the body limit allows.
	s.Buffer(nil, maxBody)
	for s.Scan() {
		// ScanLines already drops the carriage return of a CRLF end; one more belongs to the mark.
		m := s.Text()
		if m == "" {
			break
		}
		marks = append(marks, m)
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("reading marks: %w; nothing was sent", err)
	}
	return marks, nil
}

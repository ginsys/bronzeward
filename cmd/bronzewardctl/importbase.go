package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// importBaseCmd is the import-base noun: the one verb, document.
func (e *env) importBaseCmd(args []string) error {
	_, rest, err := e.verb("import-base", args, "document")
	if err != nil {
		return err
	}
	return e.importBaseDocument(rest)
}

// importBaseDocument shows an import base's stored document (GET
// /import-base-revisions/{id}/document, author, human only; the server answers any author for an
// import read from a node, and for a file import only the author who made it and continued its
// review) the way import review shows a review
// (client-contracts.md §11.3): only when standard input and output are a terminal, through the
// in-process pager, after the protected-review notice, with no -o json, yaml, save or export form,
// no override and no marks prompt. A refusal of the server is shown as it comes and prints no body.
// The text is held in memory only.
func (e *env) importBaseDocument(args []string) (err error) {
	pos, err := e.parse("import-base document", args, 1, nil)
	if err != nil {
		return err
	}
	if err := protectedOn(runtime.GOOS); err != nil {
		return err
	}
	out := e.rawFile()
	switch {
	case e.output != "table":
		return usageError{errors.New("import-base document has no -o json or yaml form: it shows the document on a terminal only; nothing was read")}
	case e.noInput || e.tokenStdin:
		return usageError{errors.New("import-base document reads keys from the terminal on standard input, so it takes neither --no-input nor --token-stdin; nothing was read")}
	case out == nil || !isTerminal(out) || !isTerminal(stdin):
		return usageError{errors.New("import-base document shows the document only when standard input and standard output are a terminal, with no override; nothing was read or written")}
	}
	rows, cols, err := screenSize(out, "import-base document")
	if err != nil {
		return err
	}
	ibr := pos[0]
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	// GET /server checks the server's revision on the context's use (§7 item 3) before the document
	// is read.
	if _, _, err := c.Server(); err != nil {
		return err
	}
	r, err := c.Get("/import-base-revisions/" + url.PathEscape(ibr) + "/document")
	if err != nil {
		return err
	}
	var d apitypes.ImportBaseDocument
	err = json.Unmarshal(r.Body, &d)
	clear(r.Body)
	if err != nil {
		return fmt.Errorf("GET /api/v1/import-base-revisions/%s/document cannot be read: %w", url.PathEscape(ibr), err)
	}
	if _, err := fmt.Fprintln(out, reviewNotice); err != nil {
		return outputFailed{err} // the raw file is beneath the table guard, which would have tagged it
	}
	return e.pager(out, strings.Split(strings.TrimSuffix(d.Document, "\n"), "\n"), rows, cols)
}

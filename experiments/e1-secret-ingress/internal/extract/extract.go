// Package extract is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It is the step design §7.1 requires to come first: every known or marked secret is moved to a
// provider and replaced by a reference, and only then does anything sanitized exist to persist.
//
// The ordering is carried by a type rather than by the order of statements. Every persistence
// function takes a secret.Sanitized, and Run is how an ingested document becomes one; the only
// other constructor call is staging's resume path, which rebuilds one from bytes that were
// already sanitized when they were staged. So there is no arrangement of calls in which a draft is
// written before this function returns.
//
// The restriction is not the compiler's. NewSanitized is exported, since Go cannot scope it to one
// sibling package, and secret's TestNewSanitizedHasNoUnexpectedCallers is what fails if anything
// else calls it. A reviewer does not have to read the ingestion flow to believe the ordering; they
// have to believe that test, which is a lexical scan of the module.
//
// Run fails closed. If a provider write fails halfway, it returns an error and no Sanitized, so
// the caller has nothing it could persist. If the substituted document still contains a secret it
// was supposed to have removed, it returns an error too — that check is cheap and catches the one
// class of bug this design cannot otherwise see, where the reference is written somewhere other
// than where the secret was.
package extract

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

// refPrefix marks a substituted value. It is deliberately not valid as any of the things it
// replaces — not a certificate, not a token, not base64 — so a reference that reached a consumer
// expecting a real value fails there rather than being used.
const refPrefix = "bw:ref:"

// Store is where extracted secrets go. The experiment's implementation talks to OpenBao; the tests
// use an in-memory one. Nothing here selects a provider for v1.
type Store interface {
	// Name identifies the provider in the journal and the report.
	Name() string
	// Put stores value under key and returns the URI that now addresses it.
	Put(ctx context.Context, key string, value []byte) (uri string, err error)
}

// Request is one extraction.
type Request struct {
	// Document is modified in place: each marked scalar is replaced by its reference.
	Document *document.Document
	// Paths are the locations to extract, from schema detection, operator marks, or both.
	Paths []string
	// Store is where the values go.
	Store Store
	// Journal records each extraction. It is required: an extraction nobody recorded cannot be
	// shown to have happened before anything else, which is the entire claim under test.
	Journal *journal.Journal
	// Control is consulted at the extraction boundary. It may be nil on the honest path.
	Control *checkpoint.Control
	// RunID namespaces the keys in the store so runs do not overwrite each other's evidence.
	RunID string
}

// Result is what a successful extraction produces.
type Result struct {
	// Sanitized is the document with every marked secret replaced. It is the only thing the caller
	// may persist.
	Sanitized secret.Sanitized
	// Digests are every extracted secret's full SHA-256, sorted by the path it came from. The
	// caller attaches these to each journal write record, so that verification can assert the
	// write came after all of them.
	Digests []string
}

// Run extracts every path, substitutes a reference for each, and returns the sanitized document.
func Run(ctx context.Context, req Request) (Result, error) {
	switch {
	case req.Document == nil:
		return Result{}, errors.New("extract: no document")
	case req.Store == nil:
		return Result{}, errors.New("extract: no store")
	case req.Journal == nil:
		return Result{}, errors.New("extract: no journal; an unrecorded extraction cannot be shown to have happened first")
	case req.RunID == "":
		return Result{}, errors.New("extract: no run id")
	case len(req.Paths) == 0:
		// A run that extracts nothing produces a document identical to its input and a clean leak
		// scan, which is indistinguishable from a successful extraction unless it is refused here.
		return Result{}, errors.New("extract: nothing to extract; this would produce a clean run that demonstrates nothing")
	}

	// plaintexts is kept so the substituted document can be checked against every value that was
	// supposed to leave it. It is the only place in this program outside a deliberate control that
	// holds more than one secret at once, and it never leaves this function.
	plaintexts := make([]secret.Unresolved, 0, len(req.Paths))
	refs := make([]secret.Reference, 0, len(req.Paths))
	digests := make([]string, 0, len(req.Paths))

	// Extracted in path order, whatever order the caller supplied. The documented order of
	// Result.Digests — and so of every journal record and provider write — was only true because
	// the one caller happened to sort first; a caller assembling paths from a map would have made
	// two runs over the same document disagree, which is exactly the comparison the journal exists
	// for.
	paths := append([]string(nil), req.Paths...)
	sort.Strings(paths)
	for _, path := range paths {
		value, ok := req.Document.Get(path)
		if !ok {
			return Result{}, fmt.Errorf("extract: no scalar at %q", path)
		}
		if strings.HasPrefix(value, refPrefix) {
			// Already a reference. Extracting it again would store the reference as if it were a
			// secret and leave the document unchanged, while the journal recorded a substitution.
			return Result{}, fmt.Errorf("extract: %q already holds a reference; this document has been extracted before", path)
		}

		u := secret.NewUnresolved([]byte(value))
		key := req.RunID + "/" + path

		uri, err := req.Store.Put(ctx, key, u.Unsafe())
		if err != nil {
			return Result{}, fmt.Errorf("extract: storing %q: %w", path, err)
		}

		if _, err := req.Journal.Append(journal.Record{
			Event:      journal.EventExtracted,
			Checkpoint: checkpoint.AfterExtract.String(),
			Surface:    req.Store.Name(),
			Digests:    []string{u.Digest()},
			Detail:     fmt.Sprintf("%s -> %s (%s)", path, uri, u),
		}); err != nil {
			// The value is in the provider but unrecorded. Continuing would produce a run whose
			// journal cannot establish that this secret was extracted before the draft was
			// written, so the only honest outcome is to stop.
			return Result{}, fmt.Errorf("extract: recording the extraction of %q: %w", path, err)
		}

		if err := req.Document.Replace(path, refPrefix+uri); err != nil {
			return Result{}, fmt.Errorf("extract: substituting at %q: %w", path, err)
		}

		plaintexts = append(plaintexts, u)
		refs = append(refs, secret.Reference{Path: path, URI: uri, Digest: u.Digest()})
		digests = append(digests, u.Digest())
	}

	body, err := req.Document.Bytes()
	if err != nil {
		return Result{}, fmt.Errorf("extract: re-encoding the document: %w", err)
	}

	if err := assertNoPlaintextRemains(body, refs, plaintexts); err != nil {
		return Result{}, err
	}

	// The boundary is crossed only once the document is provably sanitized, so a --crash-at or
	// --hold-at here captures a disk on which extraction is complete and nothing has been
	// persisted. That is the window §7.1's requirement is actually about.
	if req.Control != nil {
		req.Control.Reach(checkpoint.AfterExtract)
	}

	return Result{Sanitized: secret.NewSanitized(body, refs), Digests: digests}, nil
}

// assertNoPlaintextRemains checks the substituted document against every value that was extracted.
//
// It is not a leak scan and it does not replace one: it compares against exactly the values this
// run removed, so it cannot see a secret nobody marked. What it does catch is the substitution
// going to the wrong place — a reference written at one path while the secret stays at another —
// which no other check in this experiment would notice, because the journal would record the
// extraction and the provider would hold the value.
func assertNoPlaintextRemains(body []byte, refs []secret.Reference, plaintexts []secret.Unresolved) error {
	text := string(body)
	var left []string
	for i, u := range plaintexts {
		// An empty original cannot be searched for: every document contains the empty string.
		// Its substitution is checked by the reference's presence instead.
		if len(u.Unsafe()) == 0 {
			continue
		}
		if strings.Contains(text, string(u.Unsafe())) {
			left = append(left, refs[i].Path)
		}
	}
	// The text search above sees only values the encoder writes out verbatim. A multi-line secret
	// becomes an indented block, and one with characters YAML must escape becomes a quoted string
	// with those escapes, so neither appears in the text as it was extracted and the search passes
	// with the value still there. The re-encoded document is therefore parsed back and every scalar
	// compared as a value, which is the form the secret was read in.
	if len(left) == 0 {
		reparsed, err := document.Load(body)
		if err != nil {
			return fmt.Errorf("extract: the sanitized document does not parse back: %w", err)
		}
		for _, path := range reparsed.Paths() {
			value, _ := reparsed.Get(path)
			for i, u := range plaintexts {
				if len(u.Unsafe()) > 0 && value == string(u.Unsafe()) {
					left = append(left, refs[i].Path+" (as the value at "+path+")")
				}
			}
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("extract: the document still holds the value extracted from %s; "+
			"refusing to return anything persistable", strings.Join(left, ", "))
	}

	for _, ref := range refs {
		if !strings.Contains(text, refPrefix+ref.URI) {
			return fmt.Errorf("extract: the reference for %s is not in the document; "+
				"the substitution did not land where the secret was", ref.Path)
		}
	}
	return nil
}

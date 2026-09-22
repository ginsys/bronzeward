// Package journal is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// A leak scan can say that no copy of a secret was on disk at the instant it ran. It cannot say
// that extraction happened before persistence, which is what design §7.1 actually requires. The
// journal is what carries that claim: every extraction and every write appends a record, and
// Verify asserts mechanically that each write of a payload which could have contained a secret is
// preceded by that secret's extraction.
//
// Three properties make the journal usable as evidence rather than as narration:
//
//   - It holds digests, never values. A journal that leaked would defeat the experiment it is
//     evidence for, so the only secret-derived thing it records is a SHA-256.
//   - Every record is fsynced before the call returns. A record written but not flushed would be
//     lost by the very SIGKILL the crash controls exist to deliver, and a crashed run's bundle
//     would then be uninterpretable.
//   - It is append-only and sequence-numbered, so a gap is visible. A missing seq means a record
//     was lost, which is a reason to discard the bundle rather than to read around it.
//
// The journal is written by the prototype, so it is not independent evidence on its own. The
// experiment pairs it with three observers that are not the prototype: PostgreSQL's server-side
// clock and WAL position, the secret provider's own per-version timestamps, and the injection log
// written by a third process.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Event names. Verify understands the first two; the rest are context for a human reading the
// file, and are deliberately not load-bearing.
const (
	// EventExtracted records that one secret reached the provider and a reference replaced it.
	// Digests holds exactly that secret's digest; Detail holds the reference it became.
	EventExtracted = "secret.extracted"
	// EventWrite records bytes about to be persisted. PayloadSHA256 is the digest of those exact
	// bytes, and Digests lists every secret present in the source document, whether or not this
	// payload was expected to contain it. Listing them all is what makes Verify an assertion
	// rather than a restatement: the prototype does not get to decide which secrets were at risk.
	EventWrite = "payload.write"
	// EventCheckpoint records crossing a boundary.
	EventCheckpoint = "checkpoint.reached"
	// EventNote is free-form context.
	EventNote = "note"
)

// Record is one journal line. It is JSON on disk and a row in ingest_journal, with the same field
// names in both so that the two can be diffed without a mapping table.
type Record struct {
	// Seq is assigned by Append, starting at 1. A gap means a lost record.
	Seq int `json:"seq"`
	// RunID identifies the run, and is the directory name of its evidence.
	RunID string `json:"run_id"`
	// Event is one of the constants above.
	Event string `json:"event"`
	// Checkpoint is the boundary the flow was at, in its external spelling.
	Checkpoint string `json:"checkpoint,omitempty"`
	// Surface names where the bytes were going: a file path under the run root, a table name, a
	// log, the provider. It is what a leak-scan hit is matched back against.
	Surface string `json:"surface,omitempty"`
	// PayloadSHA256 is the digest of the exact bytes written, for EventWrite.
	PayloadSHA256 string `json:"payload_sha256,omitempty"`
	// Digests are the full SHA-256 digests of the secrets this record concerns.
	Digests []string `json:"secret_digests,omitempty"`
	// Detail is human-readable context. It must never hold a secret value; the package cannot
	// enforce that, so callers pass a secret.Unresolved's rendering rather than its plaintext.
	Detail string `json:"detail,omitempty"`
	// Wall is the host clock, comparable against the fixture's injection log and the containers'
	// logs, all of which share this host's clock.
	Wall time.Time `json:"wall"`
	// MonoNanos is nanoseconds since the journal was opened, from a monotonic source. It is what
	// orders records when the wall clock steps, which NTP can make it do mid-run.
	MonoNanos int64 `json:"mono_ns"`
}

// Journal is an append-only, fsynced record of one run.
type Journal struct {
	mu      sync.Mutex
	file    *os.File
	runID   string
	seq     int
	opened  time.Time
	records []Record
}

// Open creates or appends to the journal at path and fsyncs the directory entry, so that the file
// itself survives a SIGKILL delivered immediately afterwards.
func Open(path, runID string) (*Journal, error) {
	if runID == "" {
		return nil, errors.New("journal: a run id is required; an unidentified journal cannot be matched to a bundle")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("journal: creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: opening %s: %w", path, err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		f.Close()
		return nil, err
	}
	return &Journal{file: f, runID: runID, opened: time.Now()}, nil
}

// syncDir flushes a directory entry. Without it, a file created and fsynced can still be absent
// after a power loss or, here, be absent from a container image captured mid-run.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("journal: opening %s to sync it: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("journal: syncing %s: %w", dir, err)
	}
	return nil
}

// Append assigns the sequence number and clocks, writes the record and fsyncs it. The record as
// stored is returned, so a caller can log the seq it was given.
//
// It fsyncs on every call, which is slow and deliberate: the crash controls deliver SIGKILL, and
// anything still in a buffer at that moment is gone.
func (j *Journal) Append(r Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.seq++
	r.Seq = j.seq
	r.RunID = j.runID
	r.Wall = time.Now()
	r.MonoNanos = int64(time.Since(j.opened))

	line, err := json.Marshal(r)
	if err != nil {
		return Record{}, fmt.Errorf("journal: encoding record %d: %w", r.Seq, err)
	}
	if _, err := j.file.Write(append(line, '\n')); err != nil {
		return Record{}, fmt.Errorf("journal: writing record %d: %w", r.Seq, err)
	}
	if err := j.file.Sync(); err != nil {
		return Record{}, fmt.Errorf("journal: syncing record %d: %w", r.Seq, err)
	}

	j.records = append(j.records, r)
	return r, nil
}

// Records returns a copy of what has been appended in this process.
func (j *Journal) Records() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, len(j.records))
	copy(out, j.records)
	return out
}

// Close flushes and closes the file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}

// Load reads a journal from disk, which is how a crashed run's journal is verified: the process
// that wrote it is gone.
//
// A truncated final line is reported rather than skipped. SIGKILL between the write and the fsync
// cannot produce one here, but a full disk can, and silently dropping the last record would hide
// exactly the record a crash run is about.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("journal: opening %s: %w", path, err)
	}
	defer f.Close()

	var out []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("journal: %s line %d is not a record: %w", path, line, err)
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("journal: reading %s: %w", path, err)
	}
	return out, nil
}

// Violation is one way a set of records fails §7.1's ordering requirement.
type Violation struct {
	// Seq is the record at fault.
	Seq int
	// Reason states what is wrong, in a form that can go straight into the report.
	Reason string
}

func (v Violation) String() string { return fmt.Sprintf("seq %d: %s", v.Seq, v.Reason) }

// Verify asserts the property the whole experiment is built to test: every write of a payload that
// could have contained a secret happens after that secret was extracted.
//
// It also checks the journal's own integrity first. A journal with a gap, a duplicate or a
// backwards clock cannot support a claim about ordering, so those are violations in their own
// right rather than warnings: an incomplete instrument must not return "no violations".
//
// A nil result means every write in these records was preceded by the extraction of every secret
// it lists. It does not mean no secret leaked — only that this record of the run is consistent
// with §7.1. The leak scan and the calibrated controls answer the other half.
func Verify(records []Record) []Violation {
	var violations []Violation

	ordered := make([]Record, len(records))
	copy(ordered, records)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Seq < ordered[j].Seq })

	// Integrity: sequence numbers must run 1..n with no gap and no repeat.
	for i, r := range ordered {
		if want := i + 1; r.Seq != want {
			violations = append(violations, Violation{
				Seq:    r.Seq,
				Reason: fmt.Sprintf("expected sequence %d here; the journal is missing records or repeats one, so it cannot establish an order", want),
			})
			break
		}
	}

	// Integrity: the monotonic clock must not run backwards within one run.
	for i := 1; i < len(ordered); i++ {
		if ordered[i].MonoNanos < ordered[i-1].MonoNanos {
			violations = append(violations, Violation{
				Seq:    ordered[i].Seq,
				Reason: fmt.Sprintf("monotonic clock went backwards (%dns after %dns)", ordered[i].MonoNanos, ordered[i-1].MonoNanos),
			})
		}
	}

	// The ordering requirement itself.
	extracted := map[string]int{}
	for _, r := range ordered {
		switch r.Event {
		case EventExtracted:
			for _, d := range r.Digests {
				if _, seen := extracted[d]; !seen {
					extracted[d] = r.Seq
				}
			}
		case EventWrite:
			for _, d := range r.Digests {
				at, seen := extracted[d]
				switch {
				case !seen:
					violations = append(violations, Violation{
						Seq: r.Seq,
						Reason: fmt.Sprintf("wrote %s while secret %s had not been extracted; §7.1 requires extraction before any ordinary plaintext persistence",
							surfaceOf(r), short(d)),
					})
				case at >= r.Seq:
					violations = append(violations, Violation{
						Seq: r.Seq,
						Reason: fmt.Sprintf("wrote %s at sequence %d but secret %s was extracted at sequence %d",
							surfaceOf(r), r.Seq, short(d), at),
					})
				}
			}
		}
	}

	return violations
}

// surfaceOf names the destination of a write for an error message, falling back to the payload
// digest when no surface was recorded, so a violation is never reported without a handle on it.
func surfaceOf(r Record) string {
	if r.Surface != "" {
		return r.Surface
	}
	if r.PayloadSHA256 != "" {
		return "a payload with digest " + short(r.PayloadSHA256)
	}
	return "an unnamed surface"
}

// short abbreviates a digest for a message. The full value stays in the record.
func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

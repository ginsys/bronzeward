package api

import (
	"database/sql"
	"strings"
	"testing"
)

const hostnameDoc = "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: node-a\n"

// nameMachine gives ie's machine the stored name step 2a compares (compilation.md §2.3 step 2a).
func (ie *ingestEnv) nameMachine(t *testing.T, name string) {
	t.Helper()
	mustExec(t, ie.db, `UPDATE machine SET name = $1 WHERE id = $2`, name, ie.machine)
}

// filledRecord reads the one import base revision: its stored document and its hostname record.
func (ie *ingestEnv) filledRecord(t *testing.T) (doc string, name sql.NullString, equal sql.NullBool) {
	t.Helper()
	if err := ie.db.QueryRow(`SELECT document, filled_hostname, filled_hostname_equal FROM import_base_revision`).
		Scan(&doc, &name, &equal); err != nil {
		t.Fatal(err)
	}
	return doc, name, equal
}

// Compilation §2.3 step 2a, persistence-api §4.2: an import whose HostnameConfig equals the machine's
// name stores a base without that document and records the name and that the document was the fill's
// own, so a later compile can tell the machine is unchanged in form.
func TestImportRemovesTheFilledHostname(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.nameMachine(t, "node-a")
	op, j := ie.startJob(t, map[string]any{"document": "machine:\n  token: " + runToken + "\n---\n" + hostnameDoc})
	ie.runWith(t, options{}, j)
	if r := readOp(t, ie.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	doc, name, equal := ie.filledRecord(t)
	if strings.Contains(doc, "HostnameConfig") {
		t.Errorf("the stored base keeps the document: %q", doc)
	}
	if name.String != "node-a" || !equal.Valid || !equal.Bool {
		t.Errorf("record %+v, %+v", name, equal)
	}
}

// A bare hostname document is removed too, and records that it was not the fill's own.
func TestImportRecordsABareHostnameAsNotEqual(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.nameMachine(t, "node-a")
	_, j := ie.startJob(t, map[string]any{"document": "machine:\n  token: " + runToken + "\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nhostname: node-a\n"})
	ie.runWith(t, options{}, j)
	doc, name, equal := ie.filledRecord(t)
	if strings.Contains(doc, "HostnameConfig") || name.String != "node-a" || !equal.Valid || equal.Bool {
		t.Errorf("document %q, record %+v, %+v", doc, name, equal)
	}
}

// Nothing removed, nothing recorded: both columns stay null.
func TestImportWithoutAFilledHostnameRecordsNothing(t *testing.T) {
	for label, document := range map[string]string{
		"no document":      "machine:\n  token: " + runToken + "\n",
		"another hostname": "machine:\n  token: " + runToken + "\n---\n" + strings.Replace(hostnameDoc, "node-a", "node-b", 1),
	} {
		t.Run(label, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			ie.nameMachine(t, "node-a")
			_, j := ie.startJob(t, map[string]any{"document": document})
			ie.runWith(t, options{}, j)
			if _, name, equal := ie.filledRecord(t); name.Valid || equal.Valid {
				t.Errorf("record %+v, %+v", name, equal)
			}
		})
	}
}

// Persistence-api §4.2: the draft transaction reads the name again, and a rename between the request
// and the commit refuses 409 stale-input naming the name that was compared and the one now stored.
// The claim is abandoned and the draft is untouched. Control: the same import with no rename succeeds.
func TestImportRefusesAfterARename(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.nameMachine(t, "node-a")
	before := ie.currentETag(t)
	op, j := ie.startJob(t, map[string]any{"document": "machine:\n  token: " + runToken + "\n---\n" + hostnameDoc})
	ie.runWith(t, options{beforeT1: func() { ie.nameMachine(t, "node-b") }}, j)
	p := ie.wantFailed(t, op, j.claim.ID, 409, "stale-input")
	conflicts, _ := p["conflicts"].([]any)
	if len(conflicts) != 1 {
		t.Fatalf("problem %v", p)
	}
	c, _ := conflicts[0].(map[string]any)
	if c["machine"] != ie.machine || c["expected"] != "node-a" || c["actual"] != "node-b" {
		t.Errorf("conflict %v", c)
	}
	ie.draftUnchanged(t, before)
}

// An import that removed nothing does not read the name: a rename after the request leaves it alone.
func TestImportWithoutARemovalIgnoresARename(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.nameMachine(t, "node-a")
	op, j := ie.startJob(t, map[string]any{})
	ie.runWith(t, options{beforeT1: func() { ie.nameMachine(t, "node-b") }}, j)
	if r := readOp(t, ie.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
}

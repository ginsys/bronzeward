package api

import (
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// importBaseSentinel is an unmarked value: the sanitized document the revision stores still holds
// it, so only the summary's own fields may answer anything about the document, never its text.
const importBaseSentinel = "bw-import-base-sentinel-4c1d"

func importBaseCall(tok, rev string) call {
	return call{method: "GET", path: prefix + "/import-base-revisions/" + rev, token: tok}
}

// importedBase runs an import of twoSecrets plus the sentinel to its end and returns the import
// base revision it wrote.
func (ie *ingestEnv) importedBase(t *testing.T) string {
	t.Helper()
	op, j := ie.startJob(t, map[string]any{"document": twoSecrets + "  network:\n    hostname: " + importBaseSentinel + "\n",
		"marks": []string{labelMark}})
	ie.runWith(t, options{}, j)
	r := readOp(t, ie.db, op)
	ibr, _ := r.result["importBaseRevision"].(string)
	if r.state != "succeeded" || ibr == "" {
		t.Fatalf("operation %+v", r)
	}
	return ibr
}

// PA §3.2, §9.3, choice §17.38: the summary answers that a revision exists, whose it is, when it
// was written and how much it holds, to an author, under no-store, and nothing else: no document
// text, no digest, no reference name, no generation path.
func TestImportBaseSummary(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ibr := ie.importedBase(t)
	var author, createdAt string
	var bytes, refs, embedded int
	var names []string
	if err := ie.db.QueryRow(`SELECT author, to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS'), octet_length(document),
		(SELECT count(*) FROM import_base_reference WHERE revision = id), jsonb_array_length(embedded)
		FROM import_base_revision WHERE id = $1`, ibr).Scan(&author, &createdAt, &bytes, &refs, &embedded); err != nil {
		t.Fatal(err)
	}
	rows, err := ie.db.Query(`SELECT name FROM import_base_reference WHERE revision = $1`, ibr)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	var baseline, config []byte
	if err := ie.db.QueryRow(`SELECT baseline_digest, configuration_digest FROM import_base_revision WHERE id = $1`, ibr).Scan(&baseline, &config); err != nil {
		t.Fatal(err)
	}
	if refs != 2 || len(names) != 2 || bytes <= len(importBaseSentinel) {
		t.Fatalf("control: %d references, names %v, %d bytes", refs, names, bytes)
	}

	rec := ie.do(ie.api, importBaseCall(ie.human("h-author"), ibr))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Bronzeward-Epoch") == "" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	ie.bodies = append(ie.bodies, rec.Body.String())
	b := decode[map[string]any](t, rec, http.StatusOK)
	if len(b) != 7 || b["id"] != ibr || b["machine"] != ie.machine || b["author"] != author ||
		b["documentBytes"] != float64(bytes) || b["references"] != float64(refs) || b["embedded"] != float64(embedded) ||
		!strings.HasPrefix(fmtString(b["createdAt"]), createdAt) {
		t.Fatalf("body %s; want author %s, %d bytes, %d references, %d embedded, created %s", rec.Body, author, bytes, refs, embedded, createdAt)
	}
	// Nothing of the document, the digests, the references or the provider reaches the answer or a log.
	leaks := []string{importBaseSentinel, "gen/", "!bwref", runToken, runLabel, hex.EncodeToString(baseline), hex.EncodeToString(config),
		base64.StdEncoding.EncodeToString(baseline), base64.StdEncoding.EncodeToString(config)}
	leaks = append(leaks, names...)
	for _, s := range leaks {
		if strings.Contains(rec.Body.String(), s) {
			t.Errorf("the summary holds %q: %s", s, rec.Body)
		}
		if ie.logged(s) {
			t.Errorf("a log line holds %q", s)
		}
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

// PA §9.2, §10.3: the summary is for an author, human only: a service identity holding author and
// a human viewer are refused 403.
func TestImportBaseSummaryRoles(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ibr := ie.importedBase(t)
	for name, tok := range map[string]string{"service": ie.robot, "viewer": ie.human("h-viewer")} {
		if p := wantProblem(t, ie.refused(importBaseCall(tok, ibr)), http.StatusForbidden, "forbidden"); strings.Contains(p["detail"].(string), importBaseSentinel) {
			t.Fatalf("%s: problem %v", name, p)
		}
	}
}

// PA §9.1, §9.3: an unknown revision and another entity's identifier are 404 not-found, and an item
// read takes no query.
func TestImportBaseSummaryNotFound(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ibr := ie.importedBase(t)
	author := ie.human("h-author")
	wantProblem(t, ie.refused(importBaseCall(author, id.New(id.ImportBase))), http.StatusNotFound, "not-found")
	wantProblem(t, ie.refused(importBaseCall(author, ie.draft)), http.StatusNotFound, "not-found")
	wantProblem(t, ie.refused(importBaseCall(author, "not-an-id")), http.StatusNotFound, "not-found")
	wantProblem(t, ie.refused(call{method: "GET", path: prefix + "/import-base-revisions/" + ibr + "?x=1", token: author}),
		http.StatusBadRequest, "invalid-request")
}

// PA §3.2, §9.3: a machine's applied block names the import base revision of its Applied release,
// which is the one a draft without an import base entry compiles on, so a client reaches the
// summary from the machine with one read. A machine with no Applied has no applied block.
func TestMachineAppliedImportBase(t *testing.T) {
	p := newPublishEnv(t)
	viewer := p.human("h-viewer")
	get := func() machineBody {
		return decode[machineBody](t, p.do(p.api, call{method: "GET", path: prefix + "/machines/" + p.machine, token: viewer}), http.StatusOK)
	}
	if m := get(); m.Applied != nil {
		t.Fatalf("control: applied before any release is applied: %+v", m.Applied)
	}
	p.nextDraft() // publishes the fixture's draft and records its release Applied
	m := get()
	if m.Applied == nil || m.Applied.ImportBase == nil || *m.Applied.ImportBase != p.ibr {
		t.Fatalf("applied %+v; want import base %s", m.Applied, p.ibr)
	}
	// The list answers the same block, one row per machine.
	l := decode[listPage[machineBody]](t, p.do(p.api, call{method: "GET", path: prefix + "/machines", token: viewer}), http.StatusOK)
	var seen int
	for _, it := range l.Items {
		if it.ID == p.machine {
			seen++
			if it.Applied == nil || it.Applied.ImportBase == nil || *it.Applied.ImportBase != p.ibr {
				t.Fatalf("list applied %+v", it.Applied)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("the list holds the machine %d times", seen)
	}
}

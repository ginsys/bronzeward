package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
)

// No API request creates or places the CA key's typed reference (compilation.md §5.1). Each case
// says whether it can fail without the guard (red) or holds a refusal that stands anyway (guard).

// taggedNodeConfig is a configuration that already carries the tag at its place.
const taggedNodeConfig = "version: v1alpha1\nmachine:\n  type: controlplane\n  ca:\n    key: !bwref talos/ca-key\n"

func caKeyDeclaration() map[string]any {
	return map[string]any{"references": map[string]any{ingest.CAKeyName: map[string]any{"kind": "talos-ca-key"}}}
}

// seedCAKeyRow gives ie's cluster an import base that holds the typed reference, so a carried name
// would find a row to resolve to.
func (ie *ingestEnv) seedCAKeyRow(t *testing.T) {
	t.Helper()
	ibr := id.New(id.ImportBase)
	tx, err := ie.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at, author) VALUES ($1, $2, $3, '[]', '\x01', $4, 'transit/baseline-digest:1', $4, now(),
		(SELECT min(id) FROM principal))`, ibr, ie.machine, taggedNodeConfig, make([]byte, 32))
	mustExec(t, tx, `INSERT INTO import_base_reference (revision, name, kind) VALUES ($1, $2, 'talos-ca-key')`, ibr, ingest.CAKeyName)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func (ie *ingestEnv) noReferenceWritten(t *testing.T, imports int) {
	t.Helper()
	if n := count(t, ie.db, `SELECT count(*) FROM fragment_reference`); n != 0 {
		t.Fatalf("%d fragment reference rows", n)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM import_base_reference`); n != imports {
		t.Fatalf("%d import base reference rows, want %d", n, imports)
	}
}

// Red: without the request guard the update ends unknown-reference, not bad-declaration.
func TestFragmentPutCannotDeclareTheCAKey(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	ie.seedCAKeyRow(t)
	rec := ie.putFragment("ca-key", fragmentPut(t, "cluster", taggedNodeConfig, []string{}, caKeyDeclaration()), ie.etag,
		"k-fragment-ca-key-0001")
	p := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed")
	if p["rule"] != string(ingest.RuleBadDeclaration) {
		t.Fatalf("rule %v", p["rule"])
	}
	ie.noReferenceWritten(t, 1)
}

// Guard: an ingestion takes no declared reference, and neither do a profile or an assignment.
func TestRequestsTakeNoCAKeyDeclaration(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	wantProblem(t, ie.do(ie.api, ingestCall(author, "k-ca-key-declared-0001", ie.etag,
		ie.body(map[string]any{"declarations": caKeyDeclaration(), "document": taggedNodeConfig}))),
		http.StatusBadRequest, "invalid-request")
	for path, body := range map[string]string{
		"/profiles/p":                `{"fragments":["frv_aaaaaaaaaaaaaaaaaaaaaaaaaa"],"declarations":{}}`,
		"/assignments/" + ie.machine: `{"profiles":[],"fragments":{},"declarations":{}}`,
	} {
		wantProblem(t, ie.do(ie.api, call{method: "PUT", path: prefix + "/drafts/" + ie.draft + path, token: author,
			key: "k-ca-key-body-" + id.New(id.Draft)[4:20], ifMatch: ie.etag, body: body}), http.StatusBadRequest, "invalid-request")
	}
	ie.noReferenceWritten(t, 0)
}

// Guard: a document that carries the tag with no declaration is refused, from a request's own
// document.
func TestImportDocumentWithTheCAKeyTag(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, map[string]any{"document": taggedNodeConfig})
	ie.runWith(t, options{}, j)
	if p := ie.wantFailed(t, op, j.claim.ID, http.StatusUnprocessableEntity, "validation-failed"); p["rule"] != string(ingest.RuleUndeclaredName) {
		t.Fatalf("rule %v", p["rule"])
	}
	ie.noReferenceWritten(t, 0)
}

// Guard: and from a node's configuration, for an import and a drift adoption. A node cannot serve a
// tagged value (the machinery does not load one), so the text arrives as a string.
func TestNodeConfigurationWithTheCAKeyTag(t *testing.T) {
	for _, kind := range []string{"import", "drift-adoption"} {
		t.Run(kind, func(t *testing.T) {
			me := newMachineEnv(t)
			me.node.SetMachineConfig(t, []byte(strings.Replace(standInConfig, runToken, `"!bwref talos/ca-key"`, 1)))
			if kind == "drift-adoption" {
				openDriftOn(t, me.db, me.machine)
			}
			op, j := me.startJob(t, map[string]any{"kind": kind, "source": "machine", "document": nil})
			me.runWith(t, options{}, j)
			r := readOp(t, me.db, op)
			if r.state != "failed" || r.error["rule"] != string(ingest.RuleReservedText) {
				t.Fatalf("operation %+v", r)
			}
			me.noReferenceWritten(t, 0)
		})
	}
}

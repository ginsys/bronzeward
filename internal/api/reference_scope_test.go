package api

import (
	"net/http"
	"testing"
)

// C §5.1 and §13: a declared reference may only resolve in its draft's cluster.
// Mint the reference through the API in the other cluster, from each supported
// source, so removing either carriedReference cluster predicate breaks this check.
func TestFragmentPutRefusesOtherClusterReference(t *testing.T) {
	for _, origin := range []string{"fragment", "import-base"} {
		t.Run(origin, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			localCluster, localMachine, localDraft, localETag := ie.cluster, ie.machine, ie.draft, ie.etag
			var foreignCluster string
			if err := ie.db.QueryRow(`SELECT cluster FROM machine WHERE id = $1`, ie.otherMachine).Scan(&foreignCluster); err != nil {
				t.Fatal(err)
			}
			r := ie.do(ie.api, call{method: "POST", path: prefix + "/drafts", token: ie.human("h-author"),
				key: "k-foreign-draft-012345", body: `{"cluster":"` + foreignCluster + `","title":"foreign reference"}`})
			ie.cluster, ie.machine = foreignCluster, ie.otherMachine
			ie.draft, ie.etag = decode[draftBody](t, r, http.StatusCreated).ID, r.Header().Get("ETag")
			var minted fragmentRef
			if origin == "fragment" {
				b := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil),
					ie.etag, "k-foreign-mint-0123456"))
				minted = fragmentRefs(t, ie, *b.Entry.Revision)[0]
			} else {
				_, j := ie.startJob(t, map[string]any{"document": labelDoc, "marks": []string{labelMark}})
				ie.runWith(t, ie.captured, j)
				if err := ie.db.QueryRow(`SELECT name, kind, generation FROM import_base_reference`).Scan(
					&minted.name, &minted.kind, &minted.generation); err != nil {
					t.Fatal(err)
				}
				ie.etag = ie.currentETag(t)
			}
			doc := "machine:\n  nodeLabels:\n    tier: !bwref " + minted.name + "\n"
			decl := map[string]any{"references": map[string]any{minted.name: map[string]any{"kind": minted.kind, "version": 1}}}
			body := fragmentPut(t, "role", doc, []string{}, decl)
			calls, _ := ie.f.paths()
			control := ie.fragmentOK(t, ie.putFragment("workers", body, ie.etag, "k-foreign-control-0123"))
			refs := fragmentRefs(t, ie, *control.Entry.Revision)
			if len(refs) != 1 || refs[0] != minted {
				t.Fatal("same-cluster control did not carry the minted reference")
			}
			ie.cluster, ie.machine, ie.draft, ie.etag = localCluster, localMachine, localDraft, localETag
			p := wantProblem(t, ie.putFragment("workers", body, ie.etag, "k-cross-cluster-012345"),
				http.StatusUnprocessableEntity, "validation-failed")
			if p["rule"] != "unknown-reference" || p["reference"] != minted.name {
				t.Fatalf("wrong refusal: %v", p)
			}
			claim, _ := p["ingestion"].(string)
			if n := count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE id = $1 AND draft = $2 AND state = 'abandoned'`, claim, localDraft); n != 1 {
				t.Fatal("refused claim was not abandoned in the original draft")
			}
			if n := count(t, ie.db, `SELECT count(*) FROM fragment_revision WHERE cluster = $1`, localCluster) +
				count(t, ie.db, `SELECT count(*) FROM draft_source_entry WHERE draft = $1`, localDraft); n != 0 || ie.currentETag(t) != localETag {
				t.Fatal("cross-cluster refusal changed the local draft or persisted a fragment")
			}
			if after, _ := ie.f.paths(); after != calls {
				t.Fatal("carrying or refusing the reference created a generation")
			}
			assertAbsent(t, ie, runLabel)
		})
	}
}

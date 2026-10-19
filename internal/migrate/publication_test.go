package migrate

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// A release cannot omit its evidence or borrow a read from another publication.
// The matching control copies the same machine/source/dependency unit successfully.
func TestReleasePublicationEvidence(t *testing.T) {
	db, _ := installed(t)
	r := releaseRows(t, db)
	op := id.New(id.Operation)
	mustExec(t, db, insertOperation, op, "publish", "running", "run-1/4242/publish-2", 1, r.draft2, 1, nil, r.human, nil, nil)
	valid := dbtest.PublicationObservation(t, db, r.machine, op)
	var old string
	if err := db.QueryRow(`SELECT publication_observation FROM release_machine WHERE release = $1`, r.rel).Scan(&old); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name        string
		observation any
		want        string
	}{
		{"missing", nil, "23502"}, {"unknown", id.New(id.Observation), "23503"}, {"other publication", old, "release_publication_evidence"}, {"matching", valid, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			rel := id.New(id.Release)
			mustExec(t, tx, insertRelease, rel, r.cluster, r.draft2, 1, digest(3), "v1.13", machinery, checksum, "v1.36.0", op, r.human, "publisher")
			_, err = tx.Exec(`INSERT INTO release_machine (release,cluster,machine,import_base_revision,assignment_revision,mode,ciphertext,
   ciphertext_digest,configuration_digest,redacted,provenance,key_name,publication_observation)
   SELECT $1,cluster,machine,import_base_revision,assignment_revision,mode,ciphertext,ciphertext_digest,configuration_digest,
    redacted,provenance,key_name,$3 FROM release_machine WHERE release=$2`, rel, r.rel, c.observation)
			if err == nil {
				mustExec(t, tx, `INSERT INTO release_source (release,cluster,kind,fragment,profile,assignment,name,machine,fragment_revision,profile_revision,assignment_revision,head_revision)
    SELECT $1,cluster,kind,fragment,profile,assignment,name,machine,fragment_revision,profile_revision,assignment_revision,head_revision FROM release_source WHERE release=$2`, rel, r.rel)
				mustExec(t, tx, `INSERT INTO dependency (release,machine,kind,provider,object,version,created,reference,source_revision,source_digest,path,occurrence)
    SELECT $1,machine,kind,provider,object,version,created,reference,source_revision,source_digest,path,occurrence FROM dependency WHERE release=$2`, rel, r.rel)
				err = tx.Commit()
			}
			if refusal(err) != c.want {
				t.Fatalf("%v; got %s want %s", err, refusal(err), c.want)
			}
		})
	}
}

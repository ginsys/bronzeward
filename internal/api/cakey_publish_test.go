package api

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// Publication of an import base that holds the typed CA-key reference (compilation.md §6,
// persistence-api.md §16). Ingestion does not create the reference yet, so the rows are written
// directly; the key is the one the test's own generated configuration holds.

// caKeyEnv is a buildEnv whose machine's import base names the CA key by the typed reference.
type caKeyEnv struct {
	*buildEnv
	path    string // the signer's path of the cluster
	encoded string // the key as machine.ca.key holds it
}

func newCAKeyEnv(t *testing.T) *caKeyEnv {
	t.Helper()
	b := newBuildEnv(t)
	c := &caKeyEnv{buildEnv: b, path: "access/talos-ca/" + b.cluster}
	src := b.ingested(generatedConfig(t, constants.DefaultKubernetesVersion))
	var doc struct {
		Machine struct {
			CA struct{ Key yaml.Node } `yaml:"ca"`
		} `yaml:"machine"`
	}
	if err := yaml.Unmarshal([]byte(src.document), &doc); err != nil {
		t.Fatal(err)
	}
	name := doc.Machine.CA.Key.Value
	v, ok := b.held.values[src.gens[name]]
	if !ok {
		t.Fatal("machine.ca.key was not extracted")
	}
	c.encoded, _ = decoded(t, v).(string)
	raw, err := base64.StdEncoding.DecodeString(c.encoded)
	if err != nil || len(raw) == 0 {
		t.Fatalf("machine.ca.key is not base64: %v", err)
	}
	pem, err := provider.NewValue(provider.KindString, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	delete(b.held.values, src.gens[name])
	b.held.values[c.path] = pem
	src.document = strings.Replace(src.document, "!bwref "+name, "!bwref "+ingest.CAKeyName, 1)
	delete(src.refs, name)
	delete(src.gens, name)

	ibr := id.New(id.ImportBase)
	tx, err := b.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at, author) VALUES ($1, $2, $3, $4, '\x01', $5, 'transit/baseline-digest:1', $5, now(),
		(SELECT min(id) FROM principal))`, ibr, b.machine, src.document, src.embedded, make([]byte, 32))
	for n, r := range src.refs {
		mustExec(t, tx, `INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
			VALUES ($1, $2, $3, $4, nullif($5, ''), $6)`, ibr, n, string(r.Kind), r.Version, r.Encoding, src.gens[n])
	}
	mustExec(t, tx, `INSERT INTO import_base_reference (revision, name, kind) VALUES ($1, $2, $3)`,
		ibr, ingest.CAKeyName, string(provider.KindCAKey))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, b.db, `UPDATE draft_entry SET import_base_revision = $1 WHERE draft = $2 AND machine = $3`, ibr, b.draft, b.machine)
	b.ibr = ibr
	return c
}

// firstKey records a first-key act naming version of the cluster's CA key, created at created.
func (c *caKeyEnv) firstKey(version int, created time.Time) {
	c.t.Helper()
	act := id.New(id.Act)
	mustExec(c.t, c.db, `INSERT INTO act (id, principal, principal_kind, via, action, subjects, epoch, at)
		SELECT $1, p.id, p.kind, 'tool', 'test', '{}', i.epoch, now() FROM principal p, installation_state i ORDER BY p.id LIMIT 1`, act)
	mustExec(c.t, c.db, `INSERT INTO first_key_act (act, cluster, kind, replaced, ca_fingerprint, key_path, key_version, key_created_time)
		VALUES ($1, $2, 'machine-config', $3, repeat('a', 64), $4, $5, $6)`, act, c.cluster, version > 1, c.path, version, created)
}

func (c *caKeyEnv) keyRecords(rel string) string {
	c.t.Helper()
	var rows sql.NullString
	if err := c.db.QueryRow(`SELECT string_agg(kind || ' ' || object || ' ' || version, ',' ORDER BY kind) FROM dependency
		WHERE release = $1 AND reference = $2`, rel, ingest.CAKeyName).Scan(&rows); err != nil {
		c.t.Fatal(err)
	}
	return rows.String
}

// The release records the signer's path at the act's version, as an effective and a reproduction
// dependency, and holds no form of the key.
func TestPublishRendersTheCAKey(t *testing.T) {
	c := newCAKeyEnv(t)
	c.firstKey(1, kvCreated)
	u, ref := c.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	c.unit = u
	rel, ref := c.commit()
	if ref != nil {
		t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	if got, want := c.keyRecords(rel), fmt.Sprintf("effective %s 1,reproduction %s 1", c.path, c.path); got != want {
		t.Fatalf("CA-key dependencies %q, want %q", got, want)
	}
	var rows string
	if err := c.db.QueryRow(`SELECT concat_ws(E'\n',
		(SELECT string_agg(t::text, E'\n') FROM release_machine t WHERE release = $1),
		(SELECT string_agg(t::text, E'\n') FROM dependency t WHERE release = $1))`, rel).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rows, c.encoded) || c.logged(c.encoded) {
		t.Fatal("the key is shown")
	}
	c.noSecretIn(rows + fmt.Sprint(u.machines[0].redacted))
}

func TestPublishRefusesWithoutFirstKeyAct(t *testing.T) {
	c := newCAKeyEnv(t)
	_, ref := c.build()
	if ref == nil || ref.status != 503 || ref.code != "dependency-unavailable" || !strings.Contains(ref.detail, "first-keys") {
		t.Fatalf("got %v", ref)
	}
}

// The path was written again since the act: the version's created_time is not the act's.
func TestPublishRefusesARewrittenCAKey(t *testing.T) {
	c := newCAKeyEnv(t)
	c.firstKey(1, kvCreated.Add(time.Second))
	_, ref := c.build()
	if ref == nil || ref.status != 422 || ref.code != "validation-failed" {
		t.Fatalf("got %v", ref)
	}
	dep, _ := ref.extra["dependency"].(map[string]any)
	if dep["reason"] != "version-changed" || dep["object"] != c.path {
		t.Fatalf("dependency %v", dep)
	}
}

// A first key supplied between the pin and the read refuses the publication; the next one pins the
// new version and publishes.
func TestPublishRefusesAFirstKeyBetweenPinAndRead(t *testing.T) {
	c := newCAKeyEnv(t)
	c.firstKey(1, kvCreated)
	supplied := false
	c.a.o.stopAt = func(step string) bool {
		if step == "publish-observations" && !supplied { // after the snapshot's pin, before the reads
			supplied = true
			c.firstKey(2, kvCreated)
		}
		return false
	}
	_, ref := c.build()
	if !supplied {
		t.Fatal("the first key was not supplied")
	}
	if ref == nil || ref.status != 409 || ref.code != "conflict" {
		t.Fatalf("got %v", ref)
	}
	u, ref := c.build()
	if ref != nil {
		t.Fatalf("the next publication refused: %v %v", ref, ref.extra)
	}
	c.unit = u
	rel, ref := c.commit()
	if ref != nil {
		t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	if got := c.keyRecords(rel); !strings.HasSuffix(got, c.path+" 2") {
		t.Fatalf("CA-key dependencies %q, want version 2", got)
	}
}

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
// directly; the key is the one the test's own generated configuration holds. The cluster's first-key
// act is the one the publication environment's signer seeded.

// caKeyEnv is a buildEnv whose machine's import base names the CA key by the typed reference.
type caKeyEnv struct {
	*buildEnv
	path    string // the signer's path of the cluster
	encoded string // the key as machine.ca.key holds it
	act     provider.CAKeyVersion
}

func newCAKeyEnv(t *testing.T) *caKeyEnv {
	t.Helper()
	b := newBuildEnv(t)
	c := &caKeyEnv{buildEnv: b, path: "access/talos-ca/" + b.cluster, act: latestActVersion(t, b.db, b.cluster)}
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
	b.held.caCreated = []time.Time{c.act.CreatedTime}
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

// publish builds and commits the release and answers its CA-key dependencies.
func (c *caKeyEnv) publish() (releaseUnit, string, string) {
	c.t.Helper()
	u, ref := c.build()
	if ref != nil {
		c.t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	c.unit = u
	rel, ref := c.commit()
	if ref != nil {
		c.t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	var rows sql.NullString
	if err := c.db.QueryRow(`SELECT string_agg(kind || ' ' || object || ' ' || version, ',' ORDER BY kind) FROM dependency
		WHERE release = $1 AND reference = $2`, rel, ingest.CAKeyName).Scan(&rows); err != nil {
		c.t.Fatal(err)
	}
	return u, rel, rows.String
}

func (c *caKeyEnv) records(version int) string {
	return fmt.Sprintf("effective %s %d,reproduction %s %d", c.path, version, c.path, version)
}

// The release records the signer's path at the act's version, as an effective and a reproduction
// dependency, and holds no form of the key.
func TestPublishRendersTheCAKey(t *testing.T) {
	c := newCAKeyEnv(t)
	u, rel, got := c.publish()
	if want := c.records(c.act.Version); got != want {
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

func TestPublishRefusesTheCAKeyWithoutFirstKeyAct(t *testing.T) {
	c := newCAKeyEnv(t)
	c.noSeed = true
	dropFirstKeyActs(t, c.db, c.cluster)
	_, ref := c.build()
	if ref == nil || ref.status != 503 || ref.code != "dependency-unavailable" || !strings.Contains(ref.detail, "no first-key act names") {
		t.Fatalf("got %v", ref)
	}
}

// The path was written again since the act: the version's created_time is not the act's.
func TestPublishRefusesARewrittenCAKey(t *testing.T) {
	c := newCAKeyEnv(t)
	c.held.caCreated = []time.Time{c.act.CreatedTime.Add(time.Second)}
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
	later := c.act.CreatedTime.Add(time.Second)
	c.held.caCreated = append(c.held.caCreated, later)
	supplied := false
	c.a.o.stopAt = func(step string) bool {
		if step == "publish-observations" && !supplied { // after the snapshot's pin, before the reads
			supplied = true
			c.env.keys.seedCreated(t, c.db, c.cluster, later)
		}
		return false
	}
	_, ref := c.build()
	if !supplied {
		t.Fatal("the first key was not supplied")
	}
	if ref == nil || ref.status != 409 || ref.code != "conflict" {
		t.Fatalf("got %v %v", ref, ref)
	}
	if _, _, got := c.publish(); got != c.records(c.act.Version+1) {
		t.Fatalf("CA-key dependencies %q, want %q", got, c.records(c.act.Version+1))
	}
}

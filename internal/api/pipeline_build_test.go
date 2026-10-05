package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/compatibility/talos113"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// buildHeld is the synthetic AES-CBC encryption secret the network fragment adds at a path the
// base does not set (a fragment overriding a base reference is refused, choice §16.20); no record,
// refusal or log line may quote it.
const buildHeld = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

// buildFragment is the network fragment's text, with extra appended to its cluster section.
func buildFragment(extra string) string {
	return "cluster:\n  aescbcEncryptionSecret: &secret " + buildHeld + "\n" + extra
}

// heldValues is a stand-in provider holding the values ingestion extracted, by generation path:
// the metadata identity answers each generation retained, created at kvCreated, and the artifact
// key with one version created at transitCreated (with no Date when noDate); the compiler
// identity reads the held value (or fails with readErr) and encrypts an artifact under keyVersion
// (1 when unset) to a digest of it, never to the plaintext.
type heldValues struct {
	values     map[string]provider.Value
	keyVersion int
	readErr    error
	noDate     bool
}

func (h heldValues) KV(_ context.Context, p provider.GenerationPath) (classify.Answer, error) {
	if _, ok := h.values[p.String()]; !ok {
		return classify.Answer{}, errors.New("stand-in: no such generation")
	}
	return kvAnswer(nil, kvCreated), nil
}

func (h heldValues) Transit(context.Context, string) (classify.Answer, error) {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{
		"keys": map[string]int64{"1": transitCreated.Unix()}, "latest_version": 1, "min_available_version": 0,
		"min_decryption_version": 1, "soft_deleted": false,
	}})
	a := classify.Answer{Status: http.StatusOK, Date: pinDate.Format(http.TimeFormat), Body: classify.NewBody(b)}
	if h.noDate {
		a.Date = ""
	}
	return a, nil
}

func (h heldValues) ReadGeneration(_ context.Context, p provider.GenerationPath, _ int64) (provider.Value, time.Time, error) {
	if h.readErr != nil {
		return provider.Value{}, time.Time{}, h.readErr
	}
	v, ok := h.values[p.String()]
	if !ok {
		return provider.Value{}, time.Time{}, errors.New("stand-in: no such generation")
	}
	return v, kvCreated, nil
}

func (heldValues) ArtifactKey() string { return "bw-artifact" }

func (h heldValues) EncryptArtifact(_ context.Context, plaintext []byte) (provider.Ciphertext, error) {
	sum := sha256.Sum256(plaintext)
	return provider.Ciphertext(fmt.Sprintf("vault:v%d:", max(h.keyVersion, 1)) + base64.StdEncoding.EncodeToString(sum[:])), nil
}

// buildEnv is a publishEnv whose import base is a generated Talos configuration and whose
// changed network fragment adds a secret the base lacks and copies it by alias, both ingested as
// bronzeward ingests them, with the extracted values held by a stand-in provider.
type buildEnv struct {
	*publishEnv
	held    heldValues
	network string // the overriding fragment revision
}

// sanitizedSource is a document ingested as bronzeward ingests it: its stored text, embedded
// identifications, and each reference with the generation path its value is held at.
type sanitizedSource struct {
	document string
	embedded []byte
	refs     map[string]ingest.Reference
	gens     map[string]string
}

func (b *buildEnv) ingested(text string) sanitizedSource {
	b.t.Helper()
	u, err := ingest.Read(strings.NewReader(text), 1<<20)
	if err != nil {
		b.t.Fatal(err)
	}
	c, err := ingest.Extract(ingest.Request{Input: u})
	if err != nil {
		b.t.Fatal(err)
	}
	claim := id.New(id.Ingestion)
	gens := map[string]string{}
	s, err := c.Commit(context.Background(), func(_ context.Context, name string, v provider.Value) error {
		gen := "gen/" + b.cluster + "/" + claim + "/" + provider.NewValueID()
		gens[name], b.held.values[gen] = gen, v
		return nil
	})
	if err != nil {
		b.t.Fatal(err)
	}
	decl := s.Declarations()
	if decl.Embedded == nil {
		decl.Embedded = []ingest.Embedded{}
	}
	emb, err := json.Marshal(decl.Embedded)
	if err != nil {
		b.t.Fatal(err)
	}
	return sanitizedSource{document: string(s.Documents()), embedded: emb, refs: decl.References, gens: gens}
}

// store writes a revision and its reference rows in one transaction, as its writer does.
func (b *buildEnv) store(revision, references string, src sanitizedSource, args ...any) {
	b.t.Helper()
	tx, err := b.db.Begin()
	if err != nil {
		b.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(b.t, tx, revision, args...)
	for name, r := range src.refs {
		mustExec(b.t, tx, `INSERT INTO `+references+` (revision, name, kind, version, encoding, generation)
			VALUES ($1, $2, $3, $4, nullif($5, ''), $6)`, args[0], name, string(r.Kind), r.Version, r.Encoding, src.gens[name])
	}
	if err := tx.Commit(); err != nil {
		b.t.Fatal(err)
	}
}

func generatedConfig(t *testing.T, kubernetes string) string {
	t.Helper()
	in, err := generate.NewInput("build-test", "https://192.0.2.20:6443", kubernetes, generate.WithInstallDisk("/dev/sda"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := in.Config(machine.TypeControlPlane)
	if err != nil {
		t.Fatal(err)
	}
	out, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// importBase stores text ingested as machine's import base and makes it the draft's entry.
func (b *buildEnv) importBase(machine, text string) string {
	b.t.Helper()
	ibr := id.New(id.ImportBase)
	src := b.ingested(text)
	b.store(`INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, $3, $4, '\x01', $5, 'transit/baseline-digest:1', $5, now())`,
		"import_base_reference", src, ibr, machine, src.document, src.embedded, make([]byte, 32))
	mustExec(b.t, b.db, `DELETE FROM draft_entry WHERE draft = $1 AND machine = $2`, b.draft, machine)
	mustExec(b.t, b.db, `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision)
		VALUES ($1, $2, 'import-base', $3, $4)`, b.draft, b.cluster, machine, ibr)
	return ibr
}

// fragment stores text ingested as a revision of the named fragment and makes it the draft's entry.
func (b *buildEnv) fragment(name, layer, text string) string {
	b.t.Helper()
	frv := id.New(id.FragmentRevision)
	src := b.ingested(text)
	b.store(`INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())`, "fragment_reference", src, frv, b.cluster, name, layer, src.document, b.seed, src.embedded)
	b.entry("fragment", name, frv)
	mustExec(b.t, b.db, `UPDATE draft_source_entry SET base = (SELECT head_revision FROM fragment WHERE cluster = $2 AND name = $3)
		WHERE draft = $1 AND kind = 'fragment' AND name = $3`, b.draft, b.cluster, name)
	return frv
}

func newBuildEnv(t *testing.T) *buildEnv {
	t.Helper()
	b := &buildEnv{publishEnv: newPublishEnv(t), held: heldValues{values: map[string]provider.Value{}}}
	b.ibr = b.importBase(b.machine, generatedConfig(t, constants.DefaultKubernetesVersion))
	// The alias places the one reference at two outputs: one effective dependency, not two.
	b.network = b.fragment("network", "site", buildFragment("machine:\n  nodeAnnotations:\n    example.test/copy: *secret\n"))
	return b
}

// secondMachine registers another machine of the cluster whose import base names kubernetes.
func (b *buildEnv) secondMachine(kubernetes string) string {
	b.t.Helper()
	rec := b.do(b.api, machineCall(b.human("h-author"), "k-machine-two-0123456", b.cluster, "1c6b7d2f-3e4a-4f60-9b0c-1d2e3f4a5b6c"))
	second := decode[machineBody](b.t, rec, http.StatusCreated).ID
	b.importBase(second, generatedConfig(b.t, kubernetes))
	return second
}

func (b *buildEnv) build() (releaseUnit, *refusal) {
	b.t.Helper()
	u, ref, err := b.a.buildRelease(b.t.Context(), b.job, publishClients{meta: b.held, reader: b.held, encrypter: b.held})
	if err != nil {
		b.t.Fatalf("buildRelease: %v", err)
	}
	return u, ref
}

// held values that are text: what no record, refusal or log line may hold.
func (b *buildEnv) secrets() []string {
	out := []string{buildHeld}
	for _, v := range b.held.values {
		if s, err := v.Decode(); err == nil {
			if text, ok := s.(string); ok && len(text) >= 8 {
				out = append(out, text)
			}
		}
	}
	return out
}

// noSecretIn fails if any held text value appears in the release's rows or the server's log.
func (b *buildEnv) noSecretIn(rendered string) {
	b.t.Helper()
	for _, s := range b.secrets() {
		if strings.Contains(rendered, s) || b.logged(s) {
			b.t.Fatalf("a held value (%d bytes) is shown", len(s))
		}
	}
}

// compilation.md §6 and §11: the built unit is one T3 commits; its renderer is the build's, each
// machine's records name the generations its sources declare, the overridden base occurrence names
// the fragment that overrode it, and no held value reaches the release.
func TestBuildReleaseCommits(t *testing.T) {
	b := newBuildEnv(t)
	u, ref := b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	version, checksum, err := compile.Machinery()
	if err != nil {
		t.Fatal(err)
	}
	if r := u.renderer; r.Contract != "v1.13" || r.MachineryVersion != version || r.MachineryChecksum != checksum ||
		r.KubernetesVersion != "v"+constants.DefaultKubernetesVersion {
		t.Fatalf("renderer %+v", r)
	}
	if len(u.machines) != 1 {
		t.Fatalf("%d machines", len(u.machines))
	}
	m := u.machines[0]
	if m.machine != b.machine || m.importBase != b.ibr || m.assignment != b.asr || m.mode != "container" || m.redacted == nil ||
		m.encryption.object != "bw-artifact" || m.encryption.version != 1 || !m.encryption.created.Equal(transitCreated) {
		t.Fatalf("machine %s base %s assignment %s mode %s encryption %+v", m.machine, m.importBase, m.assignment, m.mode, m.encryption)
	}
	placed := false
	for _, r := range m.provenance {
		placed = placed || r.Source.Revision == b.network && r.Output != ""
	}
	if !placed {
		t.Fatal("no output is attributed to the network fragment's reference")
	}
	distinct := map[unitDependency]bool{}
	for _, d := range m.effective {
		distinct[d] = true
	}
	if len(m.effective) == 0 || len(m.reproduction) < len(m.effective) || len(distinct) != len(m.effective) {
		t.Fatalf("%d effective, %d reproduction dependencies", len(m.effective), len(m.reproduction))
	}
	for _, d := range append(m.effective, m.reproduction...) {
		if _, ok := b.held.values[d.object]; !ok || !d.created.Equal(kvCreated) {
			t.Fatalf("dependency %s %s version %d created %v", d.reference, d.object, d.version, d.created)
		}
	}
	b.unit = u
	rel, ref := b.commit()
	if ref != nil {
		t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	var rows string
	if err := b.db.QueryRow(`SELECT concat_ws(E'\n',
		(SELECT string_agg(t::text, E'\n') FROM release t WHERE id = $1),
		(SELECT string_agg(t::text, E'\n') FROM release_machine t WHERE release = $1),
		(SELECT string_agg(t::text, E'\n') FROM release_source t WHERE release = $1),
		(SELECT string_agg(t::text, E'\n') FROM dependency t WHERE release = $1),
		(SELECT string_agg(t::text, E'\n') FROM dependency_status t))`, rel).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rows, b.network) {
		t.Fatal("the release's rows do not name the overriding fragment")
	}
	b.noSecretIn(rows + fmt.Sprint(u.machines[0].redacted))
}

// A refused compilation fails the publication 422 naming the machine and rule (ruling R33), and
// names no value.
func TestBuildReleaseRefuses(t *testing.T) {
	minimum := fmt.Sprintf("%d.%d.%d", talos113.MinimumKubernetesVersion.Major, talos113.MinimumKubernetesVersion.Minor,
		talos113.MinimumKubernetesVersion.Patch)
	cases := []struct {
		name    string
		prepare func(b *buildEnv)
		rule    string
		machine bool
	}{
		{"invalid configuration", func(b *buildEnv) {
			b.fragment("network", "site", buildFragment("machine:\n  type: bogus\n"))
		}, "invalid", true},
		// The machinery's own message quotes the held value; only §8.3's redaction keeps it out.
		{"invalid configuration quoting a value", func(b *buildEnv) {
			b.fragment("network", "site", buildFragment("machine:\n  type: *secret\n"))
		}, "", true},
		{"Kubernetes outside the window", func(b *buildEnv) {
			b.fragment("network", "site", "machine:\n  kubelet:\n    image: ghcr.io/siderolabs/kubelet:v1.20.0\n")
		}, "kubernetes", true},
		{"contract not the renderer's", func(b *buildEnv) {
			mustExec(b.t, b.db, `UPDATE cluster SET contract = 'v1.12' WHERE id = $1`, b.cluster)
		}, "contract", false},
		{"machines disagree on Kubernetes", func(b *buildEnv) {
			b.secondMachine(minimum)
		}, "kubernetes", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newBuildEnv(t)
			c.prepare(b)
			_, ref := b.build()
			if ref == nil || ref.status != 422 || ref.code != "validation-failed" {
				t.Fatalf("buildRelease: %v; want 422 validation-failed", ref)
			}
			if c.rule != "" && ref.extra["rule"] != c.rule {
				t.Fatalf("rule %v, want %s", ref.extra["rule"], c.rule)
			}
			if _, ok := ref.extra["machine"]; ok != c.machine {
				t.Fatalf("refusal %v names a machine: %v", ref.extra, ok)
			}
			b.noSecretIn(fmt.Sprint(ref.extra) + ref.detail)
		})
	}
}

// Machines that agree are compiled into one release, each artifact encrypted under the same key
// version, which seeds one status (dependency monitor §5.2: one row per identity).
func TestBuildReleaseMachines(t *testing.T) {
	b := newBuildEnv(t)
	second := b.secondMachine(constants.DefaultKubernetesVersion)
	u, ref := b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	got := map[string]bool{}
	for _, m := range u.machines {
		got[m.machine] = len(m.ciphertext) > 0 && m.encryption.version == 1
	}
	if len(got) != 2 || !got[b.machine] || !got[second] {
		t.Fatalf("machines %v", got)
	}
	transit := 0
	for _, s := range u.statuses {
		if s.provider == classify.Transit {
			transit++
		}
	}
	if transit != 1 {
		t.Fatalf("%d Transit statuses, want 1", transit)
	}
	b.unit = u
	if _, ref := b.commit(); ref != nil {
		t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
}

// Ruling R33 for the artifact key: an encryption under a key version the first metadata read did
// not show refuses 422 naming the key and the version the ciphertext names, in the detail too.
func TestBuildReleaseEncryptionRefused(t *testing.T) {
	b := newBuildEnv(t)
	b.held.keyVersion = 2
	_, ref := b.build()
	if ref == nil || ref.status != 422 || ref.code != "validation-failed" {
		t.Fatalf("buildRelease: %v; want 422 validation-failed", ref)
	}
	d, _ := ref.extra["dependency"].(map[string]any)
	if d == nil || d["provider"] != "transit" || d["object"] != "bw-artifact" || d["version"] != int64(2) ||
		!strings.Contains(ref.detail, "bw-artifact version 2") {
		t.Fatalf("refusal %q names %v", ref.detail, ref.extra["dependency"])
	}
	b.noSecretIn(fmt.Sprint(ref.extra) + ref.detail)
}

// Ruling R33's 503: a value read refused or not understood after its classification found it
// retained, and an artifact key's first read with no readable Date, leave the dependency unknown;
// the refusal names it, logs the provider's error and holds no value.
func TestBuildReleaseDependencyUnknown(t *testing.T) {
	for _, c := range []struct {
		name     string
		prepare  func(h *heldValues)
		provider string
	}{
		{"value read absent", func(h *heldValues) { h.readErr = fmt.Errorf("provider: GET: %w", provider.ErrAbsent) }, "kv"},
		{"value read denied", func(h *heldValues) { h.readErr = fmt.Errorf("provider: GET: %w", provider.ErrDenied) }, "kv"},
		{"value read not understood", func(h *heldValues) { h.readErr = fmt.Errorf("provider: GET: %w", provider.ErrProtocol) }, "kv"},
		{"artifact key read without a Date", func(h *heldValues) { h.noDate = true }, "transit"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBuildEnv(t)
			c.prepare(&b.held)
			_, ref := b.build()
			if ref == nil || ref.status != http.StatusServiceUnavailable || ref.code != "dependency-unavailable" {
				t.Fatalf("buildRelease: %v; want 503 dependency-unavailable", ref)
			}
			d, _ := ref.extra["dependency"].(map[string]any)
			if d == nil || d["provider"] != c.provider || d["class"] != string(classify.Unknown) || (c.provider == "kv") != (d["reference"] != nil) {
				t.Fatalf("refusal names %v", ref.extra["dependency"])
			}
			b.noSecretIn(fmt.Sprint(ref.extra) + ref.detail)
		})
	}
}

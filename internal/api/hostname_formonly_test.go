package api

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/ginsys/bronzeward/internal/id"
)

// withoutHostnameConfig is text as an import stores it after step 2a: the generated configuration
// without its HostnameConfig document (compilation.md §2.3 step 2a).
func withoutHostnameConfig(t *testing.T, text string) string {
	t.Helper()
	var keep []string
	for _, d := range strings.Split(text, "\n---\n") {
		if !strings.Contains(d, "kind: HostnameConfig") {
			keep = append(keep, d)
		}
	}
	if len(keep) == 0 || len(keep) == len(strings.Split(text, "\n---\n")) {
		t.Fatal("the generated configuration holds no HostnameConfig document to remove")
	}
	return strings.Join(keep, "\n---\n")
}

// importBaseFilled is importBase with step 2a's record on the row: the name whose document the import
// removed and whether that document was the fill's own.
func (b *buildEnv) importBaseFilled(machine, text, name string, equal bool) string {
	b.t.Helper()
	ibr := id.New(id.ImportBase)
	src := b.ingested(text)
	b.store(`INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, filled_hostname, filled_hostname_equal, created_at, author) VALUES ($1, $2, $3, $4, '\x01', $5,
		'transit/baseline-digest:1', $5, $6, $7, now(), (SELECT min(id) FROM principal))`,
		"import_base_reference", src, ibr, machine, src.document, src.embedded, make([]byte, 32), name, equal)
	mustExec(b.t, b.db, `DELETE FROM draft_entry WHERE draft = $1 AND machine = $2`, b.draft, machine)
	mustExec(b.t, b.db, `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision)
		VALUES ($1, $2, 'import-base', $3, $4)`, b.draft, b.cluster, machine, ibr)
	return ibr
}

func (b *buildEnv) releaseFormOnly() bool {
	b.t.Helper()
	var formOnly bool
	if err := b.db.QueryRow(`SELECT form_only FROM release_machine WHERE import_base_revision = $1`, b.ibr).Scan(&formOnly); err != nil {
		b.t.Fatal(err)
	}
	return formOnly
}

// compilation.md §6.1 item 5: a machine onboarded with its fill's own HostnameConfig removed from the
// stored base compiles form only while its name is the recorded one. After a rename the release differs
// in content, and that content is the hostname. A record that says the removed document was not the
// fill's own, or no record, keeps the verdict false.
func TestFormOnlyOfAnOnboardedMachineRelease(t *testing.T) {
	stored := func(t *testing.T) string {
		return withoutHostnameConfig(t, generatedConfig(t, constants.DefaultKubernetesVersion))
	}
	for _, tc := range []struct {
		label, name, filled string
		equal               bool
		want                bool
	}{
		{"name unchanged", "web-1", "web-1", true, true},
		{"renamed after the import", "web-2", "web-1", true, false},
		{"the removed document was not the fill's own", "web-1", "web-1", false, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			b := newBuildEnv(t)
			b.emptyAssignment()
			mustExec(t, b.db, `UPDATE machine SET name = $1 WHERE id = $2`, tc.name, b.machine)
			b.ibr = b.importBaseFilled(b.machine, stored(t), tc.filled, tc.equal)
			b.published()
			if got := b.releaseFormOnly(); got != tc.want {
				t.Errorf("form_only = %v, want %v", got, tc.want)
			}
		})
	}
}

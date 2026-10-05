package migrate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/id"
)

// The statements the release tests insert with.
const (
	insertStatus = `INSERT INTO dependency_status (id, provider, object, version, class, reason, first_retained_at,
		unknown_since, observed_from, recorded_at, created) VALUES ($1, $2, $3, $4, $5, $6, now(), $7, now(), now(), $8)`
	// A status with the scheduled deletion time last observed (dependency monitor §3, §5.1).
	insertScheduled = `INSERT INTO dependency_status (id, provider, object, version, class, reason, first_retained_at,
		deletion_observed, observed_from, recorded_at, created) VALUES ($1, $2, $3, $4, $5, $6, now(), $7, now(), now(), $8)`
	// A status observed and recorded at the times given (dependency monitor §5.2, §6.1).
	insertStatusAt = `INSERT INTO dependency_status (id, provider, object, version, class, first_retained_at, observed_from,
		recorded_at, created) VALUES ($1, 'kv', $2, $3, 'retained', now(), $4, $5, $6)`
	insertRelease = `INSERT INTO release (id, cluster, draft, draft_revision, digest, contract, machinery_version,
		machinery_checksum, kubernetes_version, operation, published_by, published_role, epoch, published_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, epoch, now() FROM installation_state`
	insertReleaseMachine = `INSERT INTO release_machine (release, cluster, machine, import_base_revision,
		assignment_revision, mode, ciphertext, ciphertext_digest, configuration_digest, redacted, provenance, key_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12)`
	insertReleaseSource = `INSERT INTO release_source (release, cluster, kind, fragment, profile, assignment, name, machine,
		fragment_revision, profile_revision, assignment_revision, head_revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	insertDependency = `INSERT INTO dependency (release, machine, kind, provider, object, version, created, reference,
		source_revision, source_digest, path, occurrence) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
)

const (
	machinery = "v1.13.6"
	checksum  = "h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0="
	cipher    = "vault:v1:YWJj"
	created   = "2026-09-26T09:12:40.123456789Z"
)

// release holds one published release over sourceRows' draft, inserted by releaseRows.
type release struct {
	sources
	publish, rel, depKV, depKey, frgSite string
}

func releaseRows(t *testing.T, db *sql.DB) release {
	t.Helper()
	r := release{sources: sourceRows(t, db), publish: id.New(id.Operation), rel: id.New(id.Release),
		depKV: id.New(id.Dependency), depKey: id.New(id.Dependency), frgSite: id.New(id.Fragment)}
	kv := generation(r.cluster, r.claim)
	mustExec(t, db, insertOperation, r.publish, "publish", "running", "run-1/4242/publish-1", 1, r.draft, 1, nil, r.human, nil, nil)
	// The fragment the assignment selects under the site layer.
	mustExec(t, db, insertFragment, r.frgSite, r.cluster, "cluster", "site-dns", "site", r.frvSite, 1)
	mustExec(t, db, insertStatus, r.depKV, "kv", kv, 1, "retained", nil, nil, created)
	mustExec(t, db, insertStatus, r.depKey, "transit", "bw-artifact", 1, "retained", nil, nil, "2026-09-26T09:12:40Z")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, insertRelease, r.rel, r.cluster, r.draft, 1, digest(3), "v1.13", machinery, checksum, "v1.36.0",
		r.publish, r.human, "publisher")
	mustExec(t, tx, insertReleaseMachine, r.rel, r.cluster, r.machine, r.ibr, r.asr, "metal", cipher, digest(4), digest(5),
		"machine:\n  type: worker\n", `[]`, "bw-artifact")
	mustExec(t, tx, insertReleaseSource, r.rel, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 1)
	mustExec(t, tx, insertReleaseSource, r.rel, r.cluster, "fragment", r.frgSite, nil, nil, "site-dns", nil, r.frvSite, nil, nil, 1)
	mustExec(t, tx, insertReleaseSource, r.rel, r.cluster, "profile", nil, r.prf, nil, "workers", nil, nil, r.prv, nil, 1)
	mustExec(t, tx, insertReleaseSource, r.rel, r.cluster, "assignment", nil, nil, r.asg, nil, r.machine, nil, nil, r.asr, 1)
	mustExec(t, tx, insertDependency, r.rel, r.machine, "effective", "kv", kv, 1, created, "registry/example-pass", nil, nil, nil, nil)
	mustExec(t, tx, insertDependency, r.rel, r.machine, "reproduction", "kv", kv, 1, created, "registry/example-pass",
		r.frv1, digest(6), "registries:/machine/registries", 0)
	mustExec(t, tx, insertDependency, r.rel, r.machine, "encryption", "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z",
		nil, nil, nil, nil, nil)
	mustExec(t, tx, `UPDATE draft SET state = 'published', release = $2, revision = revision + 1 WHERE id = $1`, r.draft, r.rel)
	mustExec(t, tx, `UPDATE machine_state SET desired = $2, revision = revision + 1 WHERE machine = $1`, r.machine, r.rel)
	mustExec(t, tx, `UPDATE operation SET state = 'succeeded', result = jsonb_build_object('release', $2::text) WHERE id = $1`,
		r.publish, r.rel)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return r
}

// PA §3, §6.2, §7.3; compilation §9, §10.2, §11; dependency monitor §5.1: what the release tables refuse.
func TestReleaseConstraints(t *testing.T) {
	db, _ := installed(t)
	r := releaseRows(t, db)
	kv := generation(r.cluster, r.claim)
	rel2 := id.New(id.Release)
	op2, op3 := id.New(id.Operation), id.New(id.Operation)
	claim2, bob := id.New(id.Ingestion), id.New(id.Principal)
	mustExec(t, db, insertOperation, op2, "publish", "running", "run-1/4242/publish-2", 1, r.draft2, 1, nil, r.human, nil, nil)
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'bob', now())`, bob)
	// A second version of the artifact key, which no ciphertext of withMachine names, and another
	// key at the version it names.
	mustExec(t, db, insertStatus, id.New(id.Dependency), "transit", "bw-artifact", 2, "retained", nil, nil, "2026-10-02T00:00:00Z")
	mustExec(t, db, insertStatus, id.New(id.Dependency), "transit", "bw-other", 1, "retained", nil, nil, "2026-09-26T09:12:40Z")
	// Versions an effective row can name that no declaration of the sources does: another creation of
	// the declared version, a later version, another generation.
	const recreated = "2026-09-26T09:12:40.2Z"
	kvOther := "gen/" + r.cluster + "/" + r.claim + "/v2"
	mustExec(t, db, insertStatus, id.New(id.Dependency), "kv", kv, 1, "retained", nil, nil, recreated)
	mustExec(t, db, insertStatus, id.New(id.Dependency), "kv", kv, 7, "retained", nil, nil, created)
	mustExec(t, db, insertStatus, id.New(id.Dependency), "kv", kvOther, 1, "retained", nil, nil, created)
	// A second machine of the cluster, whose import base declares the same reference.
	machine2, ibr2 := id.New(id.Machine), id.New(id.ImportBase)
	mustExec(t, db, insertMachine, machine2, r.cluster, "3e8d9f4b-5c6a-4b8c-9d2e-3f4a5b6c7d8e", nil, "normal")
	mustExec(t, db, insertImportBase, ibr2, machine2, "machine:\n  type: worker\n", []byte{1}, digest(1), "transit/baseline-digest:1", digest(2))
	mustExec(t, db, insertReference, ibr2, "registry/example-pass", "string", 1, nil, kv)
	mustExec(t, db, insertReference, ibr2, "registry/base-only", "string", 1, nil, kv) // r.ibr does not declare it
	// Sources r.machine's assignment does not select: the fragment extras, declaring a reference no
	// other source does, and the profile extras-set pinning it, both of which machine2's assignment
	// selects; and a workers revision pinning site-dns instead of registries.
	inTx := func(rows ...stmt) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		for _, s := range rows {
			mustExec(t, tx, s.q, s.args...)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	frvExtra, frgExtra := id.New(id.FragmentRevision), id.New(id.Fragment)
	prvExtra, prfExtra, prvSiteOnly := id.New(id.ProfileRevision), id.New(id.Profile), id.New(id.ProfileRevision)
	asr2, asg2 := id.New(id.AssignmentRevision), id.New(id.Assignment)
	inTx(stmt{insertFragmentRevision, []any{frvExtra, r.cluster, "extras", "site", "machine: {}\n", r.human}},
		stmt{insertFragmentReference, []any{frvExtra, "registry/fragment-only", "string", 1, nil, kv}})
	mustExec(t, db, insertFragment, frgExtra, r.cluster, "cluster", "extras", "site", frvExtra, 1)
	inTx(stmt{insertProfileRevision, []any{prvExtra, r.cluster, "extras-set", r.human}},
		stmt{insertProfilePin, []any{prvExtra, r.cluster, 0, frvExtra}})
	mustExec(t, db, insertProfile, prfExtra, r.cluster, "cluster", "extras-set", prvExtra, 1)
	inTx(stmt{insertProfileRevision, []any{prvSiteOnly, r.cluster, "workers", r.human}},
		stmt{insertProfilePin, []any{prvSiteOnly, r.cluster, 0, r.frvSite}})
	inTx(stmt{insertAssignmentRevision, []any{asr2, r.cluster, machine2, r.human}},
		stmt{insertAssignmentProfile, []any{asr2, 0, "extras-set"}}, stmt{insertAssignmentFragment, []any{asr2, "site", 0, "extras"}})
	mustExec(t, db, insertAssignment, asg2, r.cluster, machine2, asr2, 1)
	// rel2 is inserted in each case's transaction first, so its rows are written with it.
	withRelease := []stmt{{insertRelease, []any{rel2, r.cluster, r.draft2, 1, digest(3), "v1.13", machinery, checksum,
		"v1.36.0", op2, r.human, "publisher"}}}
	withMachine := append(withRelease, stmt{insertReleaseMachine, []any{rel2, r.cluster, r.machine, r.ibr, r.asr, "metal",
		cipher, digest(4), digest(5), "x: 1\n", `[]`, "bw-artifact"}})
	newRelease := func(args ...any) []any {
		base := []any{rel2, r.cluster, r.draft2, 1, digest(3), "v1.13", machinery, checksum, "v1.36.0", op2, r.human, "publisher"}
		for i := 0; i+1 < len(args); i += 2 {
			base[args[i].(int)] = args[i+1]
		}
		return base
	}
	machineRow := func(args ...any) []any {
		base := []any{rel2, r.cluster, r.machine, r.ibr, r.asr, "metal", cipher, digest(4), digest(5), "x: 1\n", `[]`, "bw-artifact"}
		for i := 0; i+1 < len(args); i += 2 {
			base[args[i].(int)] = args[i+1]
		}
		return base
	}
	depRow := func(args ...any) []any {
		base := []any{rel2, r.machine, "effective", "kv", kv, 1, created, "registry/example-pass", nil, nil, nil, nil}
		for i := 0; i+1 < len(args); i += 2 {
			base[args[i].(int)] = args[i+1]
		}
		return base
	}
	withEncryption := stmt{insertDependency, depRow(2, "encryption", 3, "transit", 4, "bw-artifact", 6, "2026-09-26T09:12:40Z", 7, nil)}
	// rel2's sources as its compilation used them: the assignment selects the profile workers and
	// the site fragment site-dns, and workers pins registries at frv1.
	source := func(kind, head, name, revision any) stmt {
		row := []any{rel2, r.cluster, kind, nil, nil, nil, name, nil, nil, nil, nil, 1}
		switch kind {
		case "fragment":
			row[3], row[8] = head, revision
		case "profile":
			row[4], row[9] = head, revision
		default: // an assignment names its machine, not a name
			row[5], row[6], row[7], row[10] = head, nil, name, revision
		}
		return stmt{insertReleaseSource, row}
	}
	assignmentSource := source("assignment", r.asg, r.machine, r.asr)
	profileSource := source("profile", r.prf, "workers", r.prv)
	pinnedSource := source("fragment", r.frg, "registries", r.frv1)
	siteSource := source("fragment", r.frgSite, "site-dns", r.frvSite)
	// rel2 complete but for its effective and reproduction rows.
	withSources := append(append([]stmt{}, withMachine...), assignmentSource, profileSource, pinnedSource, siteSource, withEncryption)
	reproduction := func(object any, version int, at, revision any, occurrence int) stmt {
		return stmt{insertDependency, depRow(2, "reproduction", 4, object, 5, version, 6, at, 8, revision, 9, digest(6),
			10, "registries:/machine/registries", 11, occurrence)}
	}
	named := func(reference string, s stmt) stmt { s.args[7] = reference; return s }
	on := func(machine string, s stmt) stmt { s.args[1] = machine; return s }
	// rel2 also naming extras, and the profile extras-set pinning it; r.machine selects neither.
	extrasSource := source("fragment", frgExtra, "extras", frvExtra)
	withExtras := slices.Concat(withSources, []stmt{extrasSource, source("profile", prfExtra, "extras-set", prvExtra)})
	// machine2 in rel2, its assignment selecting both.
	withMachine2 := slices.Concat(withExtras, []stmt{{insertReleaseMachine, machineRow(2, machine2, 3, ibr2, 4, asr2)},
		source("assignment", asg2, machine2, asr2),
		{insertDependency, depRow(1, machine2, 2, "encryption", 3, "transit", 4, "bw-artifact", 6, "2026-09-26T09:12:40Z", 7, nil)}})
	fragmentOnly := []stmt{{insertDependency, depRow(7, "registry/fragment-only")},
		named("registry/fragment-only", reproduction(kv, 1, created, frvExtra, 0))}
	// site-dns, which the assignment selects, at a revision declaring a reference no other source does.
	frvSite2 := id.New(id.FragmentRevision)
	withSite2 := append(append([]stmt{}, withMachine...),
		stmt{insertFragmentRevision, []any{frvSite2, r.cluster, "site-dns", "site", "machine: {}\n", r.human}},
		stmt{insertFragmentReference, []any{frvSite2, "registry/site-only", "string", 1, nil, kv}},
		assignmentSource, profileSource, pinnedSource, source("fragment", r.frgSite, "site-dns", frvSite2), withEncryption)
	frvRole := id.New(id.FragmentRevision) // site-dns in the role layer
	const commit = `SET CONSTRAINTS ALL IMMEDIATE`
	for _, c := range []struct {
		name string
		pre  []stmt
		q    string
		args []any
		want string
	}{
		// release
		{"second release of one draft revision", nil, insertRelease, newRelease(2, r.draft), "23505"},
		{"release of another cluster's draft", nil, insertRelease, newRelease(1, r.other), "23503"},
		{"release of a draft revision 0", nil, insertRelease, newRelease(3, 0), "release_draft_revision_check"},
		{"release digest of 31 bytes", nil, insertRelease, newRelease(4, digest(3)[:31]), "release_digest_check"},
		{"release contract without its minor", nil, insertRelease, newRelease(5, "v1"), "release_contract_check"},
		{"release machinery version without v", nil, insertRelease, newRelease(6, "1.13.6"), "release_machinery_version_check"},
		// A machinery version is a canonical Go module version (semver): no empty or leading-zero part.
		{"release machinery version ending in a dot", nil, insertRelease, newRelease(6, "v1.13.6-."), "release_machinery_version_check"},
		{"release machinery version with an empty prerelease part", nil, insertRelease, newRelease(6, "v1.13.6-alpha..1"),
			"release_machinery_version_check"},
		{"release machinery version with a leading-zero minor", nil, insertRelease, newRelease(6, "v1.013.6"), "release_machinery_version_check"},
		{"release machinery version with a leading-zero numeric prerelease", nil, insertRelease, newRelease(6, "v1.13.6-01"),
			"release_machinery_version_check"},
		{"control: release machinery pseudo-version", nil, insertRelease, newRelease(6, "v1.13.7-0.20260926091240-abcdef123456"), ""},
		{"release with an empty machinery checksum", nil, insertRelease, newRelease(7, ""), "release_machinery_checksum_check"},
		{"release Kubernetes version without its patch", nil, insertRelease, newRelease(8, "v1.36"), "release_kubernetes_version_check"},
		{"second release of one operation", nil, insertRelease, newRelease(9, r.publish), "23505"},
		// Each operation differs from the release's in one column of the key alone.
		{"release of an ingest operation", []stmt{
			{insertClaim, []any{claim2, "transient", "held", nil, nil, nil}},
			{insertOperation, []any{op3, "ingest", "running", "run-1/4242/start-2", 1, r.draft2, 1, claim2, r.human, nil, nil}},
			{`UPDATE operation SET created_role = 'publisher' WHERE id = $1`, []any{op3}},
		}, insertRelease, newRelease(9, op3), "23503"},
		{"release of another draft's publish operation", []stmt{{insertOperation, []any{op3, "publish", "queued", nil, 0,
			r.draft, 1, nil, r.human, nil, nil}}}, insertRelease, newRelease(9, op3), "23503"},
		{"release of another draft revision's publish operation", []stmt{{insertOperation, []any{op3, "publish", "queued", nil, 0,
			r.draft2, 2, nil, r.human, nil, nil}}}, insertRelease, newRelease(9, op3), "23503"},
		{"release published by no principal", nil, insertRelease, newRelease(10, id.New(id.Principal)), "23503"},
		// The publisher is the principal that requested the publish operation, in its role.
		{"release published by another principal than its operation's", nil, insertRelease, newRelease(10, bob), "23503"},
		{"release of a publish operation requested in another role", []stmt{{`UPDATE operation SET created_role = 'approver'
			WHERE id = $1`, []any{op2}}}, insertRelease, newRelease(), "23503"},
		{"release published under the author role", nil, insertRelease, newRelease(11, "author"), "release_published_role_check"},
		{"release with an id of another kind", nil, insertRelease, newRelease(0, id.New(id.Draft)), "release_id_check"},
		// release_machine
		{"machine of another cluster", withRelease, insertReleaseMachine, machineRow(2, r.otherMachine), "23503"},
		{"machine at another machine's import base", withRelease, insertReleaseMachine, machineRow(3, r.otherIBR), "23503"},
		{"machine at no assignment revision", withRelease, insertReleaseMachine, machineRow(4, id.New(id.AssignmentRevision)), "23503"},
		{"machine in an unknown mode", withRelease, insertReleaseMachine, machineRow(5, "vm"), "release_machine_mode_check"},
		// PA §3.3: a machine validates in its recorded platform mode.
		{"machine in a mode other than its platform", withRelease, insertReleaseMachine, machineRow(5, "container"), "release_machine_platform"},
		{"ciphertext without its key version", withRelease, insertReleaseMachine, machineRow(6, "vault:abc"), "release_machine_ciphertext_check"},
		{"ciphertext at key version 0", withRelease, insertReleaseMachine, machineRow(6, "vault:v0:YWJj"), "release_machine_ciphertext_check"},
		// Compilation §9: a key version has up to 19 digits, as a bigint holds.
		{"ciphertext at a key version past bigint", withRelease, insertReleaseMachine,
			machineRow(6, "vault:v9223372036854775808:YWJj"), "release_machine_key_version"},
		{"ciphertext at a key version of 20 digits", withRelease, insertReleaseMachine,
			machineRow(6, "vault:v12345678901234567890:YWJj"), "release_machine_ciphertext_check"},
		{"control: ciphertext at the largest key version", withRelease, insertReleaseMachine,
			machineRow(6, "vault:v9223372036854775807:YWJj"), ""},
		{"ciphertext digest of 31 bytes", withRelease, insertReleaseMachine, machineRow(7, digest(4)[:31]), "release_machine_ciphertext_digest_check"},
		{"configuration digest of 33 bytes", withRelease, insertReleaseMachine, machineRow(8, append(digest(5), 1)), "release_machine_configuration_digest_check"},
		{"empty redacted configuration", withRelease, insertReleaseMachine, machineRow(9, ""), "release_machine_redacted_check"},
		{"provenance that is not a list", withRelease, insertReleaseMachine, machineRow(10, `{}`), "release_machine_provenance_check"},
		{"second row of one machine", withMachine, insertReleaseMachine, machineRow(), "23505"},
		// Compilation §9: each machine's artifact has one encryption dependency, checked at commit.
		{"machine without its encryption dependency", append(withMachine, assignmentSource, profileSource, pinnedSource, siteSource),
			commit, nil, "release_machine_encryption"},
		// The machine's assignment revision is its assignment source's, checked at commit.
		{"machine at an assignment revision the release's sources do not name", append(withMachine, withEncryption), commit, nil,
			"23503"},
		// release_source
		{"source with two heads", withRelease, insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, r.prf, nil, "registries", nil, r.frv1, nil, nil, 1}, "release_source_shape"},
		{"fragment source naming a machine", withRelease, insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", r.machine, r.frv1, nil, nil, 1}, "release_source_shape"},
		{"assignment source with a name", withRelease, insertReleaseSource,
			[]any{rel2, r.cluster, "assignment", nil, nil, r.asg, "workers", r.machine, nil, nil, r.asr, 1}, "release_source_shape"},
		{"source of an unknown kind", withRelease, insertReleaseSource,
			[]any{rel2, r.cluster, "import-base", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 1}, "release_source_kind_check"},
		{"source at head revision 0", withRelease, insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 0}, "release_source_head_revision_check"},
		{"source at another name's revision", withRelease, insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frvSite, nil, nil, 1}, "23503"},
		{"source in another cluster than its release", withRelease, insertReleaseSource,
			[]any{rel2, r.other, "fragment", r.frg, nil, nil, "registries", nil, r.frvOther, nil, nil, 1}, "23503"},
		{"second source of one head", append(withRelease, stmt{insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 1}}), insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frv2, nil, nil, 2}, "23505"},
		// PA §3.1: each pin is a fragment source of the release, and each selected name a source with
		// a revision, a fragment under the layer it carries; checked at commit.
		{"profile pinning a fragment revision the release does not name", append(withRelease, profileSource), commit, nil,
			"release_source_pin"},
		{"profile pinning another revision of a fragment the release names", append(withRelease, profileSource,
			source("fragment", r.frg, "registries", r.frv2)), commit, nil, "release_source_pin"},
		{"assignment selecting a profile the release does not name", append(withRelease, assignmentSource, siteSource),
			commit, nil, "release_source_selection"},
		{"assignment selecting a profile the release removes", append(withRelease, assignmentSource, siteSource,
			source("profile", r.prf, "workers", nil)), commit, nil, "release_source_selection"},
		{"assignment selecting a fragment the release does not name", append(withRelease, assignmentSource, profileSource,
			pinnedSource), commit, nil, "release_source_selection"},
		{"assignment selecting a fragment the release removes", append(withRelease, assignmentSource, profileSource,
			pinnedSource, source("fragment", r.frgSite, "site-dns", nil)), commit, nil, "release_source_selection"},
		{"assignment selecting a fragment under another layer than its revision's", append(withRelease,
			stmt{insertFragmentRevision, []any{frvRole, r.cluster, "site-dns", "role", "machine: {}\n", r.human}},
			assignmentSource, profileSource, pinnedSource, source("fragment", r.frgSite, "site-dns", frvRole)), commit, nil,
			"release_source_selection"},
		// dependency
		{"dependency of a machine the release does not cover", withRelease, insertDependency, depRow(), "23503"},
		{"dependency of an unknown kind", withMachine, insertDependency, depRow(2, "source"), "dependency_kind_check"},
		{"dependency without a status row", withMachine, insertDependency, depRow(5, 2), "23503"},
		// Dependency monitor §3: a version's identity includes its creation time.
		{"dependency on another creation of its version", withMachine, insertDependency,
			depRow(6, "2026-09-26T09:12:41Z"), "23503"},
		{"encryption dependency on KV", withMachine, insertDependency, depRow(2, "encryption", 7, nil), "dependency_shape"},
		{"effective dependency on a Transit key", withMachine, insertDependency,
			depRow(3, "transit", 4, "bw-artifact"), "dependency_shape"},
		{"encryption dependency with a reference", withMachine, insertDependency,
			depRow(2, "encryption", 3, "transit", 4, "bw-artifact"), "dependency_shape"},
		{"reproduction dependency without its source revision", withMachine, insertDependency,
			depRow(2, "reproduction", 9, digest(6), 10, "registries:/machine", 11, 0), "dependency_shape"},
		{"reproduction dependency without its source digest", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.frv1, 10, "registries:/machine", 11, 0), "dependency_shape"},
		{"reproduction dependency without its path", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.frv1, 9, digest(6), 11, 0), "dependency_shape"},
		{"effective dependency with a source", withMachine, insertDependency,
			depRow(8, r.frv1, 9, digest(6), 10, "registries:/machine"), "dependency_shape"},
		{"reproduction source of another kind", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.draft, 9, digest(6), 10, "registries:/machine", 11, 0), "dependency_source_revision_check"},
		{"creation time without its zone", withMachine, insertDependency, depRow(6, "2026-09-26T09:12:40"), "dependency_created_check"},
		{"creation time with ten fraction digits", withMachine, insertDependency,
			depRow(6, "2026-09-26T09:12:40.1234567890Z"), "dependency_created_check"},
		{"second effective row of one version", append(withMachine, stmt{insertDependency, depRow()}), insertDependency,
			depRow(), "23505"},
		{"second reproduction row of one occurrence", append(withMachine, stmt{insertDependency,
			depRow(2, "reproduction", 8, r.ibr, 9, digest(6), 10, "base:/machine/a", 11, 0)}), insertDependency,
			depRow(2, "reproduction", 8, r.ibr, 9, digest(6), 10, "base:/machine/b", 11, 0), "23505"},
		{"reproduction dependency without its occurrence", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.frv1, 9, digest(6), 10, "registries:/machine"), "dependency_shape"},
		{"reproduction occurrence -1", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.frv1, 9, digest(6), 10, "registries:/machine", 11, -1), "dependency_occurrence_check"},
		{"effective dependency with an occurrence", withMachine, insertDependency, depRow(11, 0), "dependency_shape"},
		{"encryption dependency at another key version than the ciphertext's", withMachine, insertDependency,
			depRow(2, "encryption", 3, "transit", 4, "bw-artifact", 5, 2, 6, "2026-10-02T00:00:00Z", 7, nil), "23503"},
		// Compilation §9: the encryption dependency names the key that encrypted the artifact, at its
		// version, not another retained key at the same version.
		{"encryption dependency on another key than the machine's", withMachine, insertDependency,
			depRow(2, "encryption", 3, "transit", 4, "bw-other", 6, "2026-09-26T09:12:40Z", 7, nil), "23503"},
		{"machine without its key name", withRelease, insertReleaseMachine, machineRow(11, nil), "23502"},
		// Compilation §9: an effective dependency is a reference occurrence of the machine's sources
		// that reached the artifact, so it has a reproduction row at the same version and creation,
		// whose source declares that reference at that version and generation; checked at commit.
		{"effective dependency without its reproduction occurrence", append(withSources, stmt{insertDependency, depRow()}),
			commit, nil, "dependency_effective"},
		{"effective dependency of another reference than its occurrence's", append(withSources,
			stmt{insertDependency, depRow(7, "registry/other")}, reproduction(kv, 1, created, r.frv1, 0)), commit, nil,
			"dependency_effective"},
		{"effective dependency at another creation than its occurrence's", append(withSources,
			stmt{insertDependency, depRow(6, recreated)}, reproduction(kv, 1, created, r.frv1, 0)), commit, nil,
			"dependency_effective"},
		{"effective dependency at another version than its occurrence's", append(withSources,
			stmt{insertDependency, depRow(5, 7)}, reproduction(kv, 1, created, r.frv1, 0)), commit, nil, "dependency_effective"},
		{"effective dependency whose occurrence declares another version", append(withSources,
			stmt{insertDependency, depRow(5, 7)}, reproduction(kv, 7, created, r.frv1, 0)), commit, nil, "dependency_effective"},
		{"effective dependency whose occurrence declares another generation", append(withSources,
			stmt{insertDependency, depRow(4, kvOther)}, reproduction(kvOther, 1, created, r.ibr, 0)), commit, nil,
			"dependency_effective"},
		{"effective dependency whose import base occurrence declares another version", append(withSources,
			stmt{insertDependency, depRow(5, 7)}, reproduction(kv, 7, created, r.ibr, 0)), commit, nil, "dependency_effective"},
		{"effective dependency whose fragment occurrence declares another generation", append(withSources,
			stmt{insertDependency, depRow(4, kvOther)}, reproduction(kvOther, 1, created, r.frv1, 0)), commit, nil,
			"dependency_effective"},
		{"effective dependency at another generation than its occurrence's", append(withSources,
			stmt{insertDependency, depRow(4, kvOther)}, reproduction(kv, 1, created, r.frv1, 0)), commit, nil,
			"dependency_effective"},
		{"effective dependency whose occurrence's reference its source does not declare", append(withSources,
			stmt{insertDependency, depRow(7, "registry/undeclared")}, named("registry/undeclared", reproduction(kv, 1, created, r.frv1, 0))),
			commit, nil, "dependency_effective"},
		// Each source's own declarations, not another's.
		{"effective dependency whose fragment occurrence its revision does not declare", append(withSources,
			stmt{insertDependency, depRow()}, reproduction(kv, 1, created, r.frvSite, 0)), commit, nil, "dependency_effective"},
		{"effective dependency whose import base occurrence only another import base declares", append(withSources,
			stmt{insertDependency, depRow(7, "registry/base-only")}, named("registry/base-only", reproduction(kv, 1, created, r.ibr, 0))),
			commit, nil, "dependency_effective"},
		{"effective dependency whose import base occurrence only a fragment revision declares", append(withSources,
			stmt{insertDependency, depRow(7, "registry/fragment-only")}, named("registry/fragment-only", reproduction(kv, 1, created, r.ibr, 0))),
			commit, nil, "dependency_effective"},
		// The occurrence's source is in the machine's composition, not only somewhere in the release.
		{"effective dependency whose fragment occurrence its machine does not compose", slices.Concat(withSources,
			[]stmt{extrasSource}, fragmentOnly), commit, nil, "dependency_effective"},
		{"effective dependency whose fragment occurrence only a profile its assignment does not select pins",
			slices.Concat(withExtras, fragmentOnly), commit, nil, "dependency_effective"},
		{"effective dependency whose fragment occurrence only another machine's assignment selects",
			slices.Concat(withMachine2, fragmentOnly), commit, nil, "dependency_effective"},
		{"control: effective dependency with the occurrence of a fragment its assignment selects and pins",
			slices.Concat(withMachine2, []stmt{{insertDependency, depRow(1, machine2, 7, "registry/fragment-only")},
				on(machine2, named("registry/fragment-only", reproduction(kv, 1, created, frvExtra, 0)))}), commit, nil, ""},
		{"effective dependency whose fragment occurrence only another release's profile revision pins", slices.Concat(withMachine,
			[]stmt{assignmentSource, source("profile", r.prf, "workers", prvSiteOnly), pinnedSource, siteSource, withEncryption,
				{insertDependency, depRow()}, reproduction(kv, 1, created, r.frv1, 0)}), commit, nil, "dependency_effective"},
		{"control: effective dependency with the occurrence of a fragment its assignment selects", append(withSite2,
			stmt{insertDependency, depRow(7, "registry/site-only")}, named("registry/site-only", reproduction(kv, 1, created, frvSite2, 0))),
			commit, nil, ""},
		{"effective dependency whose only occurrence is another machine's", append(withSources,
			stmt{insertReleaseMachine, machineRow(2, machine2, 3, ibr2, 4, nil)},
			stmt{insertDependency, depRow(1, machine2, 2, "encryption", 3, "transit", 4, "bw-artifact", 6, "2026-09-26T09:12:40Z", 7, nil)},
			stmt{insertDependency, depRow()}, on(machine2, reproduction(kv, 1, created, ibr2, 0))), commit, nil,
			"dependency_effective"},
		{"control: effective dependency with its import base occurrence", append(withSources,
			stmt{insertDependency, depRow()}, reproduction(kv, 1, created, r.ibr, 0)), commit, nil, ""},
		// A reproduction source is the machine's import base or a fragment revision of the release.
		{"reproduction source at another machine's import base", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.otherIBR, 9, digest(6), 10, "base:/machine", 11, 0), "23503"},
		{"reproduction source at no import base", withMachine, insertDependency,
			depRow(2, "reproduction", 8, id.New(id.ImportBase), 9, digest(6), 10, "base:/machine", 11, 0), "23503"},
		{"reproduction source at a fragment revision the release does not name", withMachine, insertDependency,
			depRow(2, "reproduction", 8, r.frv1, 9, digest(6), 10, "registries:/machine", 11, 0), "23503"},
		// dependency_status
		{"second status of one version", nil, insertStatus, []any{id.New(id.Dependency), "kv", kv, 1, "retained", nil, nil, created}, "23505"},
		{"status of an unknown class", nil, insertStatus, []any{id.New(id.Dependency), "kv", kv, 2, "fine", "absent", nil, created}, "dependency_status_reason"},
		{"status of an unknown provider", nil, insertStatus, []any{id.New(id.Dependency), "s3", kv, 2, "retained", nil, nil, created}, "dependency_status_provider_check"},
		{"unknown status without its start", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "unknown", "unreachable", nil, created}, "dependency_status_unknown_since"},
		{"retained status with an unknown start", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", nil, "2026-09-26T09:12:40Z", created}, "dependency_status_unknown_since"},
		{"status reason in capitals", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "blocked", "Soft-Deleted", nil, created}, "dependency_status_reason"},
		// Dependency monitor §3's table is the whole set of provider, class and reason.
		{"lost status for a refused request", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "lost", "denied", nil, created}, "dependency_status_reason"},
		{"KV status trimmed", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "lost", "trimmed", nil, created}, "dependency_status_reason"},
		{"Transit status destroyed", nil, insertStatus,
			[]any{id.New(id.Dependency), "transit", "bw-artifact", 3, "lost", "destroyed", nil, created}, "dependency_status_reason"},
		{"Transit status soft-deleted", nil, insertStatus,
			[]any{id.New(id.Dependency), "transit", "bw-artifact", 3, "blocked", "soft-deleted", nil, created}, "dependency_status_reason"},
		{"KV status below the decryption floor", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "blocked", "below-decryption-floor", nil, created}, "dependency_status_reason"},
		{"blocked status for an absent version", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "blocked", "absent", nil, created}, "dependency_status_reason"},
		{"unknown status without a reason", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "unknown", nil, "2026-09-26T09:12:40Z", created}, "dependency_status_reason"},
		{"unknown status of a reason no row gives", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "unknown", "flaky", "2026-09-26T09:12:40Z", created}, "dependency_status_reason"},
		{"Transit status of an undecidable deletion time", nil, insertStatus, []any{id.New(id.Dependency), "transit", "bw-artifact", 3,
			"unknown", "deletion-time-undecidable", "2026-09-26T09:12:40Z", created}, "dependency_status_reason"},
		{"KV status of an unobserved soft deletion", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "unknown", "soft-delete-unobserved", "2026-09-26T09:12:40Z", created}, "dependency_status_reason"},
		{"retained status with a reason", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", "absent", nil, created}, "dependency_status_reason"},
		{"blocked status without a reason", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "blocked", nil, nil, created}, "dependency_status_reason"},
		// Dependency monitor §3: a future deletion_time is retained, deletion-scheduled, naming the time.
		{"blocked status with a scheduled deletion", nil, insertScheduled,
			[]any{id.New(id.Dependency), "kv", kv, 2, "blocked", "deletion-scheduled", "2026-10-09T00:00:00Z", created}, "dependency_status_reason"},
		{"scheduled deletion without its time", nil, insertScheduled,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", "deletion-scheduled", nil, created}, "dependency_status_schedule"},
		{"Transit status with a scheduled deletion", nil, insertScheduled, []any{id.New(id.Dependency), "transit", "bw-artifact", 2,
			"retained", "deletion-scheduled", "2026-10-09T00:00:00Z", created}, "dependency_status_reason"},
		{"KV status at a key name", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", "bw-artifact", 1, "retained", nil, nil, created}, "dependency_status_object"},
		{"Transit status at a path", nil, insertStatus,
			[]any{id.New(id.Dependency), "transit", "transit/bw-artifact", 1, "retained", nil, nil, created}, "dependency_status_object"},
		{"Transit status at ..", nil, insertStatus,
			[]any{id.New(id.Dependency), "transit", "..", 1, "retained", nil, nil, created}, "dependency_status_object"},
		{"status at version 0", nil, insertStatus, []any{id.New(id.Dependency), "kv", kv, 0, "retained", nil, nil, created}, "dependency_status_version_check"},
		{"status with an id of another kind", nil, insertStatus, []any{id.New(id.Release), "kv", kv, 2, "retained", nil, nil, created}, "dependency_status_id_check"},
		{"status creation time without its zone", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", nil, nil, "2026-09-26T09:12:40"}, "dependency_status_created_check"},
		// Dependency monitor §5.2, §6.1: a class is recorded after the request that observed it began.
		{"status recorded before it was observed", nil, insertStatusAt,
			[]any{id.New(id.Dependency), kv, 9, "2026-09-26T09:12:41Z", "2026-09-26T09:12:40.999999Z", created}, "dependency_status_times"},
		{"control: status recorded when it was observed", nil, insertStatusAt,
			[]any{id.New(id.Dependency), kv, 9, "2026-09-26T09:12:41Z", "2026-09-26T09:12:41Z", created}, ""},
		// draft, machine_state, operation
		{"published draft without its release", nil,
			`UPDATE draft SET state = 'published' WHERE id = $1`, []any{r.draft2}, "draft_published_release"},
		{"open draft naming a release", nil, `UPDATE draft SET release = $2 WHERE id = $1`, []any{r.draft2, r.rel}, "draft_published_release"},
		{"draft published as another draft's release", nil,
			`UPDATE draft SET state = 'published', release = $2 WHERE id = $1`, []any{r.draft2, r.rel}, "23503"},
		{"desired release that does not cover the machine", nil,
			`INSERT INTO machine_state (machine, desired) VALUES ($1, $2)`, []any{r.otherMachine, r.rel}, "23503"},
		{"applied release that does not exist", nil,
			`UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation', baseline_revision = 1
			 WHERE machine = $1`, []any{r.machine, id.New(id.Release), digest(5)}, "23503"},
		{"second queued publish of one draft revision", []stmt{{`UPDATE operation SET state = 'queued', owner = NULL,
			owner_epoch = NULL, lease_until = NULL WHERE id = $1`, []any{op2}}}, insertOperation,
			[]any{id.New(id.Operation), "publish", "queued", nil, 0, r.draft2, 1, nil, r.human, nil, nil}, "23505"},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			for _, p := range c.pre {
				mustExec(t, tx, p.q, p.args...)
			}
			if _, err := tx.Exec(c.q, c.args...); refusal(err) != c.want {
				t.Errorf("%s: %v; got %q, want %q", c.name, err, refusal(err), c.want)
			}
		}()
	}
	// Controls: the same shapes commit with valid values, on a second draft revision.
	mustExec(t, db, insertStatus, id.New(id.Dependency), "kv", kv, 2, "unknown", "unreachable", "2026-09-26T09:12:40Z", created)
	mustExec(t, db, insertStatus, id.New(id.Dependency), "kv", kv, 3, "blocked", "soft-deleted", nil, created)
	mustExec(t, db, insertScheduled, id.New(id.Dependency), "kv", kv, 4, "retained", "deletion-scheduled", "2026-10-09T00:00:00Z", created)
	// A version recreated under its object and number is another identity, with its own status.
	mustExec(t, db, insertStatus, id.New(id.Dependency), "kv", kv, 1, "retained", nil, nil, "2026-09-26T09:12:40.1Z")
	mustExec(t, db, insertStatus, id.New(id.Dependency), "transit", "bw-artifact", 1, "retained", nil, nil, "2026-10-01T00:00:00Z")
	// Each provider, class and reason of dependency monitor §3's table.
	for i, s := range []struct{ provider, class, reasons string }{
		{"kv", "unknown", "malformed denied absent unavailable unreachable unreadable insufficient-evidence identity-mismatch " +
			"deletion-time-undecidable"},
		{"transit", "unknown", "malformed denied absent unavailable unreachable unreadable identity-mismatch " +
			"soft-delete-unobserved insufficient-evidence trimmed-unverified below-decryption-floor-unverified"},
		{"kv", "lost", "destroyed pruned"},
		{"transit", "lost", "trimmed"},
		{"kv", "blocked", "soft-deleted"},
		{"transit", "blocked", "below-decryption-floor"},
	} {
		object := map[string]string{"kv": kv, "transit": "bw-artifact"}[s.provider]
		for j, reason := range strings.Fields(s.reasons) {
			var since any
			if s.class == "unknown" {
				since = "2026-09-26T09:12:40Z"
			}
			mustExec(t, db, insertStatus, id.New(id.Dependency), s.provider, object, 100*(i+1)+j, s.class, reason, since, created)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, p := range append(withMachine, assignmentSource, profileSource, pinnedSource, siteSource) {
		mustExec(t, tx, p.q, p.args...)
	}
	mustExec(t, tx, insertDependency, depRow(6, "2026-09-26T09:12:40.1Z")...)
	p := reproduction(kv, 1, "2026-09-26T09:12:40.1Z", r.frv1, 1)
	mustExec(t, tx, p.q, p.args...)
	mustExec(t, tx, insertDependency, depRow(2, "reproduction", 8, r.frv1, 9, digest(6), 10, "registries:/machine", 11, 0)...)
	// Two occurrences shown at one redacted path are two rows, told apart by their ordinals.
	mustExec(t, tx, insertDependency, depRow(2, "reproduction", 8, r.ibr, 9, digest(6), 10, "base:/machine/<redacted>", 11, 0)...)
	mustExec(t, tx, insertDependency, depRow(2, "reproduction", 8, r.ibr, 9, digest(6), 10, "base:/machine/<redacted>", 11, 1)...)
	// A path has no length bound beyond its document's (compilation §9).
	mustExec(t, tx, insertDependency, depRow(2, "reproduction", 8, r.ibr, 9, digest(6), 10,
		"base:/machine/"+strings.Repeat("a", 5000), 11, 2)...)
	mustExec(t, tx, insertDependency, depRow(2, "encryption", 3, "transit", 4, "bw-artifact", 6, "2026-10-01T00:00:00Z", 7, nil)...)
	// A publish that failed leaves the draft revision free for the next.
	mustExec(t, tx, `UPDATE operation SET state = 'failed', error = '{"code": "conflict"}' WHERE id = $1`, op2)
	mustExec(t, tx, insertOperation, id.New(id.Operation), "publish", "queued", nil, 0, r.draft2, 1, nil, r.human, nil, nil)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// The baseline revision is a counter (execution and recovery §2): an adoption record sets
	// Applied at baseline revision 1, and a completed operation's new Applied advances it to 2,
	// though no import base revision is named by either.
	mustExec(t, db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, r.machine, r.rel, digest(3))
	mustExec(t, db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation',
		baseline_revision = baseline_revision + 1 WHERE machine = $1`, r.machine, rel2, digest(4))
	if n := count(t, db, "machine_state WHERE baseline_revision = 2"); n != 1 {
		t.Fatalf("%d machine states at baseline revision 2; want the advanced one", n)
	}
}

// The control for the release tables' named checks: with each dropped, the row TestReleaseConstraints expects it
// to refuse commits, so it is that check, not another, that refuses.
func TestReleaseConstraintControl(t *testing.T) {
	db, _ := installed(t)
	r := releaseRows(t, db)
	kv := generation(r.cluster, r.claim)
	rel2, op2, op3 := id.New(id.Release), id.New(id.Operation), id.New(id.Operation)
	mustExec(t, db, insertOperation, op2, "publish", "running", "run-1/4242/publish-2", 1, r.draft2, 1, nil, r.human, nil, nil)
	withRelease := stmt{insertRelease, []any{rel2, r.cluster, r.draft2, 1, digest(3), "v1.13", machinery, checksum,
		"v1.36.0", op2, r.human, "publisher"}}
	withMachine := stmt{insertReleaseMachine, []any{rel2, r.cluster, r.machine, r.ibr, r.asr, "metal", cipher, digest(4),
		digest(5), "x: 1\n", `[]`, "bw-artifact"}}
	for _, c := range []struct {
		drop string
		pre  []stmt
		q    string
		args []any
	}{
		{"ALTER TABLE release_source DROP CONSTRAINT release_source_shape", []stmt{withRelease}, insertReleaseSource,
			[]any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", r.machine, r.frv1, nil, nil, 1}},
		{"ALTER TABLE dependency DROP CONSTRAINT dependency_shape", []stmt{withRelease, withMachine}, insertDependency,
			[]any{rel2, r.machine, "encryption", "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z", "registry/example-pass",
				nil, nil, nil, nil}},
		{"ALTER TABLE release_source DROP CONSTRAINT release_source_kind_check", []stmt{withRelease}, insertReleaseSource,
			[]any{rel2, r.cluster, "import-base", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 1}},
		{"ALTER TABLE dependency DROP CONSTRAINT dependency_kind_check", []stmt{withRelease, withMachine}, insertDependency,
			[]any{rel2, r.machine, "source", "kv", kv, 1, created, "registry/example-pass", nil, nil, nil, nil}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_reason", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "fine", "absent", nil, created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_provider_check", nil, insertStatus,
			[]any{id.New(id.Dependency), "s3", kv, 2, "retained", nil, nil, created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_object", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", "bw-artifact", 1, "retained", nil, nil, created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_reason", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", "absent", nil, created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_schedule", nil, insertScheduled,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", "deletion-scheduled", nil, created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_reason", nil, insertScheduled,
			[]any{id.New(id.Dependency), "transit", "bw-artifact", 2, "retained", "deletion-scheduled", "2026-10-09T00:00:00Z", created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_reason", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "blocked", "Soft-Deleted", nil, created}},
		{"ALTER TABLE release_machine DROP CONSTRAINT release_machine_key_version", []stmt{withRelease}, insertReleaseMachine,
			[]any{rel2, r.cluster, r.machine, r.ibr, r.asr, "metal", "vault:v9223372036854775808:YWJj", digest(4), digest(5), "x: 1\n", `[]`,
				"bw-artifact"}},
		{"ALTER TABLE release_machine DROP CONSTRAINT release_machine_assignment_source", []stmt{withRelease, withMachine,
			{insertDependency, []any{rel2, r.machine, "encryption", "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z", nil, nil, nil, nil, nil}}},
			`SET CONSTRAINTS ALL IMMEDIATE`, nil},
		{"DROP TRIGGER selection ON release_source", []stmt{withRelease, {insertReleaseSource,
			[]any{rel2, r.cluster, "profile", nil, r.prf, nil, "workers", nil, nil, r.prv, nil, 1}}}, `SET CONSTRAINTS ALL IMMEDIATE`, nil},
		{"DROP TRIGGER selection ON release_source", []stmt{withRelease, {insertReleaseSource,
			[]any{rel2, r.cluster, "assignment", nil, nil, r.asg, nil, r.machine, nil, nil, r.asr, 1}}}, `SET CONSTRAINTS ALL IMMEDIATE`, nil},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_unknown_since", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "unknown", "unreachable", nil, created}},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_created_check", nil, insertStatus,
			[]any{id.New(id.Dependency), "kv", kv, 2, "retained", nil, nil, "2026-09-26T09:12:40"}},
		{"ALTER TABLE release DROP CONSTRAINT release_operation", []stmt{{insertOperation, []any{op3, "publish", "queued", nil, 0,
			r.draft, 1, nil, r.human, nil, nil}}}, insertRelease, []any{rel2, r.cluster, r.draft2, 1, digest(3), "v1.13", machinery,
			checksum, "v1.36.0", op3, r.human, "publisher"}},
		{"DROP TRIGGER encryption ON release_machine", []stmt{withRelease, withMachine,
			{insertReleaseSource, []any{rel2, r.cluster, "assignment", nil, nil, r.asg, nil, r.machine, nil, nil, r.asr, 1}},
			{insertReleaseSource, []any{rel2, r.cluster, "profile", nil, r.prf, nil, "workers", nil, nil, r.prv, nil, 1}},
			{insertReleaseSource, []any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 1}},
			{insertReleaseSource, []any{rel2, r.cluster, "fragment", r.frgSite, nil, nil, "site-dns", nil, r.frvSite, nil, nil, 1}}},
			`SET CONSTRAINTS ALL IMMEDIATE`, nil},
		{"ALTER TABLE dependency_status DROP CONSTRAINT dependency_status_times", nil, insertStatusAt,
			[]any{id.New(id.Dependency), kv, 9, "2026-09-26T09:12:41Z", "2026-09-26T09:12:40.999999Z", created}},
		{"ALTER TABLE dependency DROP CONSTRAINT dependency_encryption_key", []stmt{withRelease, withMachine,
			{insertStatus, []any{id.New(id.Dependency), "transit", "bw-other", 1, "retained", nil, nil, "2026-09-26T09:12:40Z"}}},
			insertDependency, []any{rel2, r.machine, "encryption", "transit", "bw-other", 1, "2026-09-26T09:12:40Z", nil, nil, nil, nil, nil}},
		{"DROP TRIGGER effective ON dependency", []stmt{withRelease, withMachine,
			{insertReleaseSource, []any{rel2, r.cluster, "assignment", nil, nil, r.asg, nil, r.machine, nil, nil, r.asr, 1}},
			{insertReleaseSource, []any{rel2, r.cluster, "profile", nil, r.prf, nil, "workers", nil, nil, r.prv, nil, 1}},
			{insertReleaseSource, []any{rel2, r.cluster, "fragment", r.frg, nil, nil, "registries", nil, r.frv1, nil, nil, 1}},
			{insertReleaseSource, []any{rel2, r.cluster, "fragment", r.frgSite, nil, nil, "site-dns", nil, r.frvSite, nil, nil, 1}},
			{insertDependency, []any{rel2, r.machine, "encryption", "transit", "bw-artifact", 1, "2026-09-26T09:12:40Z", nil, nil, nil, nil, nil}},
			{insertDependency, []any{rel2, r.machine, "effective", "kv", kv, 1, created, "registry/example-pass", nil, nil, nil, nil}}},
			`SET CONSTRAINTS ALL IMMEDIATE`, nil},
		{"ALTER TABLE draft DROP CONSTRAINT draft_published_release", nil,
			`UPDATE draft SET state = 'published' WHERE id = $1`, []any{r.draft2}},
		{"DROP INDEX operation_active_publish", []stmt{{`UPDATE operation SET state = 'queued', owner = NULL, owner_epoch = NULL,
			lease_until = NULL WHERE id = $1`, []any{op2}}}, insertOperation,
			[]any{id.New(id.Operation), "publish", "queued", nil, 0, r.draft2, 1, nil, r.human, nil, nil}},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(c.drop); err != nil {
				t.Fatal(err)
			}
			for _, p := range c.pre {
				mustExec(t, tx, p.q, p.args...)
			}
			if _, err := tx.Exec(c.q, c.args...); err != nil {
				t.Errorf("after %s: %v; want it to commit", c.drop, err)
			}
		}()
	}
}

// PA §3, choice §17.3: a release and its rows are immutable, and its rows are written in the
// release's own transaction, so a committed release cannot grow; a dependency status is mutable.
func TestReleaseImmutableTables(t *testing.T) {
	db, _ := installed(t)
	r := releaseRows(t, db)
	for table, column := range map[string]string{"release": "digest", "release_machine": "redacted",
		"release_source": "head_revision", "dependency": "version"} {
		for _, stmt := range []string{"UPDATE " + table + " SET " + column + " = " + column, "DELETE FROM " + table,
			"TRUNCATE " + table + " CASCADE"} {
			if _, err := db.Exec(stmt); sqlState(err) != ImmutableSQLState {
				t.Errorf("%s: %v; want SQLSTATE %s", stmt, err, ImmutableSQLState)
			}
		}
		if count(t, db, table) == 0 {
			t.Errorf("%s: a refused statement removed its rows", table)
		}
	}
	for _, c := range []stmt{
		{insertReleaseMachine, []any{r.rel, r.cluster, r.machine, r.ibr, r.asr, "metal", cipher, digest(4), digest(5), "x: 1\n", `[]`,
			"bw-artifact"}},
		{insertReleaseSource, []any{r.rel, r.cluster, "fragment", nil, nil, nil, "site-dns", nil, r.frvSite, nil, nil, 1}},
		{insertDependency, []any{r.rel, r.machine, "effective", "kv", generation(r.cluster, r.claim), 1, created,
			"registry/late", nil, nil, nil, nil}},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != ImmutableSQLState {
			t.Errorf("late row %s: %v; want SQLSTATE %s", c.q, err, ImmutableSQLState)
		}
	}
	if _, err := db.Exec(insertReleaseSource, id.New(id.Release), r.cluster, "fragment", r.frg, nil, nil, "registries", nil,
		r.frv1, nil, nil, 1); sqlState(err) != "23503" {
		t.Errorf("source of no release: %v; want SQLSTATE 23503", err)
	}
	if _, err := db.Exec(`UPDATE dependency_status SET class = 'unknown', reason = 'unreachable', unknown_since = now(),
		recorded_at = now() WHERE id = $1`, r.depKV); err != nil {
		t.Errorf("dependency_status update: %v; it is mutable", err)
	}
}

// Dependency monitor §6.1 step 5 reads the releases that reference a dependency by its status; the
// lookup searches the index on the status identity with all four columns, not a scan of every
// release's rows or of the whole index. A table of a few rows plans a sequential scan whatever its
// indexes, so the scan is disabled; with the index dropped, no plan node searches it.
func TestDependencyStatusIndex(t *testing.T) {
	db, _ := installed(t)
	r := releaseRows(t, db)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `SET LOCAL enable_seqscan = off`)
	type node struct {
		Relation  string `json:"Relation Name"`
		Index     string `json:"Index Name"`
		Condition string `json:"Index Cond"`
		Plans     []node `json:"Plans"`
	}
	// searched reports whether a node scans dependency by the index with an equality on each
	// identity column as its index condition, not as a filter, and returns the plan to show.
	searched := func() (bool, string) {
		t.Helper()
		var text string
		if err := tx.QueryRow(`EXPLAIN (FORMAT JSON) SELECT DISTINCT d.release FROM dependency d
			JOIN dependency_status s USING (provider, object, version, created) WHERE s.id = '` + r.depKV + `'`).Scan(&text); err != nil {
			t.Fatal(err)
		}
		var plans []struct {
			Plan node `json:"Plan"`
		}
		if err := json.Unmarshal([]byte(text), &plans); err != nil || len(plans) != 1 {
			t.Fatalf("plan %q: %v", text, err)
		}
		var walk func(n node) bool
		walk = func(n node) bool {
			if n.Relation == "dependency" && n.Index == "dependency_status_identity" {
				all := true
				for _, column := range []string{"provider", "object", "version", "created"} {
					all = all && regexp.MustCompile(`\(`+column+` = `).MatchString(n.Condition)
				}
				if all {
					return true
				}
			}
			for _, c := range n.Plans {
				if walk(c) {
					return true
				}
			}
			return false
		}
		return walk(plans[0].Plan), text
	}
	if ok, p := searched(); !ok {
		t.Errorf("the lookup does not search the status identity index by all four columns:\n%s", p)
	}
	mustExec(t, tx, `DROP INDEX dependency_status_identity`)
	if ok, p := searched(); ok {
		t.Errorf("without the index, a plan node still searches it:\n%s", p)
	}
}

// refusal names a check refusal by its constraint, so a case refused by another check than the one
// it targets fails, and so does release_machine_platform, the foreign key whose case shares its row
// with others; any other refusal is its SQLSTATE alone.
func refusal(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && (pe.Code == "23514" || pe.Code == "23503" && pe.ConstraintName == "release_machine_platform") {
		return pe.ConstraintName
	}
	return sqlState(err)
}

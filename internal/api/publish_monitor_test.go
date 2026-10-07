package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/monitor"
	"github.com/ginsys/bronzeward/internal/provider"
)

// lossMeta is the metadata identity's provider once the fixture's KV version is destroyed: its
// Transit key is unchanged.
type lossMeta struct{}

func (lossMeta) KV(context.Context, provider.GenerationPath) (classify.Answer, error) {
	return metaAnswer(map[string]any{"current_version": 1, "oldest_version": 0, "versions": map[string]any{
		"1": map[string]any{"created_time": kvCreated.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": true}}}), nil
}

func (lossMeta) Transit(context.Context, string) (classify.Answer, error) {
	return metaAnswer(map[string]any{"keys": map[string]any{"1": transitCreated.Unix()}, "latest_version": 1,
		"min_available_version": 0, "min_decryption_version": 1, "soft_deleted": false}), nil
}

func metaAnswer(data map[string]any) classify.Answer {
	b, _ := json.Marshal(map[string]any{"data": data})
	return classify.Answer{Status: http.StatusOK, Date: time.Now().UTC().Format(http.TimeFormat), Body: classify.NewBody(b)}
}

// lockWaits counts the sessions of this database waiting on a lock.
func lockWaits(t *testing.T, db *sql.DB) int {
	return count(t, db, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`)
}

// trimMeta is the metadata identity's provider once the Transit key's version 1 is trimmed: every
// KV version is unchanged.
type trimMeta struct{}

func (trimMeta) KV(context.Context, provider.GenerationPath) (classify.Answer, error) {
	return metaAnswer(map[string]any{"current_version": 1, "oldest_version": 0, "versions": map[string]any{
		"1": map[string]any{"created_time": kvCreated.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": false}}}), nil
}

func (trimMeta) Transit(context.Context, string) (classify.Answer, error) {
	return metaAnswer(map[string]any{"keys": map[string]any{"1": transitCreated.Unix(), "2": transitCreated.Unix() + 60},
		"latest_version": 2, "min_available_version": 2, "min_decryption_version": 2, "soft_deleted": false}), nil
}

// lostAlerts is the lost alerts of the KV version, each with the releases it names.
func lostAlerts(t *testing.T, p *publishEnv) [][]string {
	t.Helper()
	return alerts(t, p.db, "kv", p.kvPath)
}

// alerts is the lost alerts of a provider's object, each with the releases it names.
func alerts(t *testing.T, db *sql.DB, prov, object string) [][]string {
	t.Helper()
	rows, err := db.Query(`SELECT array_to_string(releases, ',') FROM dependency_alert
		WHERE kind = 'lost' AND provider = $1 AND object = $2 ORDER BY seq`, prov, object)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Split(s, ","))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// skipTransit holds the Transit status's dependency lock until the test ends, so a pass skips it
// (dependency monitor §6.1 step 1) and records the KV status alone: a pass visits statuses in
// identifier order, which is random, and Transit's record would otherwise wait first.
func skipTransit(t *testing.T, p *publishEnv) {
	t.Helper()
	conn, err := p.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock(hashtextextended(id, 0))
		FROM dependency_status WHERE provider = 'transit' AND object = 'bw-artifact'`); err != nil {
		t.Fatal(err)
	}
}

// beginNow dates publication's classification of every named version now.
func (p *publishEnv) beginNow() {
	now := time.Now()
	for i := range p.unit.statuses {
		p.unit.statuses[i].began = now
	}
}

// Dependency monitor §10.1 item 10, the row existing: a monitor pass recording the KV version's
// loss while a publication naming it holds its status FOR SHARE waits for the publication's
// commit, then names the new release in its lost alert. Control: the monitor reading the status
// without its row lock reads the releases before the publication commits, and the alert omits it.
// Without T3's FOR SHARE lock the monitor still waits here, since the release's dependency rows
// hold KEY SHARE on the status through their foreign key and FOR UPDATE waits for that too;
// TestPublishCommitHoldsStatuses checks that lock against the NO KEY UPDATE a bare UPDATE takes.
func TestPublishRacingMonitorNamedByAlert(t *testing.T) {
	p := newPublishEnv(t)
	p.nextDraft()
	p.beginNow()
	skipTransit(t, p)
	m := monitor.New(p.db, lossMeta{}, monitor.Defaults(), t.Logf, io.Discard)
	passed := make(chan error, 1)
	p.a = p.buildWith(deps{owner: p.owner}, options{commit: func(tx *sql.Tx) error {
		go func() { passed <- m.Pass(context.Background()) }()
		// The KV status is the one the pass records, so a wait is its record waiting on the held row.
		for deadline := time.Now().Add(10 * time.Second); lockWaits(t, p.db) == 0; time.Sleep(10 * time.Millisecond) {
			if len(passed) > 0 || time.Now().After(deadline) {
				t.Error("the monitor's record never waited for the publication")
				break
			}
		}
		return tx.Commit()
	}})
	rel, ref := p.commit()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	if err := <-passed; err != nil {
		t.Fatal(err)
	}
	got := lostAlerts(t, p)
	if len(got) != 1 || !slices.Contains(got[0], rel) {
		t.Fatalf("lost alerts %v, want one naming the release %s", got, rel)
	}
}

// otherCluster is a publication of a second cluster in p's database, its own machine and draft:
// it names p's Transit key version and its own KV version.
func (p *publishEnv) otherCluster() *publishEnv {
	p.t.Helper()
	e := p.env
	d := &draftEnv{env: e, seed: p.seed}
	d.cluster = e.createCluster(e.api, e.human("h-author"), "k-cluster-other-0123456")
	rec := e.do(e.api, call{method: "POST", path: prefix + "/machines", token: e.human("h-author"), key: "k-machine-other-0123456",
		body: `{"cluster":"` + d.cluster + `","smbiosUuid":"2d7c8e3f-4a5b-4c6d-8e9f-0a1b2c3d4e5f","serial":"SN-2",` +
			`"platform":"container","talosEndpoint":"10.55.0.4"}`})
	d.machine = decode[machineBody](p.t, rec, http.StatusCreated).ID
	rec = e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: e.human("h-author"), key: "k-draft-other-0123456",
		body: `{"cluster":"` + d.cluster + `","title":"workers"}`})
	d.draft, d.etag = decode[draftBody](p.t, rec, http.StatusCreated).ID, rec.Header().Get("ETag")
	return publishOn(p.t, d)
}

// Dependency monitor §10.1 item 10, two publications the first to name the version: two clusters'
// publications name one Transit key version, which only a key shared across clusters allows, since
// each publication locks every machine state of its cluster before its seed. The second one's seed
// waits for the first one's uncommitted status row; the first commits; the second re-checks, and
// while it holds the row the monitor records the version's loss, waits for its commit and names
// both releases. Control: the monitor reading the status without its row lock omits the second.
// The other outcome, the monitor recording between the first's commit and the second's re-check,
// has no gap to arrange: TestPublishFirstNamedConcurrentlyRefused stands in for it.
func TestPublishFirstNamedByTwoNamedByAlert(t *testing.T) {
	first := newPublishEnv(t)
	second := first.otherCluster()
	m := monitor.New(first.db, trimMeta{}, monitor.Defaults(), t.Logf, io.Discard)
	seeded, release := make(chan int, 1), make(chan struct{})
	first.a = first.buildWith(deps{owner: first.owner}, options{commit: func(tx *sql.Tx) error {
		var pid int
		if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return err
		}
		seeded <- pid
		<-release
		return tx.Commit()
	}})
	passed := make(chan error, 1)
	second.a = second.buildWith(deps{owner: second.owner}, options{commit: func(tx *sql.Tx) error {
		go func() { passed <- m.Pass(context.Background()) }()
		for deadline := time.Now().Add(10 * time.Second); lockWaits(t, second.db) == 0; time.Sleep(10 * time.Millisecond) {
			if len(passed) > 0 || time.Now().After(deadline) {
				t.Error("the monitor's record never waited for the second publication")
				break
			}
		}
		return tx.Commit()
	}})
	type outcome struct {
		rel string
		ref *refusal
		err error
	}
	commit := func(p *publishEnv) <-chan outcome {
		c := make(chan outcome, 1)
		go func() {
			rel, ref, err := p.a.publishCommit(context.Background(), p.job, p.unit)
			c <- outcome{rel, ref, err}
		}()
		return c
	}
	firstDone := commit(first)
	var holder int
	select {
	case holder = <-seeded:
	case o := <-firstDone:
		t.Fatalf("the first publication ended before its commit: %+v", o)
	}
	secondDone := commit(second)
	waitBlockedBy(t, first.db, holder)
	close(release)
	a, b := <-firstDone, <-secondDone
	if a.err != nil || a.ref != nil || b.err != nil || b.ref != nil {
		t.Fatalf("first %+v, second %+v; want both committed", a, b)
	}
	if err := <-passed; err != nil {
		t.Fatal(err)
	}
	got := alerts(t, first.db, "transit", "bw-artifact")
	if len(got) != 1 || !slices.Contains(got[0], a.rel) || !slices.Contains(got[0], b.rel) {
		t.Fatalf("lost alerts %v, want one naming both releases %s and %s", got, a.rel, b.rel)
	}
}

// Dependency monitor §10.1 item 10, two publications the first to name the version, refused: the
// other publication's seed of the version's status is uncommitted when this one's seed meets it,
// and by the time it commits the monitor has recorded the version's loss after this publication
// began. The held transaction stands for both, committing the status as the monitor left it: a
// publication's re-check follows its seed with no step between, so the monitor cannot be placed
// there. This publication's seed waits, finds the row, and its re-check, locking after the insert,
// refuses. Control: re-checking before the insert finds no row, and the release commits.
func TestPublishFirstNamedConcurrentlyRefused(t *testing.T) {
	p := newPublishEnv(t)
	p.beginNow()
	time.Sleep(20 * time.Millisecond)
	p.hold(`INSERT INTO dependency_status (id, provider, object, version, created, class, reason, observed_from, recorded_at)
		VALUES ($1, 'kv', $2, 1, $3, 'blocked', 'soft-deleted', clock_timestamp(), clock_timestamp())`,
		id.New(id.Dependency), p.kvPath, createdText(kvCreated))
	ref := p.refused(422, "validation-failed")
	if dep, _ := ref.extra["dependency"].(map[string]any); dep["object"] != p.kvPath {
		t.Fatalf("refusal names %v", ref.extra["dependency"])
	}
}

// Dependency monitor §10.1 item 10, the fourth order: the monitor's step 5 transaction begins and
// waits for the status row while publication begins classifying the version, then records the
// loss; publication, re-checking at its commit, is refused. Control: taking recorded_at from
// now(), the transition is dated before publication began, so the release commits and the alert,
// already committed, omits it.
func TestPublishAfterMonitorWaitedRefused(t *testing.T) {
	p := newPublishEnv(t)
	p.nextDraft()
	holder, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	mustExec(t, holder, `SELECT 1 FROM dependency_status WHERE provider = 'kv' AND object = $1 FOR UPDATE`, p.kvPath)
	m := monitor.New(p.db, lossMeta{}, monitor.Defaults(), t.Logf, io.Discard)
	passed := make(chan error, 1)
	go func() { passed <- m.Pass(context.Background()) }()
	for deadline := time.Now().Add(10 * time.Second); lockWaits(t, p.db) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the monitor never waited for the status row")
		}
	}
	time.Sleep(20 * time.Millisecond)
	p.beginNow()
	time.Sleep(20 * time.Millisecond)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-passed; err != nil {
		t.Fatal(err)
	}
	if n := len(lostAlerts(t, p)); n != 1 {
		t.Fatalf("%d lost alerts, want 1", n)
	}
	ref := p.refused(422, "validation-failed")
	if dep, _ := ref.extra["dependency"].(map[string]any); dep["object"] != p.kvPath {
		t.Fatalf("refusal names %v", ref.extra["dependency"])
	}
}

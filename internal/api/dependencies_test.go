package api

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// status seeds one Transit key version's dependency status, observed ago before the database's
// time; reason is empty for none. It returns the status's identifier.
func (d *draftEnv) status(key string, version int, class, reason string, ago time.Duration) string {
	d.t.Helper()
	dep := id.New(id.Dependency)
	mustExec(d.t, d.db, `INSERT INTO dependency_status (id, provider, object, version, created, class, reason, first_retained_at,
			unknown_since, observed_from, recorded_at)
		SELECT $1, 'transit', $2, $3, '2026-09-26T09:12:40Z', $4, NULLIF($5, ''),
			CASE WHEN $4 = 'retained' THEN t END, CASE WHEN $4 = 'unknown' THEN t END, t, t + interval '1 second'
		FROM (SELECT now() - make_interval(secs => $6) AS t) n`,
		dep, key, version, class, reason, ago.Seconds())
	return dep
}

// artifactStatus is the status of the encryption dependency each seeded release names.
func (d *draftEnv) artifactStatus() string {
	d.t.Helper()
	var dep string
	if err := d.db.QueryRow(`SELECT id FROM dependency_status WHERE provider = 'transit' AND object = 'bw-artifact'`).Scan(&dep); err != nil {
		d.t.Fatal(err)
	}
	return dep
}

// DM §7.2: any role reads each monitored dependency with its class, reason, first-seen-retained
// time, unknown_since, the time of its latest recorded classification and stale, in identifier
// order, optionally of one class; the collection carries the last completed pass's time. Only
// reference names are served, never a value.
func TestDependencyReads(t *testing.T) {
	d := newDraftEnv(t)
	d.release(d.draft, 1, releaseProvenance)
	art := d.artifactStatus()
	old := d.status("bw-old", 1, "unknown", "unreachable", time.Hour)
	other := d.status("bw-other", 2, "unknown", "denied", 0)
	gone := d.status("bw-gone", 3, "lost", "trimmed", 0)
	mustExec(t, d.db, `UPDATE dependency_monitor SET last_pass = '2026-10-07T10:00:00Z'`)

	list := decode[dependencyPage](t, d.get("/dependencies"), http.StatusOK)
	want := []string{art, old, other, gone}
	slices.Sort(want)
	if len(list.Items) != 4 || list.Next != "" || list.LastPass == nil ||
		list.LastPass.UTC().Format(time.RFC3339) != "2026-10-07T10:00:00Z" {
		t.Fatalf("dependencies %+v", list)
	}
	byID := map[string]dependencyBody{}
	for i, it := range list.Items {
		if it.ID != want[i] {
			t.Fatalf("item %d is %s; want %v", i, it.ID, want)
		}
		byID[it.ID] = it
	}
	a := byID[art]
	if a.Provider != "transit" || a.Object != "bw-artifact" || a.Version != 1 || a.Created != "2026-09-26T09:12:40Z" ||
		a.Class != "retained" || a.Reason != nil || a.FirstRetainedAt == nil || a.UnknownSince != nil || a.Stale ||
		a.RecordedAt.IsZero() || a.ObservedFrom.IsZero() {
		t.Fatalf("artifact status %+v", a)
	}
	o := byID[old]
	if o.Class != "unknown" || o.Reason == nil || *o.Reason != "unreachable" || o.UnknownSince == nil ||
		o.FirstRetainedAt != nil || !o.Stale || !o.RecordedAt.After(o.ObservedFrom) {
		t.Fatalf("old status %+v", o)
	}
	if g := byID[gone]; g.Class != "lost" || g.Reason == nil || *g.Reason != "trimmed" || g.Stale {
		t.Fatalf("lost status %+v", g)
	}

	// One class, paged: the cursor continues the filtered list.
	unknown := []string{old, other}
	slices.Sort(unknown)
	p1 := decode[dependencyPage](t, d.get("/dependencies?class=unknown&limit=1"), http.StatusOK)
	if len(p1.Items) != 1 || p1.Items[0].ID != unknown[0] || p1.Next == "" || p1.LastPass == nil {
		t.Fatalf("first unknown page %+v", p1)
	}
	p2 := decode[dependencyPage](t, d.get("/dependencies?class=unknown&limit=1&cursor="+p1.Next), http.StatusOK)
	if len(p2.Items) != 1 || p2.Items[0].ID != unknown[1] || p2.Next != "" {
		t.Fatalf("second unknown page %+v", p2)
	}
	if b := decode[dependencyPage](t, d.get("/dependencies?class=blocked"), http.StatusOK); len(b.Items) != 0 {
		t.Fatalf("blocked %+v", b)
	}

	// The item answers the same fields.
	one := decode[dependencyBody](t, d.get("/dependencies/"+old), http.StatusOK)
	if !reflect.DeepEqual(one, o) {
		t.Fatalf("item %+v; listed %+v", one, o)
	}

	// Before the first completed pass, the collection says so.
	mustExec(t, d.db, `UPDATE dependency_monitor SET last_pass = NULL`)
	if l := decode[dependencyPage](t, d.get("/dependencies"), http.StatusOK); l.LastPass != nil {
		t.Fatalf("no pass yet; got %+v", l.LastPass)
	}
	if body := d.get("/dependencies").Body.String(); !strings.Contains(body, `"lastPass":null`) {
		t.Fatalf("no lastPass member: %s", body)
	}

	for _, c := range []struct {
		path   string
		status int
		code   string
	}{
		{"/dependencies?class=stale", http.StatusBadRequest, "invalid-request"},
		{"/dependencies?class=unknown&class=lost", http.StatusBadRequest, "invalid-request"},
		{"/dependencies?state=lost", http.StatusBadRequest, "invalid-request"},
		{"/dependencies?cursor=" + makeCursor(epoch(t, d.db), d.draft), http.StatusBadRequest, "cursor-invalid"},
		{"/dependencies/" + id.New(id.Dependency), http.StatusNotFound, "not-found"},
		{"/dependencies/" + d.draft, http.StatusNotFound, "not-found"},
		{"/dependencies/" + old + "?class=unknown", http.StatusBadRequest, "invalid-request"},
	} {
		wantProblem(t, d.get(c.path), c.status, c.code)
	}
}

// DM §6.3, §10.1 item 12: a served class is stale when its own observed_from is more than three
// intervals before the database's time, whatever the last completed pass. Control: deriving stale
// from the last completed pass serves a seeded status as current while passes complete.
func TestDependencyStaleOwnObservation(t *testing.T) {
	d := newDraftEnv(t)
	// Ten seconds either side of three intervals (monitor.Defaults().Interval is a minute): the
	// read follows the seed within a second.
	threeIntervals := 3 * time.Minute
	fresh := d.status("bw-fresh", 1, "retained", "", threeIntervals-10*time.Second)
	seeded := d.status("bw-seeded", 1, "retained", "", threeIntervals+10*time.Second)
	mustExec(t, d.db, `UPDATE dependency_monitor SET last_pass = now()`)
	for dep, want := range map[string]bool{fresh: false, seeded: true} {
		if got := decode[dependencyBody](t, d.get("/dependencies/"+dep), http.StatusOK); got.Stale != want {
			t.Fatalf("%s stale %v; want %v", dep, got.Stale, want)
		}
	}
}

// DM §7.2: a dependency's releases in release order, each with the machines and kinds of the
// records naming it; an unknown dependency is 404, one no release names lists nothing.
func TestDependencyReleases(t *testing.T) {
	d := newDraftEnv(t)
	// Each release also names a KV version in two occurrences of d.machine's import base: the
	// artifact key's releases list only its own records, and the KV version's one record per
	// machine and kind.
	d.reproductions = true
	s1 := d.release(d.draft, 1, releaseProvenance)
	rec := d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"), key: "k-draft2-0123456789",
		body: `{"cluster":"` + d.cluster + `","title":"second"}`})
	s2 := d.release(decode[draftBody](t, rec, http.StatusCreated).ID, 1, releaseProvenance)
	art := d.artifactStatus()
	lone := d.status("bw-lone", 1, "retained", "", 0)
	var pass string
	if err := d.db.QueryRow(`SELECT id FROM dependency_status WHERE object = $1`, d.reproducedPass()).Scan(&pass); err != nil {
		t.Fatal(err)
	}
	rels := []string{s1.rel, s2.rel}
	slices.Sort(rels)
	kv := decode[listPage[dependencyReleaseBody]](t, d.get("/dependencies/"+pass+"/releases"), http.StatusOK)
	if len(kv.Items) != 2 || kv.Items[0].Release != rels[0] || kv.Items[1].Release != rels[1] {
		t.Fatalf("KV releases %+v; want %v", kv, rels)
	}
	for _, it := range kv.Items {
		if !slices.Equal(it.Records, []dependencyRecordBody{{Machine: d.machine, Kind: "reproduction"}}) {
			t.Fatalf("KV records %+v", it.Records)
		}
	}

	type seeded struct {
		rel      string
		machines []string
	}
	want := []seeded{{s1.rel, []string{d.machine, s1.machine2}}, {s2.rel, []string{d.machine, s2.machine2}}}
	slices.SortFunc(want, func(a, b seeded) int { return strings.Compare(a.rel, b.rel) })
	check := func(it dependencyReleaseBody, w seeded) {
		t.Helper()
		ms := slices.Clone(w.machines)
		slices.Sort(ms)
		if it.Release != w.rel || it.Cluster != d.cluster || it.PublishedAt.IsZero() || len(it.Records) != 2 {
			t.Fatalf("release %+v; want %s", it, w.rel)
		}
		for i, m := range ms {
			if it.Records[i] != (dependencyRecordBody{Machine: m, Kind: "encryption"}) {
				t.Fatalf("records %+v; want machines %v", it.Records, ms)
			}
		}
	}
	all := decode[listPage[dependencyReleaseBody]](t, d.get("/dependencies/"+art+"/releases"), http.StatusOK)
	if len(all.Items) != 2 || all.Next != "" {
		t.Fatalf("releases %+v", all)
	}
	for i := range want {
		check(all.Items[i], want[i])
	}
	p1 := decode[listPage[dependencyReleaseBody]](t, d.get("/dependencies/"+art+"/releases?limit=1"), http.StatusOK)
	if len(p1.Items) != 1 || p1.Next == "" {
		t.Fatalf("first page %+v", p1)
	}
	check(p1.Items[0], want[0])
	p2 := decode[listPage[dependencyReleaseBody]](t, d.get("/dependencies/"+art+"/releases?limit=1&cursor="+p1.Next), http.StatusOK)
	if len(p2.Items) != 1 || p2.Next != "" {
		t.Fatalf("second page %+v", p2)
	}
	check(p2.Items[0], want[1])

	if none := decode[listPage[dependencyReleaseBody]](t, d.get("/dependencies/"+lone+"/releases"), http.StatusOK); len(none.Items) != 0 {
		t.Fatalf("lone %+v", none)
	}
	wantProblem(t, d.get("/dependencies/"+id.New(id.Dependency)+"/releases"), http.StatusNotFound, "not-found")
	wantProblem(t, d.get("/dependencies/"+s1.rel+"/releases"), http.StatusNotFound, "not-found")
	wantProblem(t, d.get("/dependencies/"+art+"/releases?class=lost"), http.StatusBadRequest, "invalid-request")
}

// DM §7.2: alerts in recording order, all of them or one dependency's, with the class, reason and
// releases each recorded; a monitor-stalled alert concerns no dependency. A cursor names the last
// alert listed, never its sequence.
func TestDependencyAlerts(t *testing.T) {
	d := newDraftEnv(t)
	s := d.release(d.draft, 1, releaseProvenance)
	art := d.artifactStatus()
	other := d.status("bw-other", 1, "retained", "", 0)
	ep := epoch(t, d.db)
	// Identifiers in descending order, so recording order is not identifier order.
	ids := []string{id.New(id.DependencyAlert), id.New(id.DependencyAlert), id.New(id.DependencyAlert)}
	slices.Sort(ids)
	slices.Reverse(ids)
	first, stalled, last := ids[0], ids[1], ids[2]
	mustExec(t, d.db, `INSERT INTO dependency_alert (id, seq, kind, dependency, provider, object, version, created, class, reason,
			releases, observed_from, epoch, recorded_at)
		VALUES ($1, 0, 'regression', $2, 'transit', 'bw-artifact', 1, '2026-09-26T09:12:40Z', 'unknown', 'unreachable',
			ARRAY[$3], now() - interval '1 minute', $4, now())`, first, art, s.rel, ep)
	mustExec(t, d.db, `INSERT INTO dependency_alert (id, seq, kind, epoch, recorded_at) VALUES ($1, 0, 'monitor-stalled', $2, now())`,
		stalled, ep)
	mustExec(t, d.db, `INSERT INTO dependency_alert (id, seq, kind, dependency, provider, object, version, created, class, reason,
			releases, observed_from, answer_date, epoch, recorded_at)
		VALUES ($1, 0, 'persistent', $2, 'transit', 'bw-artifact', 1, '2026-09-26T09:12:40Z', 'unknown', 'unreachable',
			ARRAY[$3], now(), now(), $4, now())`, last, art, s.rel, ep)

	all := decode[listPage[alertBody]](t, d.get("/dependency-alerts"), http.StatusOK)
	if len(all.Items) != 3 || all.Next != "" || all.Items[0].ID != first || all.Items[1].ID != stalled || all.Items[2].ID != last {
		t.Fatalf("alerts %+v; want %s %s %s", all, first, stalled, last)
	}
	f := all.Items[0]
	if f.Kind != "regression" || f.Dependency == nil || *f.Dependency != art || f.Provider == nil || *f.Provider != "transit" ||
		f.Object == nil || *f.Object != "bw-artifact" || f.Version == nil || *f.Version != 1 ||
		f.Created == nil || *f.Created != "2026-09-26T09:12:40Z" || f.Class == nil || *f.Class != "unknown" ||
		f.Reason == nil || *f.Reason != "unreachable" || !slices.Equal(f.Releases, []string{s.rel}) || f.Deletion != nil ||
		f.ObservedFrom == nil || f.AnswerDate != nil || f.Epoch != ep || f.RecordedAt.IsZero() {
		t.Fatalf("regression alert %+v", f)
	}
	if st := all.Items[1]; st.Kind != "monitor-stalled" || st.Dependency != nil || st.Class != nil || st.Releases != nil ||
		st.ObservedFrom != nil || st.Epoch != ep {
		t.Fatalf("stalled alert %+v", st)
	}
	if l := all.Items[2]; l.AnswerDate == nil {
		t.Fatalf("persistent alert %+v", l)
	}

	p1 := decode[listPage[alertBody]](t, d.get("/dependency-alerts?limit=2"), http.StatusOK)
	if len(p1.Items) != 2 || p1.Items[1].ID != stalled || p1.Next == "" {
		t.Fatalf("first page %+v", p1)
	}
	if strings.Contains(p1.Next, "=") {
		t.Fatalf("cursor %q", p1.Next)
	}
	p2 := decode[listPage[alertBody]](t, d.get("/dependency-alerts?limit=2&cursor="+p1.Next), http.StatusOK)
	if len(p2.Items) != 1 || p2.Items[0].ID != last || p2.Next != "" {
		t.Fatalf("second page %+v", p2)
	}

	mine := decode[listPage[alertBody]](t, d.get("/dependencies/"+art+"/alerts"), http.StatusOK)
	if len(mine.Items) != 2 || mine.Items[0].ID != first || mine.Items[1].ID != last || mine.Next != "" {
		t.Fatalf("dependency alerts %+v", mine)
	}
	m1 := decode[listPage[alertBody]](t, d.get("/dependencies/"+art+"/alerts?limit=1"), http.StatusOK)
	if len(m1.Items) != 1 || m1.Items[0].ID != first || m1.Next == "" {
		t.Fatalf("first dependency page %+v", m1)
	}
	m2 := decode[listPage[alertBody]](t, d.get("/dependencies/"+art+"/alerts?limit=1&cursor="+m1.Next), http.StatusOK)
	if len(m2.Items) != 1 || m2.Items[0].ID != last || m2.Next != "" {
		t.Fatalf("second dependency page %+v", m2)
	}
	if none := decode[listPage[alertBody]](t, d.get("/dependencies/"+other+"/alerts"), http.StatusOK); len(none.Items) != 0 {
		t.Fatalf("other %+v", none)
	}

	for _, c := range []struct {
		path   string
		status int
		code   string
	}{
		// A cursor naming no alert, or another dependency's alert on a dependency's list.
		{"/dependency-alerts?cursor=" + makeCursor(ep, id.New(id.DependencyAlert)), http.StatusBadRequest, "cursor-invalid"},
		{"/dependencies/" + art + "/alerts?cursor=" + makeCursor(ep, stalled), http.StatusBadRequest, "cursor-invalid"},
		{"/dependency-alerts?cursor=" + makeCursor(ep, art), http.StatusBadRequest, "cursor-invalid"},
		{"/dependency-alerts?kind=lost", http.StatusBadRequest, "invalid-request"},
		{"/dependencies/" + id.New(id.Dependency) + "/alerts", http.StatusNotFound, "not-found"},
		{"/dependencies/" + s.rel + "/alerts", http.StatusNotFound, "not-found"},
	} {
		wantProblem(t, d.get(c.path), c.status, c.code)
	}
}

// DM §7.2's member names, read from the answers' JSON objects rather than through the response
// types, so a renamed member fails here.
func TestDependencyMembers(t *testing.T) {
	d := newDraftEnv(t)
	s := d.release(d.draft, 1, releaseProvenance)
	art := d.artifactStatus()
	d.status("bw-other", 1, "retained", "", 0)
	mustExec(t, d.db, `INSERT INTO dependency_alert (id, seq, kind, dependency, provider, object, version, created, class, reason,
			releases, observed_from, epoch, recorded_at)
		VALUES ($1, 0, 'regression', $2, 'transit', 'bw-artifact', 1, '2026-09-26T09:12:40Z', 'unknown', 'unreachable',
			ARRAY[$3], now(), $4, now())`, id.New(id.DependencyAlert), art, s.rel, epoch(t, d.db))
	whole := func(m map[string]any) map[string]any { return m }
	first := func(m map[string]any) map[string]any {
		items, _ := m["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("no items in %v", m)
		}
		it, _ := items[0].(map[string]any)
		return it
	}
	firstRecord := func(m map[string]any) map[string]any {
		recs, _ := first(m)["records"].([]any)
		if len(recs) == 0 {
			t.Fatalf("no records in %v", m)
		}
		r, _ := recs[0].(map[string]any)
		return r
	}
	dependency := []string{"class", "created", "firstRetainedAt", "id", "object", "observedFrom", "provider", "reason",
		"recordedAt", "stale", "unknownSince", "version"}
	alert := []string{"answerDate", "class", "created", "deletion", "dependency", "epoch", "id", "kind", "object",
		"observedFrom", "provider", "reason", "recordedAt", "releases", "version"}
	for _, c := range []struct {
		path string
		pick func(map[string]any) map[string]any
		want []string
	}{
		{"/dependencies?limit=1", whole, []string{"items", "lastPass", "next"}},
		{"/dependencies", first, dependency},
		{"/dependencies/" + art, whole, dependency},
		{"/dependencies/" + art + "/releases", first, []string{"cluster", "publishedAt", "records", "release"}},
		{"/dependencies/" + art + "/releases", firstRecord, []string{"kind", "machine"}},
		{"/dependencies/" + art + "/alerts", first, alert},
		{"/dependency-alerts", first, alert},
	} {
		var got []string
		for k := range c.pick(decode[map[string]any](t, d.get(c.path), http.StatusOK)) {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s members %v; want %v", c.path, got, c.want)
		}
	}
}

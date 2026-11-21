package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
)

// stubPresence answers every presence read with ans, as the provider answered, and records the
// clusters asked for.
type stubPresence struct {
	mu       sync.Mutex
	ans      classify.Answer
	clusters []string
}

func (s *stubPresence) CAKeyMetadata(_ context.Context, cluster string) (classify.Answer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clusters = append(s.clusters, cluster)
	return s.ans, nil
}

func (s *stubPresence) set(a classify.Answer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ans = a
}

func (s *stubPresence) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.clusters...)
}

// The provider's Date and the times around it, in whole seconds as the header gives them.
var (
	taDate    = time.Date(2026, 11, 21, 6, 0, 0, 0, time.UTC)
	taCreated = time.Date(2026, 11, 21, 5, 40, 12, 123456789, time.UTC)
)

// taMarker is planted in every provider answer, where a defect would copy the answer; taPlanted
// checks the plant reached the answer, so a clean scan means something.
const taMarker = "PLANTED-PROVIDER-TEXT"

// taVersion is one version entry of a KV metadata answer.
func taVersion(created time.Time, deletion string, destroyed bool) map[string]any {
	return map[string]any{"created_time": created.Format(time.RFC3339Nano), "deletion_time": deletion, "destroyed": destroyed}
}

// taMeta is a KV v2 metadata answer with the given current and oldest versions and entries.
func taMeta(current, oldest int, versions map[string]any) classify.Answer {
	return taBody(map[string]any{"data": map[string]any{
		"current_version": current, "oldest_version": oldest, "versions": versions,
		"custom_metadata": map[string]any{"note": taMarker},
	}})
}

func taBody(v any) classify.Answer {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return classify.Answer{Status: http.StatusOK, Date: taDate.Format(http.TimeFormat), Body: classify.NewBody(b)}
}

// taRaw is an answer with the marker appended to its body, so the body is no longer valid JSON where
// it was: use it for an answer that fails whatever its body says. taExact keeps the body as given, for
// an answer whose body decides (a 404 is "never written" only for an empty errors list).
func taRaw(status int, body string) classify.Answer {
	return taExact(status, body+taMarker)
}

func taExact(status int, body string) classify.Answer {
	return classify.Answer{Status: status, Date: taDate.Format(http.TimeFormat), Body: classify.NewBody([]byte(body))}
}

func taPlanted(a classify.Answer) bool {
	return strings.Contains(string(a.Body.Bytes()), taMarker)
}

type talosAccessEnv struct {
	*env
	stub    *stubPresence
	cluster string
	author  string
}

func newTalosAccessEnv(t *testing.T, withPresence bool) *talosAccessEnv {
	t.Helper()
	stub := &stubPresence{}
	d := deps{}
	if withPresence {
		d.presence = stub
	}
	e := newEnvWith(t, d, options{})
	f := &talosAccessEnv{env: e, stub: stub, author: e.human("h-author")}
	f.cluster = e.createCluster(e.api, f.author, "k-ta-cluster-0123456789")
	return f
}

func (f *talosAccessEnv) get(tok, cluster string) *httptest.ResponseRecorder {
	return f.do(f.api, call{method: "GET", path: prefix + "/clusters/" + cluster + "/talos-access", token: tok})
}

func (f *talosAccessEnv) acts(t *testing.T) int {
	t.Helper()
	return count(t, f.db, "SELECT count(*) FROM act")
}

// held decodes a 200 body and checks it is the whole body: present with its version and creation
// time, and no reason.
func wantHeld(t *testing.T, rec *httptest.ResponseRecorder, version int64, created time.Time) {
	t.Helper()
	b := decode[map[string]any](t, rec, http.StatusOK)
	if len(b) != 3 || b["present"] != true || b["version"] != float64(version) || b["createdTime"] != created.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("body %s; want present, version %d, createdTime %s and nothing else", rec.Body, version, created.UTC().Format(time.RFC3339Nano))
	}
}

// wantNotHeld checks a 200 body of present false with its reason and nothing else.
func wantNotHeld(t *testing.T, rec *httptest.ResponseRecorder, reason string) {
	t.Helper()
	b := decode[map[string]any](t, rec, http.StatusOK)
	if len(b) != 2 || b["present"] != false || b["reason"] != reason {
		t.Fatalf("body %s; want present false, reason %s and nothing else", rec.Body, reason)
	}
}

// wantUnusable checks a 503 that says nothing about presence: no present field and, unless wantRetry,
// no Retry-After.
func wantUnusable(t *testing.T, rec *httptest.ResponseRecorder, code string, wantRetry bool) {
	t.Helper()
	doc := wantProblem(t, rec, http.StatusServiceUnavailable, code)
	if _, ok := doc["present"]; ok {
		t.Fatalf("the refusal carries a present field: %s", rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); wantRetry != (got != "") || got != "" && got != "1" {
		t.Fatalf("Retry-After %q, want it %t and 1 second", got, wantRetry)
	}
}

// §3.3 Presence: the latest version stored answers present with its version and creation time, and
// a newer version answers its own, not an earlier one's. A scheduled deletion is still present.
func TestTalosAccessPresent(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	newer := taCreated.Add(time.Hour)
	for name, c := range map[string]struct {
		ans     classify.Answer
		version int64
		created time.Time
	}{
		"first version": {taMeta(1, 0, map[string]any{"1": taVersion(taCreated, "", false)}), 1, taCreated},
		"second version": {taMeta(2, 0, map[string]any{
			"1": taVersion(taCreated, "", false), "2": taVersion(newer, "", false)}), 2, newer},
		"an earlier version destroyed": {taMeta(2, 0, map[string]any{
			"1": taVersion(taCreated, taDate.Add(-time.Hour).Format(time.RFC3339Nano), true), "2": taVersion(newer, "", false)}), 2, newer},
		"deletion scheduled": {taMeta(1, 0, map[string]any{
			"1": taVersion(taCreated, taDate.Add(time.Hour).Format(time.RFC3339Nano), false)}), 1, taCreated},
	} {
		if !taPlanted(c.ans) {
			t.Fatalf("%s: the plant did not reach the answer", name)
		}
		f.stub.set(c.ans)
		rec := f.get(f.author, f.cluster)
		wantHeld(t, rec, c.version, c.created)
		if strings.Contains(rec.Body.String(), taMarker) || strings.Contains(rec.Body.String(), "access/talos-ca") {
			t.Errorf("%s: the body holds provider text or a path: %s", name, rec.Body)
		}
	}
	if got := f.stub.asked(); len(got) != 4 || got[0] != f.cluster {
		t.Fatalf("the provider was asked for %q, want the cluster four times", got)
	}
}

// §3.3 Presence: no version is held when the path is absent (404) or the latest is deleted.
func TestTalosAccessNotHeld(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	past := taDate.Add(-time.Hour).Format(time.RFC3339Nano)
	noDate := taMeta(1, 0, map[string]any{"1": taVersion(taCreated, past, true)})
	noDate.Date = ""
	for name, c := range map[string]struct {
		ans    classify.Answer
		reason string
	}{
		"never written or removed": {taExact(404, `{"errors":[]}`), "never-written-or-removed"},
		"soft deleted":             {taMeta(1, 0, map[string]any{"1": taVersion(taCreated, past, false)}), "latest-version-deleted"},
		"destroyed":                {taMeta(1, 0, map[string]any{"1": taVersion(taCreated, past, true)}), "latest-version-deleted"},
		// A destroyed version needs no Date to be told from a scheduled deletion.
		"destroyed with no readable Date": {noDate, "latest-version-deleted"},
	} {
		f.stub.set(c.ans)
		rec := f.get(f.author, f.cluster)
		wantNotHeld(t, rec, c.reason)
		if strings.Contains(rec.Body.String(), taMarker) {
			t.Errorf("%s: the body holds provider text: %s", name, rec.Body)
		}
	}
}

// §3.3 Presence, row 1, without a database: a 404 is "never written" only for KV's empty errors list, and
// the body of any other 404 (text for a route no engine serves, no body, data beside the list) is 503
// talos-access-unavailable, the same body for every one of them.
func TestPresenceOfNotFound(t *testing.T) {
	body, ref := presenceOf(taExact(404, `{"errors":[]}`))
	if ref != nil || body.Present || body.Reason != reasonNeverWritten {
		t.Fatalf("an empty errors list: %+v, %v", body, ref)
	}
	for _, b := range []string{`{"errors":["no handler for route"]}`, `{}`, ``, `{"errors":[],"data":{}}`, `{"errors":null}`, `[]`, `{"errors":[]} trailing`} {
		body, ref := presenceOf(taExact(404, b))
		if ref == nil || ref.status != http.StatusServiceUnavailable || ref.code != "talos-access-unavailable" || ref.retryAfter != 0 || body.Present || body.Reason != "" {
			t.Errorf("a 404 with body %q: %+v, %+v", b, body, ref)
		}
	}
}

// §3.3 Presence: a deletion in the provider's own second, or a Date that cannot be read, cannot be told
// from a past one, so the read says so and asks to be repeated in a second.
func TestTalosAccessUndecidableDeletion(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	sameSecond := taMeta(1, 0, map[string]any{"1": taVersion(taCreated, taDate.Add(500*time.Millisecond).Format(time.RFC3339Nano), false)})
	noDate := taMeta(1, 0, map[string]any{"1": taVersion(taCreated, taDate.Add(time.Hour).Format(time.RFC3339Nano), false)})
	noDate.Date = ""
	for _, ans := range []classify.Answer{sameSecond, noDate} {
		f.stub.set(ans)
		wantUnusable(t, f.get(f.author, f.cluster), "talos-access-unavailable", true)
	}
}

// §3.3 Presence: a sealed or unreachable store, or no presence identity, is 503 dependency-unavailable
// and never "absent".
func TestTalosAccessStoreUnavailable(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	for _, ans := range []classify.Answer{taRaw(503, `{"errors":["Vault is sealed"]}`), {Unreachable: true}} {
		f.stub.set(ans)
		wantUnusable(t, f.get(f.author, f.cluster), "dependency-unavailable", false)
	}
	// The control: the same cluster read with a usable answer is not a 503.
	f.stub.set(taMeta(1, 0, map[string]any{"1": taVersion(taCreated, "", false)}))
	wantHeld(t, f.get(f.author, f.cluster), 1, taCreated)

	none := newTalosAccessEnv(t, false)
	wantUnusable(t, none.get(none.author, none.cluster), "dependency-unavailable", false)
}

// §3.3 Presence: an answer that says nothing sure about the latest version is 503 talos-access-unavailable,
// whatever the reason, and never present or absent. Each row names the row of the mapping that fires.
func TestTalosAccessUnusableAnswers(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	ok := taVersion(taCreated, "", false)
	noCreated := map[string]any{"deletion_time": "", "destroyed": false}
	badCreated := map[string]any{"created_time": "yesterday", "deletion_time": "", "destroyed": false}
	noCurrent := taBody(map[string]any{"data": map[string]any{"oldest_version": 0, "versions": map[string]any{"1": ok}}})
	for name, ans := range map[string]classify.Answer{
		// Row 1, the answer as a whole.
		"the provider refuses the policy":      taRaw(403, `{"errors":["permission denied"]}`),
		"a status that is neither 200 nor 404": taRaw(500, `{"errors":["boom"]}`),
		// A 404 that is not KV's empty errors list is a route nothing serves, a missing mount: it says
		// nothing about the key, so it is not "never written".
		"a 404 with an error text":      taExact(404, `{"errors":["no handler for route `+taMarker+`"]}`),
		"a 404 with no errors key":      taExact(404, `{}`),
		"a 404 with an empty body":      taExact(404, ``),
		"a 404 with data beside errors": taExact(404, `{"errors":[],"data":{}}`),
		"a body that is not JSON":       taRaw(200, `not json at all`),
		"a body without data":           taBody(map[string]any{}),
		// A missing current_version is refused at row 1, as Unreadable, before CurrentVersion runs, so
		// this case does not test row 2.
		"no current_version": noCurrent,
		// Row 4, the default: Classify at the current version.
		"current_version 0":                      taMeta(0, 0, map[string]any{"1": ok}),
		"the current version has no entry":       taMeta(2, 0, map[string]any{"1": ok}),
		"an entry without created_time":          taMeta(1, 0, map[string]any{"1": noCreated}),
		"an entry whose created_time is no time": taMeta(1, 0, map[string]any{"1": badCreated}),
		"oldest_version above current_version":   taMeta(1, 2, map[string]any{"1": ok}),
		"an entry that is not an object":         taMeta(1, 0, map[string]any{"1": "x"}),
		"an entry without destroyed or deletion": taMeta(1, 0, map[string]any{"1": map[string]any{"created_time": taCreated.Format(time.RFC3339Nano)}}),
		"a deletion_time that is not a time":     taMeta(1, 0, map[string]any{"1": taVersion(taCreated, "soon", false)}),
	} {
		f.stub.set(ans)
		rec := f.get(f.author, f.cluster)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d %s; want 503", name, rec.Code, rec.Body)
			continue
		}
		wantUnusable(t, rec, "talos-access-unavailable", false)
		if strings.Contains(rec.Body.String(), taMarker) {
			t.Errorf("%s: the refusal holds provider text: %s", name, rec.Body)
		}
	}
}

// §3.3 Presence: an unknown cluster is 404 and asks the provider nothing, whatever the provider holds.
func TestTalosAccessUnknownCluster(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	f.stub.set(taMeta(1, 0, map[string]any{"1": taVersion(taCreated, "", false)}))
	// An unknown cluster, a string that is no identifier, and an identifier of another kind.
	for _, cluster := range []string{id.New(id.Cluster), "cl_nope", id.New(id.Machine)} {
		wantProblem(t, f.get(f.author, cluster), http.StatusNotFound, "not-found")
	}
	if got := f.stub.asked(); len(got) != 0 {
		t.Fatalf("the provider was asked for %q about clusters that do not exist", got)
	}
	wantHeld(t, f.get(f.author, f.cluster), 1, taCreated) // the control: a real cluster is read
}

// §3.3 Presence: the read is for an author who is a human. A viewer and a service token that holds
// the author role are 403, each with its control, and the provider is not asked for either. The read
// takes no query and records no act.
func TestTalosAccessRefusals(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	f.stub.set(taMeta(1, 0, map[string]any{"1": taVersion(taCreated, "", false)}))
	acts := f.acts(t)
	wantProblem(t, f.get(f.human("h-viewer"), f.cluster), http.StatusForbidden, "forbidden")
	wantProblem(t, f.get(f.human("h-publisher"), f.cluster), http.StatusForbidden, "forbidden")
	wantProblem(t, f.get(f.robot, f.cluster), http.StatusForbidden, "forbidden")
	wantProblem(t, f.get("", f.cluster), http.StatusUnauthorized, "unauthenticated")
	if got := f.stub.asked(); len(got) != 0 {
		t.Fatalf("the provider was asked for %q on behalf of a refused caller", got)
	}
	wantHeld(t, f.get(f.author, f.cluster), 1, taCreated) // the control: an author human is served
	wantHeld(t, f.get(f.human("h-all"), f.cluster), 1, taCreated)
	wantProblem(t, f.do(f.api, call{method: "GET", path: prefix + "/clusters/" + f.cluster + "/talos-access?x=1", token: f.author}),
		http.StatusBadRequest, "invalid-request")
	if got := f.acts(t); got != acts {
		t.Fatalf("%d acts before the reads, %d after", acts, got)
	}
}

// §3.3 Presence: no answer, whatever it holds, puts the provider's text, a path or a token into a
// response, and the answers all go out under no-store (every do checks the headers).
func TestTalosAccessHidesTheProvider(t *testing.T) {
	f := newTalosAccessEnv(t, true)
	ok := taVersion(taCreated, "", false)
	for name, ans := range map[string]classify.Answer{
		"held":        taMeta(1, 0, map[string]any{"1": ok}),
		"absent":      taExact(404, `{"errors":["no value found at secret/metadata/access/talos-ca `+taMarker+`"]}`),
		"denied":      taRaw(403, `{"errors":["1 error occurred: permission denied for token s.stand-in"]}`),
		"sealed":      taRaw(503, `{"errors":["Vault is sealed"]}`),
		"unparseable": taRaw(200, `{"data":`),
	} {
		if !taPlanted(ans) {
			t.Fatalf("%s: the plant did not reach the answer", name)
		}
		f.stub.set(ans)
		rec := f.get(f.author, f.cluster)
		for _, leak := range []string{taMarker, "access/talos-ca", "secret/", "s.stand-in", "Vault", "permission denied", "errors"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: the response holds %q: %s", name, leak, rec.Body)
			}
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", name, rec.Header().Get("Cache-Control"))
		}
	}
	// The control for the scan: it finds the text where it is.
	if !strings.Contains(string(taRaw(403, "x").Body.Bytes()), taMarker) {
		t.Fatal("the scan does not find the plant")
	}
}

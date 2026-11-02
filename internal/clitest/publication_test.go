package clitest

import (
	"bufio"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// standInNodeID is the Talos node ID the stand-in reports, as `talosctl get identity` prints one.
const standInNodeID = "7x1SuC8Ege5BGXdAfTEff5iQnlWZLfv9h1LGMxA2pYkC"

// standIn is the seeded machine's Talos stand-in (talostest) on loopback. It reports the machine's
// identity and a configuration the test generates; the cluster's talosconfig in the provider is
// one the test generates for it. secrets are the values of both that the leak scan looks for.
type standIn struct {
	node    *talostest.Node
	secrets []string
}

// configSecret is a secret-bearing line of a generated configuration: a token, a secret or a key.
var configSecret = regexp.MustCompile(`(?m)^\s*(?:token|secret|key|aescbcEncryptionSecret|secretboxEncryptionSecret): (\S{16,})$`)

// newStandIn serves the stand-in, writes its talosconfig to the provider at
// access/talos/<cluster> under the administrative identity, as the operator does (persistence-api
// §5.4), and points the machine at it through the CLI. Each of unmarked is a synthetic secret its
// configuration holds in a file's content, /var/etc/bwsynth-<n>.conf, which the schema does not
// recognise, so only an operator's mark extracts it; the leak scan looks for it too.
func (f *fixture) newStandIn(t *testing.T, b *baotest.Bao, s seeded, ctl func(...string) result, unmarked ...string) *standIn {
	t.Helper()
	pki := talostest.NewPKI(t)
	node := talostest.Serve(t, pki)
	node.SetSMBIOSUUID(t, strings.ToUpper(seedUUID), true)
	node.SetNodeID(t, standInNodeID)
	node.SetClusterID(t, seedClusterID)
	in, err := generate.NewInput("bwsynth", "https://cp.example.test:6443", constants.DefaultKubernetesVersion, generate.WithInstallDisk("/dev/sda"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := in.Config(machine.TypeControlPlane)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range unmarked {
		mc := cfg.RawV1Alpha1().MachineConfig
		mc.MachineFiles = append(mc.MachineFiles, &v1alpha1.MachineFile{FileContent: "token = " + v, FilePermissions: 0o644,
			FilePath: fmt.Sprintf("/var/etc/bwsynth-%d.conf", i), FileOp: "create"})
	}
	doc, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	node.SetMachineConfig(t, doc)
	st := &standIn{node: node, secrets: append([]string{base64.StdEncoding.EncodeToString(pki.KeyPEM)}, unmarked...)}
	for _, m := range configSecret.FindAllStringSubmatch(string(doc), -1) {
		st.secrets = append(st.secrets, m[1])
	}
	if len(st.secrets) < 3 {
		t.Fatalf("the generated configuration holds %d secrets the scan can find", len(st.secrets)-1)
	}
	access := "/v1/secret/data/access/talos/" + s.cluster
	data := map[string]any{"data": map[string]any{"talosconfig": string(pki.Talosconfig("", "", ""))}}
	if status, body, err := b.Do(b.Admin(), http.MethodPost, access, data); err != nil || status/100 != 2 {
		t.Fatalf("writing the talosconfig: %d %v %s", status, err, body)
	}
	t.Cleanup(func() { b.Do(b.Admin(), http.MethodDelete, "/v1/secret/metadata/access/talos/"+s.cluster, nil) })
	f.want(ctl("--context", "human", "machine", "set-endpoint", s.machine, "--talos-endpoint", node.Endpoint), 0)
	return st
}

// leaks names the stand-in's secrets s holds.
func (st *standIn) leaks(s string) []string {
	var l []string
	for i, v := range st.secrets {
		if strings.Contains(s, v) {
			l = append(l, fmt.Sprintf("secret %d", i))
		}
	}
	return l
}

// importMachine imports machine into draft from its stand-in as h-all (POST /ingestions, source
// machine), a route no CLI command sends yet, and waits for the ingestion's operation to succeed.
func (f *fixture) importMachine(t *testing.T, machine, draft string) {
	t.Helper()
	body := `{"kind":"import","source":"machine","staging":"transient","machine":"` + machine + `","draft":"` + draft + `","marks":[]}`
	q, _ := http.NewRequest(http.MethodPost, f.base+"/api/v1/ingestions", strings.NewReader(body))
	q.Header.Set("Authorization", "Bearer "+f.mint("h-all", ""))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", "k-import-"+machine+"-"+draft)
	q.Header.Set("If-Match", f.draftETag(draft))
	var started struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(f.do(q, http.StatusAccepted), &started); err != nil || started.Operation == "" {
		t.Fatalf("POST /ingestions: %v", err)
	}
	if state := f.ended(started.Operation); state != "succeeded" {
		t.Fatalf("the import ended %s: %s", state, f.events(started.Operation))
	}
}

// ended waits up to two minutes for operation op to end and returns its state.
func (f *fixture) ended(op string) string {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var state string
		if err := f.db.QueryRow(`SELECT state FROM operation WHERE id = $1`, op).Scan(&state); err != nil {
			f.t.Errorf("reading operation %s: %v", op, err)
			return ""
		}
		if client.Terminal(state) || time.Now().After(deadline) {
			return state
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// events are operation op's event entries, in order.
func (f *fixture) events(op string) []string {
	f.t.Helper()
	rows, err := f.db.Query(`SELECT entry::text FROM operation_event WHERE operation = $1 ORDER BY number`, op)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// cutStream is EVT-04's fault: on the first event stream it passes the headers and the first
// event, holds the rest until the database shows the operation terminal, then ends the stream.
// Every other request passes.
func (f *fixture) cutStream(t *testing.T, cut *atomic.Bool) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Accept") != "text/event-stream" || !cut.CompareAndSwap(false, true) {
			return false
		}
		q, _ := http.NewRequestWithContext(r.Context(), r.Method, f.base+r.URL.RequestURI(), nil)
		q.Header = r.Header.Clone()
		a, err := http.DefaultClient.Do(q)
		if err != nil {
			t.Errorf("EVT-04: opening the stream: %v", err)
			w.WriteHeader(http.StatusBadGateway)
			return true
		}
		defer a.Body.Close()
		maps.Copy(w.Header(), a.Header)
		w.WriteHeader(a.StatusCode)
		lines := bufio.NewReader(a.Body)
		for event := false; ; {
			line, err := lines.ReadString('\n')
			if err != nil {
				t.Errorf("EVT-04: the stream ended before its first event: %v", err)
				return true
			}
			io.WriteString(w, line)
			if line == "\n" && event {
				break
			}
			event = event || strings.HasPrefix(line, "id:")
		}
		w.(http.Flusher).Flush()
		f.ended(path.Base(path.Dir(r.URL.Path)))
		return true
	}
}

// opID is an operation ID in the CLI's output.
var opID = regexp.MustCompile(`\bop_[a-z2-7]{26}\b`)

// TestPublication runs the publication wait through the CLI against a server with a provider and
// a Talos stand-in for the seeded machine: the case of testdata/publication-cases.yaml (EVT-04),
// no mutation across it, the release's review and provenance, a withheld review, the journey tasks
// T6 and T7, and a scan of the CLI's files for the stand-in's secrets.
func TestPublication(t *testing.T) {
	f, bao := startProvider(t, "viewer")
	in := newInjector(t, f.base)
	in.writing = true
	b, err := os.ReadFile("testdata/publication-cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(b, &records); err != nil || len(records) != 1 || records[0].ID != "EVT-04" {
		t.Fatalf("publication-cases.yaml holds %v, %v; want the one record EVT-04", records, err)
	}
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	f.store(f.humanFile(), f.mint("h-all", ""), "")
	s := f.seed()
	var st *standIn
	ctl := func(args ...string) result {
		r := f.scan(f.exec(nil, "", args...), args)
		if st == nil {
			return r
		}
		for _, where := range []string{strings.Join(args, " "), r.out, r.err} {
			if l := st.leaks(where); len(l) > 0 {
				t.Errorf("bronzewardctl %v: output or argv holds the stand-in's %v", args, l)
			}
		}
		return r
	}
	st = f.newStandIn(t, bao.b, s, ctl)
	f.importMachine(t, s.machine, s.draft)
	applied := func() sql.NullString {
		var a sql.NullString
		if err := f.db.QueryRow(`SELECT applied_release FROM machine_state WHERE machine = $1`, s.machine).Scan(&a); err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		return a
	}
	before := applied()

	var op, rel string
	t.Run("EVT-04", func(t *testing.T) {
		var cut atomic.Bool
		in.set(nil, nil)
		f.want(ctl("--context", "human", "draft", "show", s.draft), 0, s.draft)
		in.set(f.cutStream(t, &cut), nil)
		r := ctl("--context", "human", "publish", s.draft)
		f.want(r, 0, "Publication succeeded: release rel_", "is now Desired for 1 machine. Nothing has been sent to any machine.",
			s.machine+" has no Applied release: hand it over with an adopt plan:\n    bronzewardctl plan create --release 'rel_",
			"' --machine '"+s.machine+"' --operation 'adopt'\n")
		if op = opID.FindString(r.out); op == "" {
			t.Fatalf("publish named no operation:\n%s", r.out)
		}
		if !cut.Load() {
			t.Error("the publication wait opened no event stream")
		}
		var shown []string
		for _, e := range f.events(op) {
			var v struct{ Type string }
			json.Unmarshal([]byte(e), &v)
			shown = append(shown, "  "+v.Type)
		}
		if got := strings.Split(strings.TrimSuffix(r.err, "\n"), "\n"); !slices.Equal(got, shown) {
			t.Errorf("standard error %q; want each event once, in order: %q", got, shown)
		}
		events := "GET /api/v1/operations/" + op + "/events"
		seen := in.requests()
		if n := len(slices.DeleteFunc(slices.Clone(seen), func(s string) bool { return s != events })); n != 2 ||
			!slices.Contains(seen, "GET /api/v1/operations/"+op) {
			t.Errorf("requests %v; want the stream, the operation read and the JSON event pages", seen)
		}
		if err := f.db.QueryRow(`SELECT id FROM release WHERE operation = $1`, op).Scan(&rel); err != nil {
			t.Fatal(err)
		}
	})
	if rel == "" {
		t.FailNow()
	}

	t.Run("no mutation", func(t *testing.T) {
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM operation WHERE kind IN ('apply-config', 'adopt')`).Scan(&n); err != nil || n != 0 {
			t.Errorf("%d apply-config or adopt operations, %v", n, err)
		}
		if a := st.node.AppliedRequests(); len(a) != 0 {
			t.Errorf("the stand-in received %d ApplyConfiguration requests", len(a))
		}
		if after := applied(); after != before {
			t.Errorf("the machine's Applied went from %v to %v", before, after)
		}
	})

	t.Run("review and provenance", func(t *testing.T) {
		in.set(nil, nil)
		r := ctl("--context", "human", "release", "review", rel, s.machine)
		f.want(r, 0, "Release "+rel+", machine "+s.machine+", validated: container\n", "machine:\n", "<redacted:")
		raw := ctl("--context", "human", "-o", "json", "release", "review", rel, s.machine)
		var served struct {
			Configuration *string
			Provenance    []struct {
				Reference string
				Version   int
				Member    *int
			}
		}
		if err := json.Unmarshal([]byte(raw.out), &served); err != nil || served.Configuration == nil {
			t.Fatalf("-o json: %v\n%s", err, raw.out)
		}
		if !strings.Contains(r.out, *served.Configuration) {
			t.Errorf("review did not print the served configuration verbatim:\n%s", r.out)
		}
		p := ctl("--context", "human", "release", "provenance", rel, s.machine)
		f.want(p, 0, "Provenance covers secret references only; other fields are not traced.")
		rows := strings.Split(strings.TrimSuffix(p.out, "\n"), "\n")
		if len(served.Provenance) == 0 || len(rows) != len(served.Provenance)+2 {
			t.Fatalf("provenance printed %d lines for %d served records:\n%s", len(rows), len(served.Provenance), p.out)
		}
		for i, rec := range served.Provenance {
			ref := rec.Reference
			if rec.Member != nil {
				ref += fmt.Sprintf("#%d", *rec.Member)
			}
			if !strings.HasPrefix(rows[i+1], ref+" ") || !strings.Contains(rows[i+1], fmt.Sprintf(" %d ", rec.Version)) {
				t.Errorf("row %d %q; want served record %s version %d", i+1, rows[i+1], ref, rec.Version)
			}
		}
	})

	// A withheld review has no client path to it: the redacted form is removed from the release's
	// row in the database, as a configuration that could not be redacted leaves it.
	t.Run("withheld review", func(t *testing.T) {
		tx, err := f.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, q := range []string{`ALTER TABLE release_machine DISABLE TRIGGER immutable_rows`,
			`UPDATE release_machine SET redacted = NULL WHERE release = '` + rel + `'`,
			`ALTER TABLE release_machine ENABLE TRIGGER immutable_rows`} {
			if _, err := tx.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		r := ctl("--context", "human", "release", "review", rel, s.machine)
		f.want(r, 0, "Configuration not shown: it could not be redacted.\nServer notice: the configuration could not be redacted, so it is not shown\n")
		if strings.Contains(r.out, "machine:") {
			t.Errorf("a withheld review printed a document:\n%s", r.out)
		}
	})

	t.Run("journeys", func(t *testing.T) { f.publicationJourneys(t, in, s, ctl) })

	t.Run("files", func(t *testing.T) {
		err := filepath.WalkDir(f.home, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if l := st.leaks(string(b)); len(l) > 0 {
				t.Errorf("%s holds the stand-in's %v", p, l)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	f.scanFiles()
}

// publicationJourneys runs the journey tasks T6 and T7 (operator-experience.md §13) as scripts
// through the CLI and keeps their output in testdata/journeys (keepJourney). T6 is done when the
// machine's review is open: a fragment with an empty label is assigned to the machine, published,
// and the review read. T7 explains the empty label and a redacted field: the review, the
// provenance of its secret references, and the draft that names the fragment revision the label
// came from.
func (f *fixture) publicationJourneys(t *testing.T, in *injector, s seeded, ctl func(...string) result) {
	in.set(nil, nil)
	dir := t.TempDir()
	labels := filepath.Join(dir, "worker-labels.yaml")
	if err := os.WriteFile(labels, []byte("machine:\n  nodeLabels:\n    node.example.test/pool: workers\n    node.example.test/team: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var draft, rel string
	script := func(name string, steps [][]string) {
		var out strings.Builder
		for _, step := range steps {
			args := slices.Clone(step)
			for i, a := range args {
				args[i] = strings.NewReplacer("{draft}", draft, "{release}", rel, "{machine}", s.machine, "{cluster}", s.cluster).Replace(a)
			}
			r := ctl(args...)
			switch {
			case args[2] == "draft" && args[3] == "create":
				// A machine published from a draft needs an import base in it; no CLI command imports
				// yet, so the test does, and the script's next step reads the draft it moved.
				draft = regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(r.out)
				f.importMachine(t, s.machine, draft)
			case args[2] == "publish":
				if rel = regexp.MustCompile(`rel_[a-z2-7]{26}`).FindString(r.out); rel == "" {
					t.Errorf("the journey's publication named no release:\n%s%s", r.out, r.err)
				}
			}
			fmt.Fprintf(&out, "$ bronzewardctl %s\n%s%s[exit %d]\n\n", strings.Join(step, " "), r.out, r.err, r.code)
		}
		keepJourney(t, name, out.String(), dir)
	}
	h := []string{"--context", "human"}
	script("t6", [][]string{
		append(h, "draft", "create", "--cluster", "{cluster}", "--title", "worker labels"),
		append(h, "draft", "show", "{draft}"),
		append(h, "fragment", "set", "worker-labels", "--draft", "{draft}", "--layer", "role", "--file", labels),
		append(h, "profile", "set", "workers", "--draft", "{draft}", "--fragments", "worker-labels"),
		append(h, "assignment", "set", "{machine}", "--draft", "{draft}", "--profiles", "workers"),
		append(h, "publish", "{draft}"),
		append(h, "release", "review", "{release}", "{machine}"),
	})
	script("t7", [][]string{
		append(h, "release", "review", "{release}", "{machine}"),
		append(h, "release", "provenance", "{release}", "{machine}"),
		append(h, "draft", "show", "{draft}"),
	})
}

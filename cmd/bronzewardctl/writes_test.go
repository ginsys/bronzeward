package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestRepeatFlags checks each refusal of decision T10's repeat flags (#256), all before sending.
func TestRepeatFlags(t *testing.T) {
	key, epoch, principal, ifMatch := "--idempotency-key=BWSYNTH-aaaaaaaaaaaaaaa", "--expect-epoch=ep_1", "--expect-principal=svc_1", `--if-match="e"`
	for _, c := range []struct {
		name         string
		args         []string
		takesIfMatch bool
		want         string // "" for a repeat, "first" for a first send, else the refusal
	}{
		{"none: a first send", nil, false, "first"},
		{"--if-match alone: a first send with that ETag", []string{ifMatch}, true, "first"},
		{"all three", []string{key, epoch, principal}, false, ""},
		{"all four where the route takes If-Match", []string{key, epoch, principal, ifMatch}, true, ""},
		{"no key", []string{epoch, principal}, false, "a repeat needs --idempotency-key, --expect-epoch and --expect-principal"},
		{"no epoch", []string{key, principal}, false, "a repeat needs"},
		{"no principal", []string{key, epoch}, false, "a repeat needs"},
		{"no if-match where the route takes one", []string{key, epoch, principal}, true, "and --if-match, each as the outcome-unknown block printed it"},
		{"if-match where the route takes none", []string{key, epoch, principal, ifMatch}, false, "this route takes no If-Match"},
		{"a key not as printed", []string{"--idempotency-key=short", epoch, principal}, false, "give the key as printed"},
		// A script's unset variables: never a first send with a new key.
		{"all given empty", []string{"--idempotency-key=", "--expect-epoch=", "--expect-principal="}, false, "was given an empty value"},
		{"one given empty", []string{key, epoch, "--expect-principal="}, false, "--expect-principal was given an empty value"},
		{"if-match given empty", []string{"--if-match="}, true, "--if-match was given an empty value"},
	} {
		var f repeatFlags
		fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
		f.flags(fs)
		if err := fs.Parse(c.args); err != nil {
			t.Fatal(err)
		}
		r, err := f.repeat(c.takesIfMatch)
		var u usageError
		switch {
		case c.want == "first" && (r != nil || err != nil):
			t.Errorf("%s: %v, %v; want a first send", c.name, r, err)
		case c.want == "" && (err != nil || r == nil || r.Key != f.key.v || r.Epoch != "ep_1" || r.Principal != "svc_1"):
			t.Errorf("%s: %v, %v; want the repeat", c.name, r, err)
		case c.want != "" && c.want != "first" && (!errors.As(err, &u) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v; want a usage error saying %q", c.name, err, c.want)
		}
	}
}

// TestUnreadableAnswers: a write that succeeded exits 0 though its answer cannot be read, and -o
// json prints the answer as served; after an unknown outcome, a list item that cannot be read fails
// the confirming read rather than being skipped, since it could be the one looked for.
func TestUnreadableAnswers(t *testing.T) {
	var unknown atomic.Bool // the POST is redirected, an outcome unknown, when set
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
		case "GET /api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case "GET /api/v1/clusters":
			fmt.Fprint(w, `{"items":[["BWSYNTH"]]}`)
		case "POST /api/v1/clusters":
			if unknown.Load() {
				http.Redirect(w, r, "https://BWSYNTH.example.invalid/", http.StatusTemporaryRedirect)
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":["BWSYNTH"]}`)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, o := range []string{"table", "json", "yaml"} {
		var out, errb bytes.Buffer
		code := run([]string{"-o", o, "cluster", "add", "--name", "lab-a"}, &out, &errb)
		wantOut := map[string]string{"table": "", "json": `"BWSYNTH"`}[o]
		if code != 0 || !strings.Contains(errb.String(), "succeeded, but its answer cannot be read") || !strings.Contains(out.String(), wantOut) || o == "table" && out.Len() != 0 {
			t.Errorf("-o %s: exit %d, stdout %q, stderr %q; want 0, the answer as served for json, the failure on stderr", o, code, out.String(), errb.String())
		}
		// Standard output failing part way: the answer is written once and the failure is the exit.
		short := &shortWriter{}
		if code := run([]string{"-o", o, "cluster", "add", "--name", "lab-a"}, short, io.Discard); o != "table" && (code != 1 || short.calls != 1) {
			t.Errorf("-o %s with standard output failing: exit %d after %d writes; want 1 after 1", o, code, short.calls)
		}
	}
	unknown.Store(true)
	var out, errb bytes.Buffer
	if code := run([]string{"cluster", "add", "--name", "lab-a"}, &out, &errb); code != 3 || !strings.Contains(errb.String(), "GET /api/v1/clusters: item 0 cannot be read") {
		t.Errorf("an unreadable item after an unknown outcome: exit %d, stderr %q; want 3 and the item named", code, errb.String())
	}
}

// TestUnknownOutcomeOutput: on exit 3 standard output stays empty, whatever -o asks, and the T10
// block is on standard error; a repeat missing a flag exits 2 and sends nothing.
func TestUnknownOutcomeOutput(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
		case "GET /api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case "GET /api/v1/clusters":
			fmt.Fprint(w, `{"items":[]}`)
		default:
			http.Redirect(w, r, "https://BWSYNTH.example.invalid/", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, o := range []string{"table", "json", "yaml"} {
		var out, errb bytes.Buffer
		code := run([]string{"-o", o, "cluster", "add", "--name", "lab-a"}, &out, &errb)
		if code != 3 || out.Len() != 0 || !strings.Contains(errb.String(), "outcome unknown") || !strings.Contains(errb.String(), "--idempotency-key ") {
			t.Errorf("-o %s: exit %d, stdout %q, stderr %s; want 3, nothing and the block", o, code, out.String(), errb.String())
		}
	}
	mu.Lock()
	seen = nil
	mu.Unlock()
	var out, errb bytes.Buffer
	if code := run([]string{"cluster", "add", "--name", "lab-a", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa"}, &out, &errb); code != 2 || out.Len() != 0 {
		t.Errorf("a repeat missing flags: exit %d, stdout %q", code, out.String())
	}
	if len(seen) != 0 {
		t.Errorf("a repeat missing flags sent %v", seen)
	}
}

package provider

import (
	"context"
	"io"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/classify"
)

func metadataStandIn(t *testing.T, handler http.HandlerFunc) (*Metadata, *recorder) {
	t.Helper()
	addr, rec := serve(t, handler)
	m, err := NewMetadata(addr, tokenOf(testToken))
	if err != nil {
		t.Fatal(err)
	}
	return m, rec
}

const metaDate = "Thu, 24 Sep 2026 19:51:17 GMT"

// One GET by name, no LIST, no query (dependency-monitor.md §3 step 1); the answer comes back as
// sent: status, Date and body.
func TestMetadataAsksByName(t *testing.T) {
	const body = `{"data":{"anything":1}}`
	m, rec := metadataStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", metaDate)
		w.WriteHeader(200)
		io.WriteString(w, body)
	})
	p := newPath(t)
	a, err := m.KV(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if q := rec.last(); q.method != http.MethodGet || q.path != "/v1/secret/metadata/"+p.String() || q.token != testToken || len(q.body) != 0 {
		t.Fatalf("sent %s %s", q.method, q.path)
	}
	if a.Status != 200 || a.Date != metaDate || string(a.Body) != body || a.Unreachable {
		t.Fatalf("answer %+v", a)
	}
	a, err = m.Transit(t.Context(), testArtifactKey)
	if err != nil {
		t.Fatal(err)
	}
	if q := rec.last(); q.method != http.MethodGet || q.path != "/v1/transit/keys/"+testArtifactKey || len(q.body) != 0 {
		t.Fatalf("sent %s %s", q.method, q.path)
	}
	if a.Status != 200 || a.Date != metaDate || string(a.Body) != body {
		t.Fatalf("answer %+v", a)
	}
}

// No status is mapped here: the classifier's table decides each, and a redirect is not followed.
func TestMetadataStatusesUnmapped(t *testing.T) {
	for _, status := range []int{204, 307, 400, 403, 404, 429, 500, 503} {
		m, rec := metadataStandIn(t, func(w http.ResponseWriter, r *http.Request) {
			if status == 307 {
				w.Header().Set("Location", "/elsewhere")
			}
			w.WriteHeader(status)
		})
		a, err := m.Transit(t.Context(), testArtifactKey)
		if err != nil || a.Status != status || a.Unreachable {
			t.Errorf("%d: %+v, %v", status, a, err)
		}
		if n := len(rec.all()); n != 1 {
			t.Errorf("%d: %d requests", status, n)
		}
	}
}

func TestMetadataUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + l.Addr().String()
	l.Close()
	m, err := NewMetadata(addr, tokenOf(testToken))
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Transit(t.Context(), testArtifactKey)
	if err != nil || !a.Unreachable || a.Status != 0 {
		t.Fatalf("%+v, %v", a, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m2, _ := metadataStandIn(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	if a, err := m2.KV(ctx, newPath(t)); err != nil || !a.Unreachable {
		t.Fatalf("cancelled: %+v, %v", a, err)
	}
}

// A body over the cap is not kept: the classifier then finds the answer unreadable.
func TestMetadataBodyCapped(t *testing.T) {
	m, _ := metadataStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"data":{"pad":"`+strings.Repeat("x", maxResponse)+`"}}`)
	})
	a, err := m.Transit(t.Context(), testArtifactKey)
	if err != nil || a.Status != 200 || a.Body != nil {
		t.Fatalf("status %d, %d body bytes, %v", a.Status, len(a.Body), err)
	}
	if r := classify.Classify(classify.Dependency{Provider: classify.Transit, Object: testArtifactKey, Version: 1}, a); r.Reason != classify.Unreadable {
		t.Fatalf("classified %s/%q", r.Class, r.Reason)
	}
}

func TestMetadataRefusesBeforeSending(t *testing.T) {
	m, rec := metadataStandIn(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	if _, err := m.KV(t.Context(), GenerationPath{}); err == nil {
		t.Error("the zero path was asked for")
	}
	for _, key := range []string{"", "..", "a/b", "a?b"} {
		if _, err := m.Transit(t.Context(), key); err == nil {
			t.Errorf("key %q asked for", key)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d request(s) sent", n)
	}
}

// withList is the control: the role type with one more method.
type withList struct{ *Metadata }

func (withList) List(context.Context, string) ([]string, error) { return nil, nil }

// The metadata identity asks for one object's metadata by name and does nothing else
// (dependency-monitor.md §4): no LIST, no data read, no Transit operation.
func TestMetadataMethodSet(t *testing.T) {
	want := []string{"KV", "Transit"}
	if got := methodNames(reflect.TypeFor[*Metadata]()); !slices.Equal(got, want) {
		t.Fatalf("*Metadata exports %q, want exactly %q", got, want)
	}
	if got := methodNames(reflect.TypeFor[withList]()); slices.Equal(got, want) {
		t.Fatal("the control with an extra method passed the same check")
	}
}

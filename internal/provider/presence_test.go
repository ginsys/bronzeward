package provider

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

func presenceStandIn(t *testing.T, handler http.HandlerFunc) (*Presence, *recorder) {
	t.Helper()
	addr, rec := serve(t, handler)
	p, err := NewPresence(addr, tokenOf(testToken))
	if err != nil {
		t.Fatal(err)
	}
	return p, rec
}

// One GET of the metadata of the cluster's CA key, built from a cluster id only; the answer comes
// back as sent.
func TestPresenceAsksTheMetadataPath(t *testing.T) {
	const body = `{"data":{"anything":1}}`
	p, rec := presenceStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", metaDate)
		w.WriteHeader(200)
		io.WriteString(w, body)
	})
	cl := id.New(id.Cluster)
	a, err := p.CAKeyMetadata(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	if q := rec.last(); q.method != http.MethodGet || q.path != "/v1/secret/metadata/access/talos-ca/"+cl || q.token != testToken || len(q.body) != 0 {
		t.Fatalf("sent %s %s", q.method, q.path)
	}
	if a.Status != 200 || a.Date != metaDate || string(a.Body.Bytes()) != body || a.Unreachable {
		t.Fatalf("answer %+v", a)
	}
}

// A cluster that is not a cl_ identifier sends nothing, so no caller can aim the identity at
// another secret.
func TestPresenceRefusesBeforeSending(t *testing.T) {
	p, rec := presenceStandIn(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	cl := id.New(id.Cluster)
	for name, in := range map[string]string{
		"empty": "", "other kind": id.New(id.Ingestion), "slash": cl + "/x", "dot-dot": "cl_../../sys", "query": cl + "?x",
	} {
		if _, err := p.CAKeyMetadata(t.Context(), in); err == nil {
			t.Errorf("%s: %q asked for", name, in)
		}
	}
	if n := len(rec.all()); n != 0 {
		t.Fatalf("%d request(s) sent", n)
	}
	if _, err := p.CAKeyMetadata(t.Context(), cl); err != nil || len(rec.all()) != 1 {
		t.Fatalf("the control cluster id was not sent: %v, %d request(s)", err, len(rec.all()))
	}
}

// No status is mapped here: the classifier's table decides each, and a redirect is not followed.
func TestPresenceStatusesUnmapped(t *testing.T) {
	for _, status := range []int{204, 307, 400, 403, 404, 429, 500, 503} {
		p, rec := presenceStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			if status == 307 {
				w.Header().Set("Location", "/elsewhere")
			}
			w.WriteHeader(status)
		})
		a, err := p.CAKeyMetadata(t.Context(), id.New(id.Cluster))
		if err != nil || a.Status != status || a.Unreachable {
			t.Errorf("%d: %+v, %v", status, a, err)
		}
		if n := len(rec.all()); n != 1 {
			t.Errorf("%d: %d requests", status, n)
		}
	}
}

// withData is the control: the role type with one more method.
type withData struct{ *Presence }

func (withData) ReadCAKey(context.Context, string) ([]byte, error) { return nil, nil }

// The presence identity asks for one key's metadata and does nothing else: no data read, no list,
// no write, no Transit operation.
func TestPresenceMethodSet(t *testing.T) {
	want := []string{"CAKeyMetadata"}
	if got := methodNames(reflect.TypeFor[*Presence]()); !slices.Equal(got, want) {
		t.Fatalf("*Presence exports %q, want exactly %q", got, want)
	}
	if got := methodNames(reflect.TypeFor[withData]()); slices.Equal(got, want) {
		t.Fatal("the control with an extra method passed the same check")
	}
}

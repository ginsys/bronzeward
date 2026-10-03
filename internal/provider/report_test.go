package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// reportStandIn returns a Report pointed at handler and the request URIs it received.
func reportStandIn(t *testing.T, handler http.HandlerFunc) (*Report, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var uris []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uris = append(uris, r.Method+" "+r.RequestURI)
		mu.Unlock()
		if r.Header.Get("X-Vault-Token") != testToken {
			t.Errorf("token header %q", r.Header.Get("X-Vault-Token"))
		}
		if r.ContentLength > 0 {
			t.Errorf("a listing sent a body")
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	rep, err := NewReport(srv.URL, tokenOf(testToken))
	if err != nil {
		t.Fatal(err)
	}
	return rep, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(uris)
	}
}

func keys(names ...string) map[string]any { return data(map[string]any{"keys": names}) }

// TestReportListsEachLevel: each level is one GET with list=true on the metadata path; the names
// of Bronzeward's form come back, and every other name is counted, never returned.
func TestReportListsEachLevel(t *testing.T) {
	cl, other, claim, value := id.New(id.Cluster), id.New(id.Cluster), id.New(id.Ingestion), NewValueID()
	rep, uris := reportStandIn(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/secret/metadata/gen/":
			// A leaf directly under gen/, a non-cluster directory and a cluster id without
			// its slash are not Bronzeward's.
			respond(t, w, 200, keys(cl+"/", "stray", "notes/", other+"/", cl))
		case "/v1/secret/metadata/gen/" + cl + "/":
			respond(t, w, 200, keys(claim+"/", id.New(id.Cluster)+"/", claim))
		case "/v1/secret/metadata/gen/" + cl + "/" + claim + "/":
			respond(t, w, 200, keys(value, "sub/", "has.dot", value+"x"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	ctx := t.Context()

	cls, err := rep.Clusters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cls.Names, []string{cl, other}) || cls.Skipped != 3 {
		t.Fatalf("clusters %q, skipped %d", cls.Names, cls.Skipped)
	}
	claims, err := rep.Claims(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(claims.Names, []string{claim}) || claims.Skipped != 2 {
		t.Fatalf("claims %q, skipped %d", claims.Names, claims.Skipped)
	}
	values, err := rep.Values(ctx, cl, claim)
	if err != nil {
		t.Fatal(err)
	}
	want1, _ := NewGenerationPath(cl, claim, value)
	want2, _ := NewGenerationPath(cl, claim, value+"x")
	if !slices.Equal(values.Paths, []GenerationPath{want1, want2}) || values.Skipped != 2 {
		t.Fatalf("values %v, skipped %d", values.Paths, values.Skipped)
	}
	want := []string{
		"GET /v1/secret/metadata/gen/?list=true",
		"GET /v1/secret/metadata/gen/" + cl + "/?list=true",
		"GET /v1/secret/metadata/gen/" + cl + "/" + claim + "/?list=true",
	}
	if got := uris(); !slices.Equal(got, want) {
		t.Fatalf("requests %q, want %q", got, want)
	}
}

// TestReportListingOutcomes: a 404 is an empty directory; every other failure keeps its class.
func TestReportListingOutcomes(t *testing.T) {
	cl := id.New(id.Cluster)
	for name, c := range map[string]struct {
		status int
		body   any
		want   error
	}{
		"absent":         {404, map[string]any{"errors": []string{}}, nil},
		"denied":         {403, map[string]any{"errors": []string{"permission denied"}}, ErrDenied},
		"sealed":         {503, map[string]any{"errors": []string{"Vault is sealed"}}, ErrUnavailable},
		"no keys":        {200, data(map[string]any{}), ErrProtocol},
		"keys not names": {200, data(map[string]any{"keys": []int{1}}), ErrProtocol},
		"other status":   {500, map[string]any{"errors": []string{"x"}}, nil},
	} {
		rep, _ := reportStandIn(t, func(w http.ResponseWriter, r *http.Request) { respond(t, w, c.status, c.body) })
		l, err := rep.Claims(t.Context(), cl)
		switch {
		case name == "absent":
			if err != nil || len(l.Names) != 0 || l.Skipped != 0 {
				t.Errorf("%s: %+v, %v", name, l, err)
			}
		case name == "other status":
			if err == nil || errors.Is(err, ErrAbsent) || errors.Is(err, ErrDenied) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrProtocol) {
				t.Errorf("%s: %v", name, err)
			}
		case !errors.Is(err, c.want):
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "sealed") {
			t.Errorf("%s: the error relays server text: %v", name, err)
		}
	}
}

// TestReportRefusesNonIDs: a cluster or claim argument that is not an identifier of its kind is
// refused before any request, so no caller can steer a listing outside gen/.
func TestReportRefusesNonIDs(t *testing.T) {
	rep, uris := reportStandIn(t, func(w http.ResponseWriter, r *http.Request) { respond(t, w, 200, keys()) })
	cl, claim := id.New(id.Cluster), id.New(id.Ingestion)
	for name, call := range map[string]func() error{
		"claims of a claim id":      func() error { _, err := rep.Claims(t.Context(), claim); return err },
		"claims of ..":              func() error { _, err := rep.Claims(t.Context(), ".."); return err },
		"values of a cluster pair":  func() error { _, err := rep.Values(t.Context(), cl, cl); return err },
		"values under a claim id":   func() error { _, err := rep.Values(t.Context(), claim, claim); return err },
		"values of a slashed claim": func() error { _, err := rep.Values(t.Context(), cl, claim+"/x"); return err },
	} {
		if call() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got := uris(); len(got) != 0 {
		t.Fatalf("requests sent: %q", got)
	}
}

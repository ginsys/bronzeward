package provider

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// executorStandIn is an Executor pointed at handler, with the recorder standIn fills.
func executorStandIn(t *testing.T, handler http.HandlerFunc) (*Executor, *recorder) {
	t.Helper()
	i, rec := standIn(t, testKeys, handler)
	e, err := NewExecutor(i.c.base, tokenOf(testToken))
	if err != nil {
		t.Fatal(err)
	}
	return e, rec
}

// The executor reads a cluster's Talos access as ingestion does (persistence-api.md §3.3): the same
// request under its own token, the same version identity and the same typed outcomes.
func TestExecutorTalosAccess(t *testing.T) {
	cl := id.New(id.Cluster)
	e, rec := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, kvRead(map[string]any{"talosconfig": talosconfigText}, kvMetadata(4, "2026-10-08T10:11:12Z")))
	})
	a, err := e.TalosAccess(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.last()
	if got.method != http.MethodGet || got.path != "/v1/secret/data/access/talos/"+cl || got.token != testToken || len(got.body) != 0 {
		t.Fatalf("request %s %s, token %q, body %q", got.method, got.path, got.token, got.body)
	}
	if v := a.Version(); v.Path != "access/talos/"+cl || v.Version != 4 || v.CreatedTime.IsZero() || string(a.Talosconfig()) != talosconfigText {
		t.Fatalf("version %+v", v)
	}
	if _, err := e.TalosAccess(t.Context(), "cl_../../sys/raw"); err == nil || len(rec.all()) != 1 {
		t.Fatalf("a malformed cluster: %v after %d requests", err, len(rec.all()))
	}

	for status, want := range map[int]error{404: ErrAbsent, 403: ErrDenied} {
		e, _ := executorStandIn(t, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, status, map[string]any{"errors": []string{}})
		})
		if _, err := e.TalosAccess(t.Context(), cl); !errors.Is(err, want) {
			t.Errorf("status %d: %v, want %v", status, err, want)
		}
	}
}

// The role type has exactly the executor's operations so far: the Talos access read. Artifact
// decryption arrives with dispatch (ginsys/bronzeward#26).
func TestExecutorMethodSet(t *testing.T) {
	want := []string{"TalosAccess"}
	if got := methodNames(reflect.TypeFor[*Executor]()); !slices.Equal(got, want) {
		t.Fatalf("*Executor exports %q, want exactly %q", got, want)
	}
}

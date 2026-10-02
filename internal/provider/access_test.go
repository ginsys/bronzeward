package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"go.yaml.in/yaml/v3"
)

const talosconfigText = "context: a\ncontexts:\n  a:\n    key: TALOS-ACCESS-KEY\n"

// kvRead is a KV v2 read answer for one version.
func kvRead(fields map[string]any, metadata map[string]any) map[string]any {
	return data(map[string]any{"data": fields, "metadata": metadata})
}

func kvMetadata(version any, created any) map[string]any {
	return map[string]any{"version": version, "created_time": created, "deletion_time": "", "destroyed": false, "custom_metadata": nil}
}

func TestTalosAccessPath(t *testing.T) {
	cl := id.New(id.Cluster)
	p, err := TalosAccessPath(cl)
	if err != nil || p != "access/talos/"+cl {
		t.Fatalf("TalosAccessPath(%q) = %q, %v", cl, p, err)
	}
	for name, in := range map[string]string{
		"empty":         "",
		"other kind":    id.New(id.Ingestion),
		"no prefix":     strings.TrimPrefix(cl, "cl_"),
		"slash":         cl + "/x",
		"dot-dot":       "cl_../../sys",
		"trailing junk": cl + "?x",
	} {
		if p, err := TalosAccessPath(in); err == nil {
			t.Errorf("%s: TalosAccessPath(%q) = %q, want a refusal", name, in, p)
		}
	}
}

func TestTalosAccessRead(t *testing.T) {
	cl := id.New(id.Cluster)
	created := "2026-10-02T10:11:12.123456789Z"
	i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, 200, kvRead(map[string]any{"talosconfig": talosconfigText}, kvMetadata(3, created)))
	})
	a, err := i.TalosAccess(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	got := rec.last()
	if got.method != http.MethodGet || got.path != "/v1/secret/data/access/talos/"+cl || got.token != testToken || len(got.body) != 0 {
		t.Fatalf("request %s %s, token %q, body %q", got.method, got.path, got.token, got.body)
	}
	v := a.Version()
	want, _ := time.Parse(time.RFC3339Nano, created)
	if v.Path != "access/talos/"+cl || v.Version != 3 || !v.CreatedTime.Equal(want) {
		t.Fatalf("version %+v", v)
	}
	if string(a.Talosconfig()) != talosconfigText {
		t.Fatalf("talosconfig %q", a.Talosconfig())
	}
	// A copy each time.
	a.Talosconfig()[0] = 'X'
	if string(a.Talosconfig()) != talosconfigText {
		t.Fatal("Talosconfig shares its bytes")
	}
	if len(rec.all()) != 1 {
		t.Fatalf("%d requests", len(rec.all()))
	}
}

func TestTalosAccessRefusesBeforeSending(t *testing.T) {
	i, rec := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {})
	if _, err := i.TalosAccess(t.Context(), "cl_../../sys/raw"); err == nil || len(rec.all()) != 0 {
		t.Fatalf("a non-cluster id: %v, %d requests", err, len(rec.all()))
	}
}

func TestTalosAccessTypedOutcomes(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		want   error
	}{
		"absent": {404, ErrAbsent},
		"denied": {403, ErrDenied},
		"sealed": {503, ErrUnavailable},
	} {
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, c.status, map[string]any{"errors": []string{talosconfigText}})
		})
		_, err := i.TalosAccess(t.Context(), id.New(id.Cluster))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "TALOS-ACCESS-KEY") {
			t.Errorf("%s: the error quotes the response: %v", name, err)
		}
	}
	// Control: a 404 on a create is not absence.
	i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, 404, nil) })
	if _, err := i.CreateGeneration(t.Context(), newPath(t), mustValue(t, KindString, "x")); err == nil || errors.Is(err, ErrAbsent) {
		t.Fatalf("a create's 404: %v", err)
	}
}

func TestTalosAccessRefusesMalformed(t *testing.T) {
	ok := map[string]any{"talosconfig": talosconfigText}
	meta := kvMetadata(1, "2026-10-02T10:11:12Z")
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range meta {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, payload := range map[string]any{
		"no data":            data(nil),
		"no fields":          kvRead(nil, meta),
		"no talosconfig":     kvRead(map[string]any{"other": talosconfigText}, meta),
		"extra field":        kvRead(map[string]any{"talosconfig": talosconfigText, "other": "x"}, meta),
		"not a string":       kvRead(map[string]any{"talosconfig": []string{talosconfigText}}, meta),
		"empty talosconfig":  kvRead(map[string]any{"talosconfig": ""}, meta),
		"no metadata":        kvRead(ok, nil),
		"version zero":       kvRead(ok, with("version", 0)),
		"version missing":    kvRead(ok, with("version", nil)),
		"version a string":   kvRead(ok, with("version", "1")),
		"created missing":    kvRead(ok, with("created_time", nil)),
		"created not a time": kvRead(ok, with("created_time", "TALOS-ACCESS-KEY")),
		"deleted":            kvRead(ok, with("deletion_time", "2026-10-02T11:00:00Z")),
		"destroyed":          kvRead(ok, with("destroyed", true)),
	} {
		i, _ := standIn(t, testKeys, func(w http.ResponseWriter, _ *http.Request) { respond(t, w, 200, payload) })
		_, err := i.TalosAccess(t.Context(), id.New(id.Cluster))
		if !errors.Is(err, ErrProtocol) {
			t.Errorf("%s: %v, want ErrProtocol", name, err)
		}
		if err != nil && strings.Contains(err.Error(), "TALOS-ACCESS-KEY") {
			t.Errorf("%s: the error quotes the response: %v", name, err)
		}
	}
}

func TestTalosAccessDoesNotRender(t *testing.T) {
	a := TalosAccess{v: TalosAccessVersion{Path: "access/talos/cl_x", Version: 1}, b: new([]byte(talosconfigText))}
	type nested struct{ a TalosAccess }
	type exported struct{ A TalosAccess }
	outputs := map[string]string{}
	for _, x := range []any{a, &a, nested{a}, exported{a}, []TalosAccess{a}, map[string]TalosAccess{"k": a}} {
		for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
			outputs[fmt.Sprintf("%s of %T", f, x)] = fmt.Sprintf(f, x)
		}
		if b, err := json.Marshal(x); err == nil {
			outputs[fmt.Sprintf("json of %T", x)] = string(b)
		}
		if b, err := yaml.Marshal(x); err == nil {
			outputs[fmt.Sprintf("yaml of %T", x)] = string(b)
		}
		for name, h := range map[string]func(*bytes.Buffer) slog.Handler{
			"slog text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
			"slog json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		} {
			var b bytes.Buffer
			slog.New(h(&b)).Info("access", "a", x)
			outputs[fmt.Sprintf("%s of %T", name, x)] = b.String()
		}
	}
	for what, out := range outputs {
		if strings.Contains(out, "TALOS-ACCESS-KEY") || strings.Contains(out, "54414c4f") || strings.Contains(out, "VEFMT1") || strings.Contains(out, "contexts") {
			t.Errorf("%s renders the talosconfig: %s", what, out)
		}
	}
	for _, m := range []func() ([]byte, error){a.MarshalJSON, a.MarshalText} {
		if b, err := m(); err == nil || b != nil {
			t.Errorf("a marshaller succeeded: %q", b)
		}
	}
	if v, err := a.MarshalYAML(); err == nil || v != nil {
		t.Errorf("MarshalYAML succeeded: %v", v)
	}
	// Control: the bytes are there to leak, and the version identity renders.
	if !strings.Contains(string(a.Talosconfig()), "TALOS-ACCESS-KEY") {
		t.Fatal("the access does not hold the talosconfig; this test proves nothing")
	}
	if s := fmt.Sprintf("%+v", a.Version()); !strings.Contains(s, "access/talos/cl_x") {
		t.Fatalf("the version identity does not render: %s", s)
	}
}

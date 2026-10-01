package talos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const secret = "machine:\n  token: SECRET-abc123.def456\n"

func TestConfigDoesNotRender(t *testing.T) {
	c := newConfig([]byte(secret), "7")
	type nested struct {
		c Config
		p *Config
	}
	type exported struct {
		C Config
		P *Config
	}
	outputs := map[string]string{}
	for _, v := range []any{c, &c, nested{c, &c}, exported{c, &c}, []Config{c}, map[string]Config{"k": c}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
			outputs[fmt.Sprintf("%s of %T", verb, v)] = fmt.Sprintf(verb, v)
		}
		if b, err := json.Marshal(v); err == nil {
			outputs[fmt.Sprintf("json of %T", v)] = string(b)
		}
		if b, err := yaml.Marshal(v); err == nil {
			outputs[fmt.Sprintf("yaml of %T", v)] = string(b)
		}
		for name, h := range map[string]func(*bytes.Buffer) slog.Handler{
			"slog text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
			"slog json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		} {
			var b bytes.Buffer
			slog.New(h(&b)).Info("read", "config", v)
			outputs[fmt.Sprintf("%s of %T", name, v)] = b.String()
		}
	}
	for what, out := range outputs {
		if strings.Contains(out, "SECRET") || strings.Contains(out, "token") || strings.Contains(out, "5345435245") || strings.Contains(out, "bWFjaGluZ") {
			t.Errorf("%s renders the configuration: %s", what, out)
		}
	}
	for _, m := range []func() ([]byte, error){c.MarshalJSON, c.MarshalText} {
		if b, err := m(); err == nil || b != nil {
			t.Errorf("a marshaller succeeded: %q", b)
		}
	}
	if v, err := c.MarshalYAML(); err == nil || v != nil {
		t.Errorf("MarshalYAML succeeded: %v", v)
	}
	if s := c.String(); s != "[talos machine configuration]" {
		t.Errorf("String: %q", s)
	}
	// Control: the bytes are there, through Bytes only.
	if got := c.Bytes(); string(got) != secret {
		t.Fatalf("Bytes: %q", got)
	}
	t.Logf("control: Bytes returns the configuration; %d renderings do not", len(outputs))
}

func TestConfigBytesIsACopy(t *testing.T) {
	in := []byte(secret)
	c := newConfig(in, "1")
	in[0] = 'X'
	b := c.Bytes()
	b[1] = 'X'
	if got := c.Bytes(); string(got) != secret {
		t.Fatalf("Config shares its bytes: %q", got)
	}
	if c.ResourceVersion() != "1" {
		t.Fatal(c.ResourceVersion())
	}
	var zero Config
	if zero.Bytes() != nil || zero.ResourceVersion() != "" {
		t.Fatal("the zero Config is not empty")
	}
}

func TestDialRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		path string
		t    Target
	}{
		"no talosconfig": {"", Target{Endpoint: "10.55.0.2", Node: "10.55.0.3"}},
		"no endpoint":    {"talosconfig", Target{Node: "10.55.0.3"}},
		"no node":        {"talosconfig", Target{Endpoint: "10.55.0.2"}},
	} {
		if r, err := Dial(t.Context(), c.path, c.t); err == nil {
			r.Close()
			t.Errorf("%s: dialled", name)
		}
	}
}

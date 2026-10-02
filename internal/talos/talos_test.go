package talos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
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

// failingState is a COSI state whose Get fails with err; nothing else of it is called.
type failingState struct {
	state.State
	err error
}

func (s failingState) Get(context.Context, resource.Pointer, ...state.GetOption) (resource.Resource, error) {
	return nil, s.err
}

// failingMachine is a machine service whose Version fails with err.
type failingMachine struct {
	machine.MachineServiceClient
	err error
}

func (m failingMachine) Version(context.Context, *emptypb.Empty, ...grpc.CallOption) (*machine.VersionResponse, error) {
	return nil, m.err
}

// TestErrorsDoNotQuoteUpstream: machinery decodes the configuration before it returns it, and a
// decoder error quotes the rejected YAML, i.e. the configuration's secrets; a talosconfig parse
// error quotes the file, which holds the client key. This package's errors keep neither, and keep
// the gRPC code and a context error.
func TestErrorsDoNotQuoteUpstream(t *testing.T) {
	const mark = "UPSTRM" // short: the YAML decoder quotes 7 characters of a value, then "..."
	_, decodeErr := configloader.NewFromBytes([]byte("version: v1alpha1\nmachine:\n  unknownField: " + mark + "\n"))
	tc := []byte("context: a\ncontexts:\n  a:\n    endpoints: " + mark + "\n")
	_, openErr := clientconfig.FromBytes(tc)
	for what, err := range map[string]error{"decode": decodeErr, "talosconfig": openErr} {
		if err == nil || !strings.Contains(err.Error(), mark) {
			t.Fatalf("control: machinery's %s error does not quote its input, so this test proves nothing: %v", what, err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for name, c := range map[string]struct {
		ctx  context.Context
		err  error
		code codes.Code
	}{
		"decode":   {t.Context(), decodeErr, codes.Unknown},
		"denied":   {t.Context(), status.Error(codes.PermissionDenied, mark), codes.PermissionDenied},
		"canceled": {canceled, fmt.Errorf("%s: %w", mark, context.Canceled), codes.Unknown},
	} {
		r := &reader{api: &client.Client{COSI: failingState{err: c.err}, MachineClient: failingMachine{err: c.err}}}
		_, mcErr := r.MachineConfig(c.ctx)
		_, vErr := r.Version(c.ctx)
		for op, err := range map[string]error{"MachineConfig": mcErr, "Version": vErr} {
			switch {
			case err == nil || strings.Contains(err.Error(), mark):
				t.Errorf("%s %s: %v", name, op, err)
			case status.Code(err) != c.code:
				t.Errorf("%s %s: code %s, want %s", name, op, status.Code(err), c.code)
			case (c.ctx.Err() != nil) != errors.Is(err, context.Canceled):
				t.Errorf("%s %s: errors.Is(context.Canceled) is wrong: %v", name, op, err)
			}
		}
	}
	if _, err := Dial(t.Context(), tc, "10.55.0.2"); err == nil || strings.Contains(err.Error(), mark) {
		t.Errorf("Dial: %v", err)
	}
}

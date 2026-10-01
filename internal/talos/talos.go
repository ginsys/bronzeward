// Package talos reads a node's machine configuration and Talos version through the Talos Go
// machinery, in process: a talosctl subprocess would put the configuration on a pipe. The
// machinery client can also apply, reset, reboot and upgrade; this package exposes none of that.
// Its surface is Reader's three methods, and guard_test.go holds the package's source to an
// allowlist of machinery calls so that a mutating call cannot be added unnoticed.
//
// Where a machine's address and credential come from is not decided (the caller supplies both).
package talos

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/client"
	cfgres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Target is the Talos API endpoint a request goes through and the node it is about; they differ
// when one node's apid proxies for another.
type Target struct {
	Endpoint string
	Node     string
}

// Reader reads one node. It holds no call that changes the node.
type Reader interface {
	// MachineConfig reads the node's active machine configuration (the v1alpha1 resource).
	MachineConfig(ctx context.Context) (Config, error)
	// Version is the node's Talos tag.
	Version(ctx context.Context) (string, error)
	Close() error
}

// Dial makes a Reader from the talosconfig file at talosconfigPath, through t's endpoint to t's
// node. It reads the file but makes no request; a request's deadline is its context's.
func Dial(ctx context.Context, talosconfigPath string, t Target) (Reader, error) {
	if talosconfigPath == "" || t.Endpoint == "" || t.Node == "" {
		return nil, errors.New("talos: a talosconfig path, an endpoint and a node are required")
	}
	c, err := client.New(ctx, client.WithConfigFromFile(talosconfigPath), client.WithEndpoints(t.Endpoint))
	if err != nil {
		// The machinery's text can quote the talosconfig, which holds the client key.
		return nil, fmt.Errorf("talos: no client from the talosconfig %s", talosconfigPath)
	}
	return &reader{api: c, node: t.Node}, nil
}

type reader struct {
	api  *client.Client
	node string
}

// requestError is a failed node request in this package's own words. The machinery's error text
// is not kept: it decodes the configuration before returning it, and a decoder error quotes the
// rejected YAML, i.e. the configuration's secrets. The gRPC code (status.Code) and a context error
// (errors.Is) are kept.
type requestError struct {
	what string
	code codes.Code
	ctx  error
}

func newRequestError(ctx context.Context, what string, err error) error {
	return &requestError{what: what, code: status.Code(err), ctx: ctx.Err()}
}

func (e *requestError) Error() string {
	s := "talos: " + e.what + ": " + e.code.String()
	if e.ctx != nil {
		s += ": " + e.ctx.Error()
	}
	return s
}

func (e *requestError) GRPCStatus() *status.Status { return status.New(e.code, e.code.String()) }
func (e *requestError) Unwrap() error              { return e.ctx }

func (r *reader) MachineConfig(ctx context.Context) (Config, error) {
	mc, err := safe.StateGetByID[*cfgres.MachineConfig](client.WithNode(ctx, r.node), r.api.COSI, cfgres.ActiveID)
	if err != nil {
		return Config{}, newRequestError(ctx, "reading the machine configuration", err)
	}
	b, err := mc.Provider().Bytes()
	if err != nil {
		return Config{}, errors.New("talos: the machine configuration could not be encoded")
	}
	return newConfig(b, mc.Metadata().Version().String()), nil
}

func (r *reader) Version(ctx context.Context) (string, error) {
	resp, err := r.api.Version(client.WithNode(ctx, r.node))
	if err != nil {
		return "", newRequestError(ctx, "version", err)
	}
	msgs := resp.GetMessages()
	if len(msgs) != 1 {
		return "", fmt.Errorf("talos: version: %d answers for one node", len(msgs))
	}
	if msgs[0].GetMetadata().GetError() != "" {
		return "", errors.New("talos: version: the node answered an error")
	}
	tag := msgs[0].GetVersion().GetTag()
	if tag == "" {
		return "", errors.New("talos: version: no tag")
	}
	return tag, nil
}

func (r *reader) Close() error { return r.api.Close() }

// Config is a machine configuration as read, with its secrets: it is the unresolved input
// ingestion sanitizes, a stand-in until that type exists. It does not render: every fmt verb
// prints a placeholder and the marshallers fail. The bytes sit behind a pointer so that printing
// a struct that holds a Config in an unexported field shows an address, not the bytes. Bytes is
// the one way out, and guard_test.go limits its callers to this package and internal/ingest.
type Config struct {
	b  *[]byte
	rv string
}

func newConfig(b []byte, resourceVersion string) Config {
	c := append([]byte(nil), b...)
	return Config{b: &c, rv: resourceVersion}
}

// Bytes is a copy of the configuration.
func (c Config) Bytes() []byte {
	if c.b == nil {
		return nil
	}
	return append([]byte(nil), *c.b...)
}

// ResourceVersion is the COSI resource version the configuration was read at.
func (c Config) ResourceVersion() string { return c.rv }

const placeholder = "[talos machine configuration]"

var errRender = errors.New("talos: a machine configuration is not marshalled")

func (Config) String() string               { return placeholder }
func (Config) GoString() string             { return placeholder }
func (Config) Format(f fmt.State, _ rune)   { fmt.Fprint(f, placeholder) }
func (Config) MarshalJSON() ([]byte, error) { return nil, errRender }
func (Config) MarshalText() ([]byte, error) { return nil, errRender }
func (Config) MarshalYAML() (any, error)    { return nil, errRender }

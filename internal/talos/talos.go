// Package talos reads a node's machine configuration and Talos version through the Talos Go
// machinery, in process: a talosctl subprocess would put the configuration on a pipe. The
// machinery client can also apply, reset, reboot and upgrade; this package exposes none of that.
// Its surface is Reader's three methods, and guard_test.go holds the package's source to an
// allowlist of machinery calls so that a mutating call cannot be added unnoticed.
//
// The caller supplies the cluster's talosconfig, as read from the provider, and the machine's
// endpoint (persistence-api §3.3). The client dials that endpoint only and sends no node metadata,
// so the request is about the node that answers it, whatever the talosconfig names.
package talos

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	cfgres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Reader reads one node. It holds no call that changes the node.
type Reader interface {
	// MachineConfig reads the node's active machine configuration (the v1alpha1 resource).
	MachineConfig(ctx context.Context) (Config, error)
	// Version is the node's Talos tag.
	Version(ctx context.Context) (string, error)
	Close() error
}

// Dial makes a Reader for the node at endpoint (ParseEndpoint's grammar) from a talosconfig's
// bytes. Only the current context's certificate authority, client certificate and key are used:
// its endpoints, nodes and auth block are not, and nothing is read from or written to disk. It
// makes no request; a request's deadline is its context's. Its errors never quote the talosconfig,
// which holds the client key.
func Dial(ctx context.Context, talosconfig []byte, endpoint string) (Reader, error) {
	ep, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	creds, err := currentCredentials(talosconfig)
	if err != nil {
		return nil, err
	}
	c, err := client.New(ctx, client.WithConfigContext(creds), client.WithEndpoints(ep))
	if err != nil {
		return nil, errors.New("talos: the talosconfig's certificate authority, certificate or key is unusable")
	}
	return &reader{api: c}, nil
}

// currentCredentials is the certificate authority, certificate and key of the talosconfig's current
// context, and nothing else from it. The machinery's parser panics on some malformed documents (it
// dereferences a null context while upgrading one: machinery v1.13.6, client/config/config.go:81);
// a credential read from the provider is input, so a panic is that document's refusal, its value
// dropped unread.
func currentCredentials(talosconfig []byte) (creds *clientconfig.Context, err error) {
	notTalosconfig := errors.New("talos: the talosconfig is not a talosconfig document")
	defer func() {
		if recover() != nil {
			creds, err = nil, notTalosconfig
		}
	}()
	cfg, err := clientconfig.FromBytes(talosconfig)
	if err != nil {
		return nil, notTalosconfig
	}
	cur := cfg.Contexts[cfg.Context]
	if cur == nil || cur.CA == "" || cur.Crt == "" || cur.Key == "" {
		return nil, errors.New("talos: the talosconfig's current context has no certificate authority, certificate and key")
	}
	return &clientconfig.Context{CA: cur.CA, Crt: cur.Crt, Key: cur.Key}, nil
}

type reader struct {
	api *client.Client
}

// direct is ctx without node routing metadata. apid forwards a request carrying node or nodes to
// the nodes named there; the request must be answered by the node at the dialled endpoint
// (persistence-api §3.3), whatever the caller's context holds. Other metadata is kept.
func direct(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return ctx
	}
	md = md.Copy()
	md.Delete("node")
	md.Delete("nodes")
	return metadata.NewOutgoingContext(ctx, md)
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
	mc, err := safe.StateGetByID[*cfgres.MachineConfig](direct(ctx), r.api.COSI, cfgres.ActiveID)
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
	resp, err := r.api.Version(direct(ctx))
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

// Config is a machine configuration as read, with its secrets; ingest.FromTalos turns it into the
// unresolved input ingestion sanitizes. It does not render: every fmt verb
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

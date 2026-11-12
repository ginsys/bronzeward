package discover

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// defaultPort is the Talos API port an endpoint without one is dialled on.
const defaultPort = "50000"

var errEndpoint = errors.New("discover: an endpoint is an IP literal (an IPv6 literal in brackets) or a DNS name, with an optional port from 1 to 65535")

// parseEndpoint is internal/talos.ParseEndpoint's grammar, copied because this package must not
// import internal/talos (it would link the machine configuration reader into the CLI);
// endpoint_test.go holds the two to the same answers.
func parseEndpoint(s string) (string, error) {
	h, p := s, defaultPort
	if i := strings.LastIndexByte(s, ':'); i >= 0 && (strings.HasSuffix(s[:i], "]") || strings.Count(s, ":") == 1) {
		h, p = s[:i], s[i+1:]
	}
	bracket := strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]")
	if bracket {
		h = h[1 : len(h)-1]
	}
	a, err := netip.ParseAddr(h)
	n, perr := strconv.Atoi(p)
	if err != nil || perr != nil || a.Zone() != "" || a.Is4() == bracket || p[0] < '1' || p[0] > '9' || n > 65535 {
		return "", errEndpoint
	}
	return netip.AddrPortFrom(a, uint16(n)).String(), nil
}

// ErrUnknownContext is the refusal for a context name that the talosconfig does not hold.
var ErrUnknownContext = errors.New("discover: the talosconfig has no context of that name")

// chosen is the context of talosconfig called name, or its current context when name is "", and that
// context's name. The machinery's parser panics on some malformed documents, so a panic is that
// document's refusal. The errors never quote the talosconfig, which holds the key.
func chosen(talosconfig []byte, name string) (c *clientconfig.Context, picked string, err error) {
	notTalosconfig := errors.New("discover: the talosconfig is not a talosconfig document")
	defer func() {
		if recover() != nil {
			c, picked, err = nil, "", notTalosconfig
		}
	}()
	cfg, err := clientconfig.FromBytes(talosconfig)
	if err != nil {
		return nil, "", notTalosconfig
	}
	named := name != ""
	if !named {
		name = cfg.Context
	}
	c, ok := cfg.Contexts[name]
	if !ok && named {
		return nil, "", ErrUnknownContext
	}
	if c == nil || c.CA == "" || c.Crt == "" || c.Key == "" {
		return nil, "", errors.New("discover: the talosconfig's context has no certificate authority, certificate and key")
	}
	return c, name, nil
}

// credentials are the certificate authority, certificate and key of the talosconfig's context called
// name (the current one when name is ""), and its first endpoint ("" when it names none): its other
// endpoints, its nodes, its auth block and every other context are not used, and nothing is read from
// or written to disk.
func credentials(talosconfig []byte, name string) (creds *clientconfig.Context, endpoint string, err error) {
	c, _, err := chosen(talosconfig, name)
	if err != nil {
		return nil, "", err
	}
	if len(c.Endpoints) > 0 {
		endpoint = c.Endpoints[0]
	}
	return &clientconfig.Context{CA: c.CA, Crt: c.Crt, Key: c.Key}, endpoint, nil
}

// OneContext is a talosconfig of one context: the one called name in talosconfig (the current one when
// name is ""), with its certificate authority, client certificate and key. Its endpoints, nodes, auth
// block and every other context are left out, so that no other cluster's key goes where the result goes.
// The server's reader reads the current context only, and the result names that one. A name that the
// file does not hold is ErrUnknownContext.
func OneContext(talosconfig []byte, name string) ([]byte, error) {
	creds, picked, err := chosen(talosconfig, name)
	if err != nil {
		return nil, err
	}
	out, err := (&clientconfig.Config{Context: picked, Contexts: map[string]*clientconfig.Context{
		picked: {CA: creds.CA, Crt: creds.Crt, Key: creds.Key},
	}}).Bytes()
	if err != nil {
		return nil, errors.New("discover: the talosconfig could not be written")
	}
	return out, nil
}

// node is a client for one node. The source guard holds the package to using the client through
// this field and nothing else of it than its reads.
type node struct {
	api *client.Client
}

func (n *node) close() { n.api.Close() } //nolint:errcheck // a read-only client has nothing to flush

// dial makes a node for host:port. It sends no node metadata, so the node that answers is the one
// dialled, whatever the talosconfig names. It makes no request.
func dial(ctx context.Context, creds *clientconfig.Context, endpoint string) (*node, error) {
	c, err := client.New(ctx, client.WithConfigContext(creds), client.WithEndpoints(endpoint))
	if err != nil {
		return nil, errors.New("discover: the talosconfig's certificate authority, certificate or key is unusable")
	}
	return &node{api: c}, nil
}

// failed is a failed node request in this package's own words: the machinery's error text is not
// kept, only the gRPC code (a missing resource, which COSI's client reports with its own error,
// is NotFound).
func failed(what string, err error) error {
	code := status.Code(err)
	if state.IsNotFoundError(err) {
		code = codes.NotFound
	}
	return fmt.Errorf("discover: %s: %s", what, code)
}

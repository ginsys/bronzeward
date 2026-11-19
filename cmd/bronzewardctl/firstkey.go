package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// maxFirstKey is the most a first key's file may hold: the document limit of the route.
const maxFirstKey = 512 << 10

// clusterFirstKey supplies or replaces a cluster's first key: one POST /clusters/{id}/first-keys, a
// human author act (operator-experience.md §5.2). The file is read once, with a limit, cut to one
// context where it is a talosconfig, sent once and never printed, logged or copied. The server tries
// the key at --endpoint before it stores anything, and answers with a rule it refused by.
func (e *env) clusterFirstKey(args []string) error {
	var cluster, kind, file, endpoint, talosContext string
	var rf repeatFlags
	if _, err := e.parse("cluster first-key", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&cluster, "cluster", "", "")
		fs.StringVar(&kind, "kind", "", "")
		fs.StringVar(&file, "file", "", "")
		fs.StringVar(&endpoint, "endpoint", "", "")
		fs.StringVar(&talosContext, "talos-context", "", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	switch {
	case cluster == "" || file == "" || endpoint == "":
		return usageError{errors.New("cluster first-key needs --cluster, --kind, --file and --endpoint")}
	case !slices.Contains([]string{"talosconfig", "machine-config", "secrets-bundle"}, kind):
		return usageError{errors.New("--kind is talosconfig, machine-config or secrets-bundle")}
	case talosContext != "" && kind != "talosconfig":
		return usageError{errors.New("--talos-context cuts a talosconfig, so it is given with --kind talosconfig only")}
	}
	doc, err := firstKeyDocument(file, kind, talosContext)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(apitypes.FirstKeyInput{Document: doc, Endpoint: endpoint, Kind: apitypes.FirstKeyInputKind(kind)})
	stop, disarm := e.arm() // the one write that takes the Ctrl-C: the request can stall at the node
	defer disarm()
	return firstKeyRefusal(e.write(&client.Write{Method: http.MethodPost, Path: "/clusters/" + url.PathEscape(cluster) + "/first-keys", Body: body,
		HumanOnly: true, Repeat: rp, Once: true, Stop: stop, Check: "act list"},
		func(_ *client.Client, r *client.Response) ([]string, error) {
			var x apitypes.FirstKeyBody
			err := json.Unmarshal(r.Body, &x)
			if x.Replaced {
				return []string{sf("Cluster %s: first key (%s) stored as CA key version %d, replacing the earlier key.", x.Cluster, x.Kind, x.CaKeyVersion.Version)}, err
			}
			return []string{sf("Cluster %s: first key (%s) stored as CA key version %d.", x.Cluster, x.Kind, x.CaKeyVersion.Version)}, err
		}))
}

// firstKeyDocument is the text to send: the file, or for a talosconfig its one context. Every error
// names the flag and quotes nothing of the file.
func firstKeyDocument(file, kind, talosContext string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", usageError{fmt.Errorf("--file cannot be read: %w", unwrapPath(err))}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFirstKey+1))
	switch {
	case err != nil:
		return "", usageError{errors.New("--file cannot be read")}
	case len(b) > maxFirstKey:
		return "", usageError{fmt.Errorf("--file holds more than %d KiB, which is more than a first key", maxFirstKey>>10)}
	}
	if kind == "talosconfig" {
		if b, err = discover.OneContext(b, talosContext); errors.Is(err, discover.ErrUnknownContext) {
			return "", usageError{errors.New("--talos-context names a context that the talosconfig does not hold")}
		} else if err != nil {
			return "", usageError{errors.New("--file is not a talosconfig this client can use")}
		}
	}
	if len(b) == 0 || !utf8.Valid(b) {
		return "", usageError{errors.New("--file holds no text that a first key can be")}
	}
	return string(b), nil
}

// firstKeyRefusal says what the route refused by, naming the rule and nothing the server's Talos read
// gave: the route sends neither the document nor a node's words, and this client prints neither.
func firstKeyRefusal(err error) error {
	var p *client.Problem
	if !errors.As(err, &p) {
		return err
	}
	rule, _ := p.Doc["rule"].(string)
	switch {
	case p.Code() == "validation-failed" && rule != "":
		return fmt.Errorf("%s: %s answered %d: the first key is refused by the rule %s. Nothing was stored", p.Where, p.Route, p.Status, client.Printable(rule))
	case p.Code() == "talos-access-unavailable":
		return fmt.Errorf("%s: %s answered %d: the key could not be tried at that endpoint. Nothing was stored", p.Where, p.Route, p.Status)
	}
	return err
}

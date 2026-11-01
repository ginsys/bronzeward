package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// repeatFlags are decision T10's flags (#256), which every write command takes: the values an
// outcome-unknown block printed, for repeating exactly that request. On a draft write --if-match
// alone gives the ETag to send instead of the kept one (T11).
type repeatFlags struct{ key, epoch, principal, ifMatch given }

// given is a flag's value and whether it was given at all, so an empty value is told from none.
type given struct {
	v   string
	set bool
}

func (g *given) String() string     { return g.v }
func (g *given) Set(v string) error { g.v, g.set = v, true; return nil }

func (r *repeatFlags) flags(fs *flag.FlagSet) {
	fs.Var(&r.key, "idempotency-key", "")
	fs.Var(&r.epoch, "expect-epoch", "")
	fs.Var(&r.principal, "expect-principal", "")
	fs.Var(&r.ifMatch, "if-match", "")
}

// repeat is the repeat the flags give, nil for a first send. It refuses, sending nothing, when one
// is missing or given empty, as a script's unset variable gives it (client-contracts.md §8.4 item
// 4); a route without If-Match takes no --if-match.
func (r *repeatFlags) repeat(takesIfMatch bool) (*client.Repeat, error) {
	need := "--idempotency-key, --expect-epoch and --expect-principal"
	if takesIfMatch {
		need = "--idempotency-key, --expect-epoch, --expect-principal and --if-match"
	}
	for name, g := range map[string]given{"idempotency-key": r.key, "expect-epoch": r.epoch, "expect-principal": r.principal, "if-match": r.ifMatch} {
		if g.set && g.v == "" {
			return nil, usageError{fmt.Errorf("--%s was given an empty value: give it as printed; nothing was sent", name)}
		}
	}
	switch {
	case r.ifMatch.set && !takesIfMatch:
		return nil, usageError{errors.New("this route takes no If-Match, so --if-match is not given")}
	case !r.key.set && !r.epoch.set && !r.principal.set:
		return nil, nil
	case !r.key.set || !r.epoch.set || !r.principal.set || takesIfMatch && !r.ifMatch.set:
		return nil, usageError{fmt.Errorf("a repeat needs %s, each as the outcome-unknown block printed it; nothing was sent", need)}
	case !client.KeyShape.MatchString(r.key.v):
		return nil, usageError{errors.New("--idempotency-key: give the key as printed, 16 to 128 of A-Z, a-z, 0-9, - and _; nothing was sent")}
	}
	return &client.Repeat{Key: r.key.v, Epoch: r.epoch.v, Principal: r.principal.v}, nil
}

// write sends w on the selected context and prints the answered resource: -o json and yaml print
// it unchanged, the table say's lines. Standard output stays empty unless the write succeeded. The
// write succeeded whatever its answer holds, so an answer say cannot read is printed raw by -o json
// and yaml, the failure goes to standard error, and the command exits 0.
func (e *env) write(w *client.Write, say func(*client.Client, *client.Response) ([]string, error)) error {
	return e.writeAfter(nil, w, say)
}

// writeAfter is write, with prep run on the opened client before the write is sent: the reads a
// command needs to build the write's body.
func (e *env) writeAfter(prep func(*client.Client) error, w *client.Write, say func(*client.Client, *client.Response) ([]string, error)) (err error) {
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	// A repeat is refused after item 3's reads alone, before prep's (§8.4 item 4).
	if err := c.CheckRepeat(w); err != nil {
		return err
	}
	if prep != nil {
		if err := prep(c); err != nil {
			return err
		}
	}
	r, err := c.Send(w)
	if err != nil {
		return err
	}
	lines, err := say(c, r)
	var out []byte
	if err == nil && e.output != "table" {
		// say can succeed without reading the answer: one that is not JSON is the same unreadable
		// answer, which -o yaml would otherwise convert or fail on.
		if len(r.Body) > 0 && !json.Valid(r.Body) {
			err = errors.New("the answer is not JSON")
		} else {
			out, err = e.render(r.Body)
		}
	}
	if err != nil {
		fmt.Fprintf(e.err, "bronzewardctl: %s: %s /api/v1%s succeeded, but its answer cannot be read: %v\n", c.Context.Where(), w.Method, w.Path, err)
		if e.output == "table" {
			return nil
		}
		// The answer as served, unconverted for -o json and yaml alike (#347), chosen in memory, so it
		// is written once and a write failure is reported.
		if out = r.Body; !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
	}
	if out != nil {
		_, err = e.out.Write(out)
		return err
	}
	return e.emit(r.Body, func(tw *tabwriter.Writer) {
		for _, l := range lines {
			fmt.Fprintln(tw, l)
		}
	})
}

// find reads every page of path and names the item match names, or says there is no such item,
// a what. An item that does not decode fails the read: it could be the one looked for.
func find[T any](c *client.Client, path, what string, match func(T) string) (string, error) {
	items, err := c.List(path)
	if err != nil {
		return "", err
	}
	for i, raw := range items {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", fmt.Errorf("GET /api/v1%s: item %d cannot be read: %w", path, i, err)
		}
		if match(v) != "" {
			return fmt.Sprintf("GET /api/v1%s shows %s", path, match(v)), nil
		}
	}
	return fmt.Sprintf("GET /api/v1%s shows no %s", path, what), nil
}

// clusterAdd records a cluster (operator-experience.md §10: POST /clusters, author, human only).
func (e *env) clusterAdd(args []string) error {
	var in apitypes.ClusterInput
	var rf repeatFlags
	if _, err := e.parse("cluster add", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&in.Name, "name", "", "")
		fs.StringVar(&in.Endpoint, "endpoint", "", "")
		fs.StringVar(&in.Contract, "contract", "", "")
		fs.StringVar(&in.TalosClusterId, "talos-cluster-id", "", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(in)
	return e.write(&client.Write{Method: http.MethodPost, Path: "/clusters", Body: body, HumanOnly: true, Repeat: rp,
		Check: "cluster list",
		Read: func(c *client.Client) (string, error) {
			return find(c, "/clusters", "cluster with this Talos cluster ID", func(x apitypes.ClusterBody) string {
				return map[bool]string{true: "the cluster " + x.Id + " (" + x.Name + ") with this Talos cluster ID"}[x.TalosClusterId == in.TalosClusterId]
			})
		}}, func(_ *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.ClusterBody
		err := json.Unmarshal(r.Body, &x)
		return []string{fmt.Sprintf("Cluster %s recorded: %s, endpoint %s, contract %s, Talos cluster ID %s.",
			x.Id, x.Name, x.Endpoint, x.Contract, x.TalosClusterId)}, err
	})
}

// machineAdd records a machine in a cluster, its Talos endpoint stored with the default port 50000
// (operator-experience.md §5.2, §10: POST /machines, author, human only).
func (e *env) machineAdd(args []string) error {
	var in apitypes.MachineInput
	var nodeID, uuid, serial, platform string
	var rf repeatFlags
	if _, err := e.parse("machine add", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&in.Cluster, "cluster", "", "")
		fs.StringVar(&nodeID, "talos-node-id", "", "")
		fs.StringVar(&uuid, "smbios-uuid", "", "")
		fs.StringVar(&serial, "serial", "", "")
		fs.StringVar(&platform, "platform", "", "")
		fs.StringVar(&in.TalosEndpoint, "talos-endpoint", "", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	in.Platform = apitypes.MachineInputPlatform(platform)
	if nodeID != "" {
		in.TalosNodeId = &nodeID
	}
	if uuid != "" {
		in.SmbiosUuid = &uuid
	}
	if serial != "" {
		in.Serial = &serial
	}
	body, _ := json.Marshal(in)
	path := "/machines?" + url.Values{"cluster": {in.Cluster}}.Encode()
	return e.write(&client.Write{Method: http.MethodPost, Path: "/machines", Body: body, HumanOnly: true, Repeat: rp,
		Check: "machine list --cluster " + client.ShellWord(in.Cluster),
		Read: func(c *client.Client) (string, error) {
			return find(c, path, "machine with this identity", func(m apitypes.MachineBody) string {
				h := m.Hardware
				same := h.TalosNodeId != nil && *h.TalosNodeId == nodeID || h.SmbiosUuid != nil && strings.EqualFold(*h.SmbiosUuid, uuid)
				return map[bool]string{true: "the machine " + m.Id + " with this identity"}[same]
			})
		}}, func(c *client.Client, r *client.Response) ([]string, error) {
		var m apitypes.MachineBody
		if err := json.Unmarshal(r.Body, &m); err != nil {
			return nil, err
		}
		// The cluster's name is shown when its read succeeds; the write is done either way.
		cluster := m.Cluster
		if r, err := c.Get("/clusters/" + url.PathEscape(m.Cluster)); err == nil {
			var x apitypes.ClusterBody
			if json.Unmarshal(r.Body, &x) == nil {
				cluster += " (" + x.Name + ")"
			}
		}
		return []string{fmt.Sprintf("Machine %s recorded in cluster %s. Endpoint %s.", m.Id, cluster, m.TalosEndpoint),
			"Next: give Bronzeward the cluster's Talos access in the provider, then import its configuration."}, nil
	})
}

// machineSetEndpoint replaces a machine's Talos endpoint (operator-experience.md §10:
// POST /machines/{id}/talos-endpoints, author, human only).
func (e *env) machineSetEndpoint(args []string) error {
	var in apitypes.EndpointInput
	var rf repeatFlags
	pos, err := e.parse("machine set-endpoint", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&in.TalosEndpoint, "talos-endpoint", "", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(in)
	m := url.PathEscape(pos[0])
	return e.write(&client.Write{Method: http.MethodPost, Path: "/machines/" + m + "/talos-endpoints", Body: body, HumanOnly: true,
		Repeat: rp, Check: "machine timeline " + client.ShellWord(pos[0]),
		Read: func(c *client.Client) (string, error) {
			r, err := c.Get("/machines/" + m)
			if err != nil {
				return "", err
			}
			var x apitypes.MachineBody
			err = json.Unmarshal(r.Body, &x)
			return "GET /api/v1/machines/" + m + " shows the Talos endpoint " + x.TalosEndpoint, err
		}}, func(_ *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.EndpointChangeBody
		err := json.Unmarshal(r.Body, &x)
		return []string{fmt.Sprintf("Machine %s: Talos endpoint %s, was %s (timeline revision %d).",
			x.Machine, x.TalosEndpoint, x.PreviousEndpoint, x.Revision)}, err
	})
}

package main

import (
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
// outcome-unknown block printed, for repeating exactly that request.
type repeatFlags struct{ key, epoch, principal, ifMatch string }

func (r *repeatFlags) flags(fs *flag.FlagSet) {
	fs.StringVar(&r.key, "idempotency-key", "", "")
	fs.StringVar(&r.epoch, "expect-epoch", "", "")
	fs.StringVar(&r.principal, "expect-principal", "", "")
	fs.StringVar(&r.ifMatch, "if-match", "", "")
}

// repeat is the repeat the flags give, nil for a first send. It refuses, sending nothing, when one
// is missing (client-contracts.md §8.4 item 4); a route without If-Match takes no --if-match.
func (r *repeatFlags) repeat(takesIfMatch bool) (*client.Repeat, error) {
	need := "--idempotency-key, --expect-epoch and --expect-principal"
	if takesIfMatch {
		need = "--idempotency-key, --expect-epoch, --expect-principal and --if-match"
	}
	switch {
	case r.ifMatch != "" && !takesIfMatch:
		return nil, usageError{errors.New("this route takes no If-Match, so --if-match is not given")}
	case r.key == "" && r.epoch == "" && r.principal == "" && r.ifMatch == "":
		return nil, nil
	case r.key == "" || r.epoch == "" || r.principal == "" || takesIfMatch && r.ifMatch == "":
		return nil, usageError{fmt.Errorf("a repeat needs %s, each as the outcome-unknown block printed it; nothing was sent", need)}
	case !client.KeyShape.MatchString(r.key):
		return nil, usageError{errors.New("--idempotency-key: give the key as printed, 16 to 128 of A-Z, a-z, 0-9, - and _; nothing was sent")}
	}
	return &client.Repeat{Key: r.key, Epoch: r.epoch, Principal: r.principal}, nil
}

// write sends w on the selected context and prints the answered resource: -o json and yaml print
// it unchanged, the table say's lines. Standard output stays empty unless the write succeeded.
func (e *env) write(w *client.Write, say func(*client.Client, []byte) ([]string, error)) (err error) {
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	r, err := c.Send(w)
	if err != nil {
		return err
	}
	lines, err := say(c, r.Body)
	if err != nil {
		return fmt.Errorf("%s: %s succeeded, but its answer: %w", c.Context.Where(), w.Path, err)
	}
	return e.emit(r.Body, func(tw *tabwriter.Writer) {
		for _, l := range lines {
			fmt.Fprintln(tw, l)
		}
	})
}

// find reads every page of path and names the item match names, or says there is no such item,
// a what.
func find[T any](c *client.Client, path, what string, match func(T) string) (string, error) {
	items, err := c.List(path)
	if err != nil {
		return "", err
	}
	for _, raw := range items {
		var v T
		if json.Unmarshal(raw, &v) == nil && match(v) != "" {
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
		}}, func(_ *client.Client, raw []byte) ([]string, error) {
		var x apitypes.ClusterBody
		err := json.Unmarshal(raw, &x)
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
		}}, func(c *client.Client, raw []byte) ([]string, error) {
		var m apitypes.MachineBody
		if err := json.Unmarshal(raw, &m); err != nil {
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
		}}, func(_ *client.Client, raw []byte) ([]string, error) {
		var x apitypes.EndpointChangeBody
		err := json.Unmarshal(raw, &x)
		return []string{fmt.Sprintf("Machine %s: Talos endpoint %s, was %s (timeline revision %d).",
			x.Machine, x.TalosEndpoint, x.PreviousEndpoint, x.Revision)}, err
	})
}

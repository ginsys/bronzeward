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
	"github.com/ginsys/bronzeward/internal/clusterid"
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

// args are the flags given, as arguments for the command a composite runs; an empty value stays
// given, so that command refuses it.
func (r *repeatFlags) args() []string {
	var a []string
	for _, f := range []struct {
		name string
		g    given
	}{{"idempotency-key", r.key}, {"expect-epoch", r.epoch}, {"expect-principal", r.principal}, {"if-match", r.ifMatch}} {
		if f.g.set {
			a = append(a, "--"+f.name+"="+f.g.v)
		}
	}
	return a
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
	defer func() { err = joinStoredSave(err, c.Save()) }()
	return e.writeWith(c, prep, w, say)
}

// joinStoredSave adds the error of saving the context to what a write ended with. writeWith returns nil
// only after the server acknowledged the write, so a save error beside a nil err is a write that is
// stored: it is a saveFailed that says so, which the callers that go on after the write read. Beside a
// write's own error it stays plain, so that error keeps its class and exit.
func joinStoredSave(err, saved error) error {
	if saved != nil && err == nil {
		saved = saveFailed{afterEnd{fmt.Errorf("the write is stored; only saving the context failed, which a later command that reads the server keeps again: %w", saved)}}
	}
	return errors.Join(err, saved)
}

// writeWith is writeAfter on a client already open, which the caller saves.
func (e *env) writeWith(c *client.Client, prep func(*client.Client) error, w *client.Write, say func(*client.Client, *client.Response) ([]string, error)) error {
	// A repeat is refused after item 3's reads alone, before prep's (§8.4 item 4).
	if err := c.CheckRepeat(w); err != nil {
		return err
	}
	if prep != nil {
		if err := prep(c); err != nil {
			return err
		}
	}
	if e.runEpoch != "" && w.Repeat == nil && !w.Entry && w.Reads == "" {
		// A run of several writes (cluster onboard) sends each under the epoch its first read showed; a write
		// that binds itself keeps its own bind. Send refuses the write, sending nothing, once the context shows another.
		w.ReadsAt(e.runEpoch, c.EpochMoves())
	}
	r, err := c.Send(w)
	if err != nil {
		return err
	}
	lines, err := say(c, r)
	if e.collect != nil && e.output != "table" {
		// A composite prints one object at its end (composite.go): the answer is kept as served for it,
		// and nothing is written for this step.
		if !json.Valid(r.Body) {
			err = errors.New("the answer is not JSON")
		}
		if err != nil {
			e.unreadable(c, w, err)
		}
		e.collect.record(r.Body, err)
		return nil
	}
	var out []byte
	if err == nil && e.output != "table" {
		// say can succeed without reading the answer: one that is not JSON, an empty one included, is
		// the same unreadable answer, which -o yaml would otherwise convert or fail on. Every write
		// route answers with a body.
		if !json.Valid(r.Body) {
			err = errors.New("the answer is not JSON")
		} else {
			out, err = e.render(r.Body)
		}
	}
	if err != nil {
		e.unreadable(c, w, err)
		if e.output == "table" {
			return nil
		}
		// The answer as served, unconverted for -o json and yaml alike (#347) but for the escape that
		// -o json gets (-o yaml has none; client-contracts.md §6 item 8), chosen in memory, so it
		// is written once and a write failure is reported.
		out = r.Body
		if e.output == "json" {
			out = client.EscapeJSON(out)
		}
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
	}
	if out != nil {
		_, err = e.out.Write(out)
		return err
	}
	return e.emit(r.Body, func(tw *tabwriter.Writer) {
		for _, l := range lines {
			fmt.Fprintln(tw, client.Printable(l)) // a sentence a write built without sf is escaped here
		}
	})
}

// unreadable says on standard error that w succeeded, but its answer cannot be read.
func (e *env) unreadable(c *client.Client, w *client.Write, err error) {
	tf(e.err, "bronzewardctl: %s: %s /api/v1%s succeeded, but its answer cannot be read: %v\n", c.Context.Where(), w.Method, w.Path, err)
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
			return "", ef("GET /api/v1%s: item %d cannot be read: %w", path, i, err)
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
	return e.write(clusterWrite(in, rp), func(_ *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.ClusterBody
		err := json.Unmarshal(r.Body, &x)
		return []string{sf("Cluster %s recorded: %s, endpoint %s, contract %s, Talos cluster ID %s.",
			x.Id, x.Name, x.Endpoint, x.Contract, x.TalosClusterId)}, err
	})
}

// clusterWrite is the write that records a cluster, which cluster add and cluster onboard send.
func clusterWrite(in apitypes.ClusterInput, rp *client.Repeat) *client.Write {
	body, _ := json.Marshal(in)
	return &client.Write{Method: http.MethodPost, Path: "/clusters", Body: body, HumanOnly: true, Repeat: rp,
		Check: "cluster list",
		Read: func(c *client.Client) (string, error) {
			// The server records the standard spelling of the ID, whichever alphabet the operator typed.
			want := in.TalosClusterId
			if s, ok := clusterid.Canonical(want); ok {
				want = s
			}
			return find(c, "/clusters", "cluster with this Talos cluster ID", func(x apitypes.ClusterBody) string {
				return map[bool]string{true: "the cluster " + x.Id + " (" + x.Name + ") with this Talos cluster ID"}[x.TalosClusterId == want]
			})
		}}
}

// machineAdd records a machine in a cluster, its Talos endpoint stored with the default port 50000
// (operator-experience.md §5.2, §10: POST /machines, author, human only).
func (e *env) machineAdd(args []string) error {
	var in apitypes.MachineInput
	var nodeID, uuid, serial, platform, name string
	var nameGiven bool
	var rf repeatFlags
	if _, err := e.parse("machine add", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&in.Cluster, "cluster", "", "")
		// The flag's presence decides, never its value: an empty --name is a named request the
		// server refuses, not a nameless machine.
		fs.Func("name", "", func(s string) error { name, nameGiven = s, true; return nil })
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
	var prep func(*client.Client) error
	if nameGiven {
		in.Name, prep = &name, needNameRevision
	}
	return e.writeAfter(prep, machineWrite(in, nodeID, uuid, rp), func(c *client.Client, r *client.Response) ([]string, error) {
		var m apitypes.MachineBody
		if err := json.Unmarshal(r.Body, &m); err != nil {
			return nil, err
		}
		// The cluster's name is shown when its read succeeds and carries the epoch of the write's
		// answer; the write is done either way (client-contracts.md section 8.5).
		cluster := m.Cluster
		if cr, err := c.Get("/clusters/" + url.PathEscape(m.Cluster)); err == nil && cr.Header.Get("Bronzeward-Epoch") == r.Header.Get("Bronzeward-Epoch") {
			var x apitypes.ClusterBody
			if json.Unmarshal(cr.Body, &x) == nil {
				cluster += " (" + x.Name + ")"
			}
		}
		return []string{sf("Machine %s recorded in cluster %s. Endpoint %s.", machineLabel(m.Id, m.Name), cluster, m.TalosEndpoint),
			"Next: supply the cluster's first key (cluster first-key), then import its configuration."}, nil
	})
}

// machineWrite is the write that records a machine, which machine add and cluster onboard send.
// nodeID and uuid are the identity in, which the read after an unknown outcome looks for.
func machineWrite(in apitypes.MachineInput, nodeID, uuid string, rp *client.Repeat) *client.Write {
	body, _ := json.Marshal(in)
	path := "/machines?" + url.Values{"cluster": {in.Cluster}}.Encode()
	return &client.Write{Method: http.MethodPost, Path: "/machines", Body: body, HumanOnly: true, Repeat: rp,
		Check: "machine list --cluster " + client.ShellWord(in.Cluster),
		Read: func(c *client.Client) (string, error) {
			return find(c, path, "machine with this identity", func(m apitypes.MachineBody) string {
				h := m.Hardware
				same := h.TalosNodeId != nil && *h.TalosNodeId == nodeID || h.SmbiosUuid != nil && strings.EqualFold(*h.SmbiosUuid, uuid)
				return map[bool]string{true: "the machine " + m.Id + " with this identity"}[same]
			})
		}}
}

// nameRevision is the API revision that adds a machine's name. A server below it refuses the field of
// machine add and has no route for machine rename, so the client refuses first, naming both revisions
// (client contracts §7.1 item 1).
const nameRevision = 10

func needNameRevision(c *client.Client) error {
	// The served revision is read on context use (§7 item 3), which a write does only inside its send.
	if _, _, err := c.Server(); err != nil {
		return err
	}
	if c.Revision < nameRevision {
		return fmt.Errorf("a machine's name needs API revision %d; the server serves revision %d", nameRevision, c.Revision)
	}
	return nil
}

// renameReaches says when a rename reaches the machine, word for word as client contracts A9 item 6 has it:
// the first two sentences, the restart sentence in its "not yet confirmed" form (until ginsys/bronzeward#1040's
// measurement picks one) and the Kubernetes node name sentence. The web rename form says the same.
var renameReaches = []string{
	"If this machine's hostname comes from its name, the new name reaches the machine only after the next release is published and a plan for it is approved. Until then the machine keeps its current hostname.",
	"Whether the machine needs a restart to take the new name is not yet confirmed.",
	"On the node, the Kubernetes node name normally follows the hostname.",
}

// machineRename replaces a machine's name (client contracts A9, operator-experience.md §10:
// POST /machines/{id}/names, author, human only). A name another machine of the cluster holds, and the
// machine's own current name, are refused by the server and shown as every refusal is. The text output
// ends with renameReaches; -o json and -o yaml print the answer unchanged.
func (e *env) machineRename(args []string) error {
	var in apitypes.NameInput
	var rf repeatFlags
	var cluster string
	pos, err := e.parse("machine rename", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&in.Name, "name", "", "")
		fs.StringVar(&cluster, "cluster", "", "")
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
	return e.writeNamed(pos[0], cluster, needNameRevision, func(id string) *client.Write {
		m := url.PathEscape(id)
		return &client.Write{Method: http.MethodPost, Path: "/machines/" + m + "/names", Body: body, HumanOnly: true,
			Repeat: rp, Check: "machine timeline " + client.ShellWord(id),
			Read: func(c *client.Client) (string, error) {
				r, err := c.Get("/machines/" + m)
				if err != nil {
					return "", err
				}
				var x apitypes.MachineBody
				err = json.Unmarshal(r.Body, &x)
				return "GET /api/v1/machines/" + m + " shows the name " + deref(x.Name), err
			}}
	}, func(_ *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.NameChangeBody
		err := json.Unmarshal(r.Body, &x)
		return append([]string{sf("Machine %s: name %s, was %s (timeline revision %d).", x.Machine, x.Name, deref(x.PreviousName), x.Revision)}, renameReaches...), err
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
		return []string{sf("Machine %s: Talos endpoint %s, was %s (timeline revision %d).",
			x.Machine, x.TalosEndpoint, x.PreviousEndpoint, x.Revision)}, err
	})
}

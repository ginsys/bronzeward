package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/machinename"
	"github.com/ginsys/bronzeward/internal/talos/discover"
	"go.yaml.in/yaml/v3"
)

// The states of cluster onboard's parts, as the object prints them: created or existing for a record,
// stored or replaced (an earlier key) or skipped for the first key, released or paused for an import; the
// failure states are those of composite.go.
const (
	obCreated     = "created"
	obExisting    = "existing"
	obStored      = "stored"
	obReplaced    = "replaced"
	obSkipped     = "skipped"
	obRefused     = "refused"
	obReleased    = "released"
	obNotRecorded = "not-recorded"
)

// onboardPart is one step of the object: its state, the ID it made or found, and for a step that did
// not finish, the command that carries on and the line the command printed for it.
type onboardPart struct {
	State   string `json:"state"`
	ID      string `json:"id,omitempty"`
	Command string `json:"command,omitempty"`
	Error   string `json:"error,omitempty"`
}

type onboardCluster struct {
	onboardPart
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

type onboardDraft struct {
	onboardPart
	Title string `json:"title"`
}

type onboardMachine struct {
	Node    string `json:"node"`
	Host    string `json:"hostname,omitempty"`
	Machine string `json:"machine,omitempty"`
	// Name is the name the machine this run recorded was given from its hostname; NameNote says how, or
	// why it has none (operator experience §5.2). Neither is set for a machine that already existed.
	// NameHeldBy is the identifier of the machine that holds the name this one was refused, for a script:
	// the note says it in words, and never by identifier.
	Name       string      `json:"name,omitempty"`
	NameNote   string      `json:"nameNote,omitempty"`
	NameHeldBy string      `json:"nameHeldBy,omitempty"`
	Add        onboardPart `json:"add"`
	Import     onboardPart `json:"import"`
	// Assignment is the assignment that selects nothing, which the run gives a machine whose import was
	// released into the draft and that has no assignment (ginsys/bronzeward#1068). Nil where the step
	// did not reach the machine.
	Assignment *onboardPart `json:"assignment,omitempty"`
}

// onboarding is the object cluster onboard prints for -o json and yaml, once, when its last step has
// settled; the text output is the same facts as lines.
type onboarding struct {
	Cluster  onboardCluster   `json:"cluster"`
	FirstKey onboardPart      `json:"firstKey"`
	Draft    *onboardDraft    `json:"draft,omitempty"`
	Machines []onboardMachine `json:"machines"`
	// again is the command that carries the run on, once a paused import is continued; ctx is the
	// --context the commands name, so each one reaches the server this run did.
	again, ctx string
}

// onboardArgs are the flags of cluster onboard.
type onboardArgs struct {
	file, endpoint, talosContext, name, kube string
	keyKind, keyFile                         string // --first-key-kind and --first-key-file, both or neither
	skipFirstKey, noImport, review           bool
	staging                                  string
	// config is the talosconfig as read and doc its one context; keyType and keyDoc are the first key to
	// send, the talosconfig's one context unless --first-key-* names another document. All are read
	// before any request is sent.
	config, doc     []byte
	keyType, keyDoc string
}

// httpsURL says whether s is an https URL with a host.
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// rerun is the command that carries on, from what the run settled: the flags given, with the
// Kubernetes endpoint as it was resolved, and --skip-first-key once the first key is stored.
func (a onboardArgs) rerun(e *env, skipFirstKey bool) string {
	w := []string{"bronzewardctl"}
	if e.context != "" {
		w = append(w, "--context", client.ShellWord(e.context))
	}
	w = append(w, "cluster", "onboard", "--talosconfig", client.ShellWord(a.file), "--name", client.ShellWord(a.name),
		"--kubernetes-endpoint", client.ShellWord(a.kube))
	for _, f := range [][2]string{{"--endpoint", a.endpoint}, {"--talos-context", a.talosContext}} {
		if f[1] != "" {
			w = append(w, f[0], client.ShellWord(f[1]))
		}
	}
	if a.staging != "encrypted" {
		w = append(w, "--staging", a.staging)
	}
	if a.review {
		w = append(w, "--review")
	}
	if a.noImport {
		w = append(w, "--no-import")
	}
	switch {
	case skipFirstKey:
		w = append(w, "--skip-first-key")
	case a.keyFile != "":
		w = append(w, "--first-key-kind", a.keyKind, "--first-key-file", client.ShellWord(a.keyFile))
	}
	return strings.Join(w, " ")
}

// quietly runs f with standard output discarded: each step's own answer is kept for the one summary.
func (e *env) quietly(f func() error) error {
	out := e.out
	e.out = io.Discard
	defer func() { e.out = out }()
	return f()
}

// conflictID is the record that a 409 conflict names under member, "" when err is none.
func conflictID(err error, member string) string {
	var p *client.Problem
	if errors.As(err, &p) && p.Code() == "conflict" {
		s, _ := p.Doc[member].(string)
		return s
	}
	return ""
}

// nameRefusal is the note for a machine whose name the server refused, "" when err is no refusal of a
// name that was sent: `name-in-use` names the machine that holds it (§9.4), whose identifier is the
// second result for a script, and `invalid-request` is a grammar the server does not take (a server older
// than the widened one takes one label). The note names that machine by the name it holds, never by its
// identifier.
func nameRefusal(err error, name string) (note, heldBy string) {
	var p *client.Problem
	if name == "" || !errors.As(err, &p) {
		return "", ""
	}
	switch p.Code() {
	case "name-in-use":
		heldBy, _ = p.Doc["machine"].(string)
		return fmt.Sprintf("no name: the name %q is already held by another machine of the cluster; rename that machine, then name this one", name), heldBy
	case "invalid-request":
		return fmt.Sprintf("no name: the server refused the hostname %q as a name", name), ""
	}
	return "", ""
}

// clusterOnboard records a cluster, its machines and its first key, then imports each machine into one
// draft (operator-experience.md §5.2, §5.9): the steps of cluster discover, cluster add, machine add,
// cluster first-key and import start, in that order, each safe to run again. Nothing it does is
// published: the draft is left for review.
func (e *env) clusterOnboard(args []string) (err error) {
	var a onboardArgs
	var noWait bool
	var rf repeatFlags
	if _, err := e.parse("cluster onboard", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&a.file, "talosconfig", "", "")
		fs.StringVar(&a.endpoint, "endpoint", "", "")
		fs.StringVar(&a.talosContext, "talos-context", "", "")
		fs.StringVar(&a.name, "name", "", "")
		fs.StringVar(&a.kube, "kubernetes-endpoint", "", "")
		fs.StringVar(&a.keyKind, "first-key-kind", "", "")
		fs.StringVar(&a.keyFile, "first-key-file", "", "")
		fs.BoolVar(&a.skipFirstKey, "skip-first-key", false, "")
		fs.BoolVar(&a.noImport, "no-import", false, "")
		fs.BoolVar(&a.review, "review", false, "")
		fs.StringVar(&a.staging, "staging", "encrypted", "")
		fs.BoolVar(&noWait, "no-wait", false, "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	if noWait {
		return usageError{errors.New("cluster onboard waits for each import, so --no-wait is not given: import start --no-wait runs one import without waiting")}
	}
	for name, g := range map[string]given{"idempotency-key": rf.key, "expect-epoch": rf.epoch, "expect-principal": rf.principal, "if-match": rf.ifMatch} {
		if g.set {
			return usageError{fmt.Errorf("cluster onboard sends several writes, so --%s is not given: run it again, which finds what exists, and repeat one write with its own command", name)}
		}
	}
	if err := e.onboardInputs(&a); err != nil {
		return err
	}
	var id apitypes.IdentityBody
	if err := e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		return getJSON(c, "/identity", &id)
	}); err != nil {
		return err
	}
	if id.Kind != "human" || !slices.Contains(id.Roles, apitypes.IdentityBodyRoles("author")) {
		return errors.New("cluster onboard needs an author signed in as a person: it records a cluster, machines and imports. Nothing was sent")
	}
	var res discover.Result
	if err := e.cancellable("the cluster's Talos nodes", func(ctx context.Context) (err error) {
		res, err = discoverCluster(ctx, a.config, a.talosContext, a.endpoint)
		return err
	}); errors.Is(err, discover.ErrNoEndpoint) {
		return usageError{errors.New("the talosconfig names no endpoint: give --endpoint <host|ip[:port]>")}
	} else if err != nil {
		return err
	}
	if !res.Supported {
		return fmt.Errorf("contract %s is not the contract of the machinery in this client, so the server would refuse the cluster. Nothing was recorded", client.Printable(res.Contract))
	}
	if a.kube == "proposed" {
		a.kube = res.KubernetesEndpoint
	}
	if !httpsURL(a.kube) {
		return errors.New("no https Kubernetes endpoint to record: the first control-plane member gave none, so give --kubernetes-endpoint <https url>. Nothing was recorded")
	}
	address, ok := firstControlPlane(res)
	if !ok && !a.skipFirstKey {
		return errors.New("no control-plane member answered, so no address can carry the first key. Nothing was recorded: run the command again once one does")
	}
	obj := &onboarding{FirstKey: onboardPart{State: stateNotRun.s()}, Cluster: onboardCluster{onboardPart: onboardPart{State: stateNotRun.s()}}}
	for _, m := range res.Members {
		om := onboardMachine{Node: m.NodeID, Host: m.Hostname, Add: onboardPart{State: stateNotRun.s()}, Import: onboardPart{State: stateNotRun.s()}}
		switch {
		case !m.Answered:
			om.Add = onboardPart{State: obNotRecorded, Error: "did not answer"}
		case m.Platform == "":
			om.Add = onboardPart{State: obNotRecorded, Error: "answered, reported no platform"}
		}
		obj.Machines = append(obj.Machines, om)
	}
	rerun := func() string {
		return a.rerun(e, a.skipFirstKey || slices.Contains([]string{obStored, obReplaced, obSkipped}, obj.FirstKey.State))
	}
	obj.ctx = e.ctxPrefix()
	defer func() {
		obj.again = rerun()
		parts := []*onboardPart{&obj.Cluster.onboardPart, &obj.FirstKey}
		if obj.Draft != nil {
			parts = append(parts, &obj.Draft.onboardPart)
		}
		for i := range obj.Machines {
			parts = append(parts, &obj.Machines[i].Add, &obj.Machines[i].Import)
		}
		for _, p := range parts {
			p.Command = obj.q(p.Command)
		}
		perr := e.say(onboardLines(obj)...)
		if e.output != "table" {
			perr = e.printComposite(obj)
		}
		if perr != nil {
			if err == nil {
				err = perr
			} else {
				fmt.Fprintf(e.err, "bronzewardctl: %s\n", client.Shown(perr))
			}
		}
	}()
	if err := e.onboardRecords(&a, res, obj, rerun); err != nil {
		return err
	}
	if err := e.onboardFirstKey(&a, obj, address, rerun); err != nil {
		return err
	}
	if a.noImport {
		for i := range obj.Machines {
			if obj.Machines[i].Machine != "" {
				obj.Machines[i].Import = onboardPart{State: obSkipped}
			}
		}
		return nil
	}
	return e.onboardImports(&a, obj, rerun)
}

// onboardInputs checks every flag, then reads the talosconfig's one context and the first key's file,
// before any request is sent: a refusal here leaves nothing to undo.
func (e *env) onboardInputs(a *onboardArgs) (err error) {
	switch {
	case a.file == "":
		return usageError{errors.New("cluster onboard needs --talosconfig <file>: it looks in no default place")}
	case unprintable(a.file):
		return usageError{errors.New("--talosconfig names a file whose name holds a byte or character that cannot be printed back in the rerun command: rename the file")}
	case a.name == "":
		return usageError{errors.New("cluster onboard needs --name <cluster name>")}
	case a.kube == "":
		return usageError{errors.New("cluster onboard needs --kubernetes-endpoint <https url>, or proposed to take the one cluster discover proposes")}
	}
	if err := checkStaging(a.staging, a.review); err != nil {
		return err
	}
	if a.kube != "proposed" && !httpsURL(a.kube) {
		return usageError{errors.New("--kubernetes-endpoint must be an https URL, or proposed")}
	}
	switch {
	case (a.keyKind == "") != (a.keyFile == ""):
		return usageError{errors.New("--first-key-kind and --first-key-file are given together or not at all")}
	case a.skipFirstKey && a.keyFile != "":
		return usageError{errors.New("--skip-first-key sends no first key, so it takes no --first-key-kind or --first-key-file")}
	case a.keyKind != "" && !slices.Contains([]string{"machine-config", "secrets-bundle"}, a.keyKind):
		return usageError{errors.New("--first-key-kind is machine-config or secrets-bundle: the talosconfig is the default first key")}
	case unprintable(a.keyFile):
		return usageError{errors.New("--first-key-file names a file whose name holds a byte or character that cannot be printed back in the rerun command: rename the file")}
	}
	if a.config, err = readTalosconfig(a.file); err != nil {
		return err
	}
	if a.doc, err = discover.OneContext(a.config, a.talosContext); errors.Is(err, discover.ErrUnknownContext) {
		return usageError{errors.New("--talos-context names a context that the talosconfig does not hold")}
	} else if err != nil {
		return fmt.Errorf("the talosconfig cannot be used: %w", err)
	}
	var ctxs struct {
		Context  string
		Contexts map[string]struct{ Endpoints []string }
	}
	if a.endpoint == "" && yaml.Unmarshal(a.config, &ctxs) == nil && len(ctxs.Contexts[cmp.Or(a.talosContext, ctxs.Context)].Endpoints) == 0 {
		return usageError{errors.New("the talosconfig's context names no endpoint: give --endpoint <host|ip[:port]>")}
	}
	a.keyType, a.keyDoc = "talosconfig", string(a.doc)
	if a.keyFile != "" {
		a.keyType = a.keyKind
		a.keyDoc, err = firstKeyDocument("--first-key-file", a.keyFile, a.keyKind, "")
	}
	return err
}

// onboardRecords records the cluster and each machine, or finds what a 409 names. A machine that is in
// another cluster stops the command.
func (e *env) onboardRecords(a *onboardArgs, res discover.Result, obj *onboarding, rerun func() string) error {
	in := apitypes.ClusterInput{Name: a.name, Endpoint: a.kube, Contract: res.Contract, TalosClusterId: res.ClusterID}
	var cl apitypes.ClusterBody
	err := e.quietly(func() error {
		return e.writeAfter(nil, clusterWrite(in, nil), func(_ *client.Client, r *client.Response) ([]string, error) {
			return nil, json.Unmarshal(r.Body, &cl)
		})
	})
	// A write the server acknowledged is recorded, whatever fails after it (keeping what the answers taught
	// the context): the step says so, and the error still ends the run, as plan create's does.
	var lost saveFailed
	saved := errors.As(err, &lost)
	state := obCreated
	if existing := conflictID(err, "cluster"); existing != "" {
		state = obExisting
		err = e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
			return getJSON(c, "/clusters/"+url.PathEscape(existing), &cl)
		})
	} else if (err == nil || saved) && cl.Id == "" {
		err, saved = errors.New("the cluster was recorded, but its answer names no cluster"), false
	}
	if err != nil && !saved {
		obj.Cluster.onboardPart = onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}
		return err
	}
	a.name = cl.Name
	obj.Cluster = onboardCluster{onboardPart: onboardPart{State: state, ID: cl.Id}, Name: cl.Name, Endpoint: cl.Endpoint}
	if saved {
		obj.Cluster.Command, obj.Cluster.Error = rerun(), client.Shown(err)
		return err
	}
	for i, m := range res.Members {
		om := &obj.Machines[i]
		if om.Add.State == obNotRecorded {
			continue
		}
		min := apitypes.MachineInput{Cluster: cl.Id, Platform: apitypes.MachineInputPlatform(m.Platform), TalosEndpoint: endpointArg(m.Address)}
		// Named by the hostname the node reports, as the web page does (operator experience §5.2).
		name, note := machinename.FromHostname(m.Hostname)
		if name != "" {
			min.Name = &name
		}
		if m.SMBIOSUUID != "" {
			min.SmbiosUuid = &m.SMBIOSUUID
		} else {
			min.TalosNodeId = &m.NodeID
		}
		var mb apitypes.MachineBody
		record := func() error {
			mb = apitypes.MachineBody{}
			return e.quietly(func() error {
				return e.writeAfter(nil, machineWrite(min, m.NodeID, m.SMBIOSUUID, nil), func(_ *client.Client, r *client.Response) ([]string, error) {
					return nil, json.Unmarshal(r.Body, &mb)
				})
			})
		}
		err := record()
		// A name the server refuses (another machine holds it, or the grammar rejects it) never stops the
		// run: the machine is recorded without it, with the reason, to be named afterwards.
		var heldBy string
		if refused, holder := nameRefusal(err, name); refused != "" {
			min.Name, name, note, heldBy = nil, "", refused, holder
			err = record()
		}
		saved := errors.As(err, &lost)
		state := obCreated
		if existing := conflictID(err, "machine"); existing != "" {
			state = obExisting
			err = e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
				return getJSON(c, "/machines/"+url.PathEscape(existing), &mb)
			})
			if err == nil && mb.Cluster != cl.Id {
				om.Machine, om.Add = mb.Id, onboardPart{State: obRefused, Error: "in another cluster"}
				return fmt.Errorf("machine %s (node %s) is recorded in cluster %s, not in cluster %s: nothing after the machines ran. Remove it from that cluster or leave this node out of this one",
					client.Printable(mb.Id), client.Printable(m.NodeID), client.Printable(mb.Cluster), client.Printable(cl.Id))
			}
		} else if (err == nil || saved) && mb.Id == "" {
			err, saved = errors.New("the machine was recorded, but its answer names no machine"), false
		}
		if err != nil && !saved {
			om.Add = onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}
			return err
		}
		om.Machine, om.Add = mb.Id, onboardPart{State: state, ID: mb.Id}
		if state == obCreated {
			om.Name, om.NameNote, om.NameHeldBy = name, note, heldBy // a machine already recorded keeps the name it has
		}
		if saved {
			om.Add.Command, om.Add.Error = rerun(), client.Shown(err)
			return err
		}
	}
	return nil
}

// onboardFirstKey supplies the cluster's first key to the address of the first control-plane member that
// answered, through the write of cluster first-key: sent once, the document never printed. A rerun
// supplies it again, and the answer says it replaced one; --skip-first-key sends nothing. An unknown
// outcome stays unknown and its rerun is the same command, which replaces whatever was stored.
func (e *env) onboardFirstKey(a *onboardArgs, obj *onboarding, address string, rerun func() string) error {
	if a.skipFirstKey {
		obj.FirstKey = onboardPart{State: obSkipped}
		return nil
	}
	in := apitypes.FirstKeyInput{Document: a.keyDoc, Endpoint: endpointArg(address), Kind: apitypes.FirstKeyInputKind(a.keyType)}
	var x apitypes.FirstKeyBody
	err := firstKeyRefusal(e.quietly(func() error {
		return e.writeAfter(nil, firstKeyWrite(obj.Cluster.ID, in, nil), func(_ *client.Client, r *client.Response) ([]string, error) {
			return nil, json.Unmarshal(r.Body, &x)
		})
	}))
	// An answer that arrived is the key stored, whatever fails after it (keeping what the answers
	// taught the context): the step says stored, and the error still ends the run, as plan create's does.
	var lost saveFailed
	if err != nil && !errors.As(err, &lost) {
		obj.FirstKey = onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}
		return err
	}
	obj.FirstKey = onboardPart{State: obStored}
	if x.Replaced {
		obj.FirstKey.State = obReplaced
	}
	if err != nil {
		obj.FirstKey.Command = rerun()
	}
	return err
}

func (s stepState) s() string { return string(s) }

// onboardImports opens the draft titled "onboard <name>" or finds the open one, then imports each
// recorded machine into it, one after the other, each as import start does. A machine whose import was
// released into this draft is skipped. A paused import ends the command, as drift adopt's does, and so
// does a held or resumed one, which only import status can read.
func (e *env) onboardImports(a *onboardArgs, obj *onboarding, rerun func() string) error {
	title, state := "onboard "+a.name, obExisting
	var draft apitypes.DraftBody
	var saved bool // the draft was recorded and only keeping the context failed after it
	err := e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		// GET /drafts takes no filter, so the open draft is picked out here.
		items, err := c.List("/drafts")
		if err != nil {
			return err
		}
		for _, raw := range items {
			var d apitypes.DraftBody
			if json.Unmarshal(raw, &d) == nil && d.Title == title && d.State == "open" && d.Cluster == obj.Cluster.ID {
				draft = d
				break
			}
		}
		if draft.Id == "" {
			return nil
		}
		// An open draft found: its ETag is read and kept, so the first import sends it.
		r, err := c.Get("/drafts/" + url.PathEscape(draft.Id))
		if err == nil {
			keep(e, c, draft.Id, r.Header.Get("ETag"))
		}
		return err
	})
	if err == nil && draft.Id == "" {
		state = obCreated
		in := apitypes.DraftInput{Cluster: obj.Cluster.ID, Title: title}
		body, _ := json.Marshal(in)
		w := &client.Write{Method: http.MethodPost, Path: "/drafts", Body: body, Check: "draft list --cluster " + client.ShellWord(in.Cluster),
			Read: func(c *client.Client) (string, error) {
				return find(c, "/drafts", "open draft with this title in this cluster", func(d apitypes.DraftBody) string {
					return map[bool]string{true: "the open draft " + d.Id + " with this title in this cluster"}[d.Cluster == in.Cluster && d.Title == in.Title && d.State == "open"]
				})
			}}
		err = e.quietly(func() error {
			return e.writeAfter(nil, w, func(c *client.Client, r *client.Response) ([]string, error) {
				if err := json.Unmarshal(r.Body, &draft); err != nil {
					return nil, err
				}
				keep(e, c, draft.Id, r.Header.Get("ETag"))
				return nil, nil
			})
		})
		var lost saveFailed
		saved = errors.As(err, &lost)
		if (err == nil || saved) && draft.Id == "" {
			err, saved = errors.New("the draft was opened, but its answer names no draft"), false
		}
	}
	if err != nil && !saved {
		obj.Draft = &onboardDraft{onboardPart: onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}, Title: title}
		return err
	}
	obj.Draft = &onboardDraft{onboardPart: onboardPart{State: state, ID: draft.Id}, Title: title}
	if saved {
		obj.Draft.Command, obj.Draft.Error = rerun(), client.Shown(err)
		return err
	}
	for i := range obj.Machines {
		m := &obj.Machines[i]
		if m.Machine == "" {
			continue
		}
		stop, err := e.onboardImport(a, m, draft.Id, rerun)
		if err != nil || stop {
			return err
		}
	}
	return e.onboardAssignments(obj, draft.Id, rerun)
}

// onboardAssignments gives each machine whose import was released into the draft an assignment that
// selects nothing, so the release the draft makes holds an assignment revision and a plan can bind it
// (ginsys/bronzeward#1068). A machine that has an assignment entry in the draft or an assignment head is
// left as it is: a rerun writes only what is missing. The draft is read first for its ETag and entries.
func (e *env) onboardAssignments(obj *onboarding, draft string, rerun func() string) error {
	var need []*onboardMachine
	for i := range obj.Machines {
		if m := &obj.Machines[i]; m.Machine != "" && m.Import.State == obReleased {
			need = append(need, m)
		}
	}
	if len(need) == 0 {
		return nil
	}
	held := map[string]bool{}
	if err := e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		r, err := c.Get("/drafts/" + url.PathEscape(draft))
		if err != nil {
			return err
		}
		var d apitypes.DraftBody
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return ef("GET /api/v1/drafts/%s cannot be read: %w", url.PathEscape(draft), err)
		}
		keep(e, c, draft, r.Header.Get("ETag"))
		for _, item := range d.Entries {
			if x, err := item.AsSourceEntry(); err == nil && x.Kind == apitypes.SourceEntryKindAssignment && x.Machine != nil {
				held[*x.Machine] = true
			}
		}
		items, err := c.List("/assignments")
		if err != nil {
			return err
		}
		for _, raw := range items {
			var a apitypes.AssignmentBody
			if json.Unmarshal(raw, &a) == nil {
				held[a.Machine] = true
			}
		}
		return nil
	}); err != nil {
		need[0].Assignment = &onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}
		return err
	}
	for _, m := range need {
		if held[m.Machine] {
			m.Assignment = &onboardPart{State: obExisting}
			continue
		}
		err := e.quietly(func() error {
			return e.write(&client.Write{Method: http.MethodPut, Path: "/drafts/" + url.PathEscape(draft) + "/assignments/" + url.PathEscape(m.Machine),
				Body: []byte(`{}`), Draft: draft, Check: "draft show " + client.ShellWord(draft),
				Read: entryRead(draft, apitypes.SourceEntryKindAssignment, m.Machine)}, func(*client.Client, *client.Response) ([]string, error) { return nil, nil })
		})
		var lost saveFailed
		if err != nil && !errors.As(err, &lost) {
			m.Assignment = &onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}
			return err
		}
		m.Assignment = &onboardPart{State: obCreated}
		if err != nil {
			m.Assignment.Command, m.Assignment.Error = rerun(), client.Shown(err)
			return err
		}
	}
	return nil
}

// onboardImport imports one machine, unless an import of it into this draft already stands. It says
// whether the run stops here with no error: an import paused for review.
func (e *env) onboardImport(a *onboardArgs, m *onboardMachine, draft string, rerun func() string) (bool, error) {
	var found *apitypes.IngestionBody
	if err := e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		items, err := c.List("/ingestions?" + url.Values{"machine": {m.Machine}}.Encode())
		if err != nil {
			return err
		}
		rank := map[string]int{"released": 1, "held": 2, "resumed": 2, "paused": 3} // a running import outranks an earlier release
		for _, raw := range items {
			var b apitypes.IngestionBody
			if json.Unmarshal(raw, &b) == nil && b.Draft == draft && rank[b.State] > 0 && (found == nil || rank[b.State] > rank[found.State]) {
				found = &b
			}
		}
		return nil
	}); err != nil {
		m.Import = onboardPart{State: classify(err, false).s(), Command: rerun(), Error: client.Shown(err)}
		return false, err
	}
	if found != nil {
		return onboardImportState(m, found, e.ctxPrefix())
	}
	args := []string{"--machine", m.Machine, "--draft", draft, "--staging", a.staging}
	if a.review {
		args = append(args, "--review")
	}
	var c *client.Client
	var acc accepted
	err := e.quietly(func() (err error) {
		c, acc, err = e.ingest("cluster onboard", "import", args)
		return err
	})
	if err != nil {
		state, cmd := classify(err, acc.Ingestion != ""), rerun()
		if acc.Ingestion != "" {
			cmd = statusCmd(acc.Ingestion)
		}
		m.Import = onboardPart{State: state.s(), ID: acc.Ingestion, Command: cmd, Error: client.Shown(err)}
		if state == stateDone {
			// The follow ended well or paused, and only the context could not be saved: read which.
			onboardSettle(m, acc.Ingestion, e.ctxPrefix(), func(id string) (b *apitypes.IngestionBody, err error) {
				if c != nil {
					return readIngestion(c, id)
				}
				err = e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) (err error) {
					b, err = readIngestion(c, id)
					return err
				})
				return b, err
			})
		}
		return false, err
	}
	b, rerr := readIngestion(c, acc.Ingestion)
	rerr = errors.Join(rerr, c.Save())
	if rerr != nil {
		m.Import = onboardPart{State: stateUnknown.s(), ID: acc.Ingestion, Command: statusCmd(acc.Ingestion), Error: client.Shown(rerr)}
		return false, rerr
	}
	return onboardImportState(m, b, e.ctxPrefix())
}

// onboardSettle sets the import of m from the ingestion read, for an import whose follow ended well or
// paused. The error that ended the run stays on the import. A failed read leaves the state unknown.
func onboardSettle(m *onboardMachine, id, ctx string, read func(string) (*apitypes.IngestionBody, error)) {
	stopped := m.Import.Error
	b, err := read(id)
	if err != nil {
		m.Import = onboardPart{State: stateUnknown.s(), ID: id, Command: qualify(ctx, statusCmd(id))}
	} else {
		_, _ = onboardImportState(m, b, ctx)
	}
	m.Import.Error = stopped
}

// statusCmd is the command that reads an import.
func statusCmd(ingestion string) string {
	return "bronzewardctl import status " + client.ShellWord(ingestion)
}

// onboardImportState settles a machine's import by the state the server shows for it.
func onboardImportState(m *onboardMachine, b *apitypes.IngestionBody, ctx string) (bool, error) {
	status := qualify(ctx, statusCmd(b.Id))
	switch b.State {
	case "released":
		m.Import = onboardPart{State: obReleased, ID: b.Id}
		return false, nil
	case "paused":
		m.Import = onboardPart{State: statePaused.s(), ID: b.Id, Command: status}
		return true, nil
	}
	m.Import = onboardPart{State: stateUnknown.s(), ID: b.Id, Command: status}
	return false, fmt.Errorf("import %s of machine %s is %s, so it is not run again here: read it with %s", client.Printable(b.Id), client.Printable(m.Machine), client.Printable(b.State), status)
}

// onboardLines is the text summary: the cluster, the first key, the draft and a row per member, then the
// next step.
func (o *onboarding) q(cmd string) string { return qualify(o.ctx, cmd) }

// ctxPrefix is the global --context of this run as a command prefix, or empty.
func (e *env) ctxPrefix() string {
	if e.context == "" {
		return ""
	}
	return "--context " + client.ShellWord(e.context) + " "
}

// qualify names the global --context in a printed command, once, so that the command reaches the server
// this run did. ctx is "--context '<name>' " or empty.
func qualify(ctx, cmd string) string {
	if ctx == "" || cmd == "" || strings.Contains(cmd, "--context") {
		return cmd
	}
	return strings.Replace(cmd, "bronzewardctl ", "bronzewardctl "+ctx, 1)
}

func onboardLines(obj *onboarding) []string {
	c := obj.Cluster
	out := []string{"Cluster: " + c.State}
	if c.ID != "" {
		out = []string{sf("Cluster %s: %s (%s, %s)", c.ID, c.State, c.Name, c.Endpoint)}
	}
	out = append(out, "First key: "+obj.FirstKey.State)
	if obj.FirstKey.Error != "" {
		out = append(out, "  "+pr(obj.FirstKey.Error))
	}
	if d := obj.Draft; d != nil {
		out = append(out, sf("Draft %s (%s): %s", d.ID, d.Title, d.State))
	}
	var commands []string
	add := func(cmds ...string) {
		for _, c := range cmds {
			if c = obj.q(c); c != "" && !slices.Contains(commands, c) {
				commands = append(commands, c)
			}
		}
	}
	out = append(out, "Machines:")
	paused := false
	for _, m := range obj.Machines {
		if m.Add.State == obNotRecorded {
			out = append(out, sf("  %s  %s  not recorded: %s", m.Node, m.Host, m.Add.Error))
			continue
		}
		line := sf("  %s  %s  %s  add %s, import %s", m.Node, m.Host, cmp.Or(m.Machine, "-"), m.Add.State, m.Import.State)
		if m.Assignment != nil {
			line += sf(", assignment %s", m.Assignment.State)
		}
		if m.NameNote != "" {
			line += sf(" (%s)", m.NameNote)
		}
		out = append(out, line)
		add(m.Add.Command, m.Import.Command)
		if m.Assignment != nil {
			add(m.Assignment.Command)
		}
		if m.Import.State == statePaused.s() {
			paused = true
			add("bronzewardctl import review "+client.ShellWord(m.Import.ID), "bronzewardctl import continue "+client.ShellWord(m.Import.ID), obj.again)
		}
	}
	add(c.Command, obj.FirstKey.Command)
	if obj.Draft != nil {
		add(obj.Draft.Command)
	}
	switch {
	case len(commands) > 0:
		out = append(out, "", "Next, one command at a time:")
		for _, cmd := range commands {
			out = append(out, "  "+cmd)
		}
		if paused {
			out = append(out, "", "Run the cluster onboard command after the continue: it imports the machines that follow.")
		}
	case obj.Draft != nil:
		out = append(out, "", obj.q("Next: read the draft, then publish it: bronzewardctl draft show "+client.ShellWord(obj.Draft.ID)))
	}
	return out
}

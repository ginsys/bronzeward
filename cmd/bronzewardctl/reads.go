package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// listing is a list command's paging (client-contracts.md §2 pagination): one page of --limit
// items from --cursor, or with --all every page.
type listing struct {
	limit  int
	cursor string
	all    bool
}

func (l *listing) flags(fs *flag.FlagSet) {
	fs.IntVar(&l.limit, "limit", 0, "")
	fs.StringVar(&l.cursor, "cursor", "", "")
	fs.BoolVar(&l.all, "all", false, "")
}

// read reads the resource show names under path, or the list at path with q as l asks. It
// returns what -o prints (the resource or page unchanged, or {"items": [...]} with --all), the
// items and the next page's cursor.
func (l listing) read(c *client.Client, path string, q url.Values, show []string) ([]byte, []json.RawMessage, string, error) {
	if l.limit != 0 {
		q.Set("limit", strconv.Itoa(l.limit))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var items []json.RawMessage
	var raw []byte
	var err error
	switch {
	case len(show) == 1:
		var r *client.Response
		if r, err = c.Get(path + "/" + url.PathEscape(show[0])); err == nil {
			raw, items = r.Body, []json.RawMessage{r.Body}
		}
	case !l.all:
		var p *client.Page
		if p, err = c.Page(path, l.cursor); err == nil {
			return p.Body, p.Items, p.Next, nil
		}
	default:
		if items, err = c.List(path); err == nil {
			raw, err = json.Marshal(map[string]any{"items": items})
		}
	}
	return raw, items, "", err
}

// more names the next page under a table.
func more(w *tabwriter.Writer, next string) {
	if next != "" {
		fmt.Fprintf(w, "Next page: --cursor %s, or --all for every page.\n", next)
	}
}

func decode[T any](c *client.Client, raw ...json.RawMessage) ([]T, error) {
	out := make([]T, len(raw))
	for i, r := range raw {
		if err := json.Unmarshal(r, &out[i]); err != nil {
			return nil, fmt.Errorf("%s: %w", c.Context.Where(), err)
		}
	}
	return out, nil
}

// session opens the selected context, reads GET /server, which checks the server's revision
// (§7 item 3), and runs do; the epoch and start mode are kept even when do fails.
func (e *env) session(do func(*client.Client, *client.Response, *apitypes.ServerBody) error) (err error) {
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	r, srv, err := c.Server()
	if err != nil {
		return err
	}
	return do(c, r, srv)
}

// noun reads a noun's verb, list or show, and its arguments; list's paging and extra flags.
func (e *env) noun(name string, args []string, l *listing, extra func(*flag.FlagSet)) (string, []string, error) {
	args, err := e.parse(name, args, -1, nil)
	if err != nil {
		return "", nil, err
	}
	if len(args) == 0 || args[0] != "list" && args[0] != "show" {
		return "", nil, usageError{fmt.Errorf("%s needs list or show <id>", name)}
	}
	if args[0] == "show" {
		pos, err := e.parse(name+" show", args[1:], 1, nil)
		return "show", pos, err
	}
	_, err = e.paged(name+" list", args[1:], 0, l, extra)
	return "list", nil, err
}

// paged parses a paged read's arguments: n positional ones, the paging flags and extra's.
func (e *env) paged(name string, args []string, n int, l *listing, extra func(*flag.FlagSet)) ([]string, error) {
	pos, err := e.parse(name, args, n, func(fs *flag.FlagSet) {
		l.flags(fs)
		if extra != nil {
			extra(fs)
		}
	})
	if err == nil && l.all && l.cursor != "" {
		err = usageError{errors.New("give --cursor or --all, not both")}
	}
	return pos, err
}

func (e *env) clusterCmd(args []string) error {
	var l listing
	_, pos, err := e.noun("cluster", args, &l, nil)
	if err != nil {
		return err
	}
	return e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		raw, items, next, err := l.read(c, "/clusters", url.Values{}, pos)
		if err != nil {
			return err
		}
		cs, err := decode[apitypes.ClusterBody](c, items...)
		if err != nil {
			return err
		}
		return e.emit(raw, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "CLUSTER\tNAME\tENDPOINT\tCONTRACT\tTALOS CLUSTER ID")
			for _, x := range cs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.Id, x.Name, x.Endpoint, x.Contract, x.TalosClusterId)
			}
			more(w, next)
		})
	})
}

func (e *env) machineCmd(args []string) error {
	if v, rest, err := e.verb("machine", args, "list", "show", "timeline", "observations"); err != nil {
		return err
	} else if v == "timeline" || v == "observations" {
		return e.machineRecords(v, rest)
	}
	var l listing
	var cluster string
	verb, pos, err := e.noun("machine", args, &l, func(fs *flag.FlagSet) { fs.StringVar(&cluster, "cluster", "", "") })
	if err != nil {
		return err
	}
	return e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		if verb == "show" {
			return e.machineShow(c, pos[0])
		}
		q := url.Values{}
		if cluster != "" {
			q.Set("cluster", cluster)
		}
		raw, items, next, err := l.read(c, "/machines", q, nil)
		if err != nil {
			return err
		}
		ms, err := decode[apitypes.MachineBody](c, items...)
		if err != nil {
			return err
		}
		return e.emit(raw, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "MACHINE\tENDPOINT\tDESIRED\tAPPLIED (SOURCE)\tOBSERVED\tSTATE")
			for _, m := range ms {
				applied := "none"
				if m.Applied != nil {
					applied = m.Applied.Release + " (" + m.Applied.Source + ")"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Id, m.TalosEndpoint, deref(m.Desired), applied, observedCell(m), stateCell(m))
			}
			more(w, next)
			fmt.Fprintln(w, "Health is not checked in this installation.")
		})
	})
}

func ago(t time.Time) string { return time.Since(t).Round(time.Second).String() + " ago" }

// observedCell compares the last configuration read with Applied and Desired, as the machine
// read attributes it (operator-experience.md §2.4); it is not a health result.
func observedCell(m apitypes.MachineBody) string {
	switch o := m.Observed; {
	case o == nil:
		return "none"
	case o.Release != nil && m.Applied != nil && *o.Release == m.Applied.Release:
		return "=applied, " + ago(o.At)
	case o.Release != nil && m.Desired != nil && *o.Release == *m.Desired:
		return "=desired, " + ago(o.At)
	default:
		return "no match, " + ago(o.At)
	}
}

func stateCell(m apitypes.MachineBody) string {
	var s []string
	for _, x := range []struct {
		on   bool
		text string
	}{{m.OpenDrift != nil, "drifted"}, {m.PendingConvergence, "pending convergence"}, {m.Frozen, "frozen"}, {m.ScopeState != "normal", "scope " + m.ScopeState}} {
		if x.on {
			s = append(s, x.text)
		}
	}
	if len(s) == 0 && strings.HasPrefix(observedCell(m), "=applied") {
		return "matches applied"
	}
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ", ")
}

// machineShow prints the machine and the status sentences of operator-experience.md §2.4,
// which also need its observations: every page is read, since the API serves them oldest first.
func (e *env) machineShow(c *client.Client, id string) error {
	r, err := c.Get("/machines/" + url.PathEscape(id))
	if err != nil {
		return err
	}
	ms, err := decode[apitypes.MachineBody](c, r.Body)
	if err != nil {
		return err
	}
	if e.output == "json" || e.output == "yaml" { // the resource alone; the sentences are the table's
		return e.emit(r.Body, nil)
	}
	items, err := c.List("/machines/" + url.PathEscape(id) + "/observations")
	if err != nil {
		return err
	}
	obs, err := decode[apitypes.ObservationBody](c, items...)
	if err != nil {
		return err
	}
	m := ms[0]
	return e.emit(r.Body, func(w *tabwriter.Writer) {
		fmt.Fprintf(w, "Machine\t%s\tcluster %s\n", m.Id, m.Cluster)
		fmt.Fprintf(w, "Endpoint\t%s\tplatform %s\n", m.TalosEndpoint, m.Platform)
		fmt.Fprintf(w, "Hardware\tnode ID %s\tSMBIOS UUID %s\n", deref(m.Hardware.TalosNodeId), deref(m.Hardware.SmbiosUuid))
		fmt.Fprintf(w, "Scope\t%s\n", m.ScopeState)
		for _, s := range sentences(m, obs) {
			fmt.Fprintln(w, s)
		}
	})
}

// sentences are operator-experience.md §2.4's status sentences, derived only from the machine
// read and its observations.
func sentences(m apitypes.MachineBody, obs []apitypes.ObservationBody) []string {
	var s []string
	add := func(f string, a ...any) { s = append(s, fmt.Sprintf(f, a...)) }
	at := func(t time.Time) string { return t.Format("2006-01-02 15:04:05 MST") }
	basis, latest := -1, (*apitypes.ObservationBody)(nil)
	for i, o := range obs {
		if m.Observed != nil && o.Id == m.Observed.Observation {
			basis = o.Basis
		}
		if len(o.Unread) > 0 && (latest == nil || o.Basis > latest.Basis) {
			latest = &obs[i]
		}
	}
	applied := "none"
	if m.Applied != nil {
		applied = m.Applied.Release
	}
	switch o := m.Observed; {
	case o == nil:
		add("No configuration read recorded.")
	case m.OpenDrift != nil:
		since := m.OpenDrift.Observation
		if i := slices.IndexFunc(obs, func(x apitypes.ObservationBody) bool { return x.Id == since }); i >= 0 {
			since = at(obs[i].At)
		}
		add("Drifted since %s: a read differed from what Bronzeward last verified (%s).", since, applied)
		if o.Release != nil && m.Desired != nil && *o.Release == *m.Desired {
			add("It equals the selected release; that does not close the drift.")
		}
	case o.Release == nil:
		add("The configuration read at %s matched neither Applied nor Desired, and no drift is open; an operation may hold the machine. See the timeline.", at(o.At))
	case *o.Release == applied:
		add("The configuration read at %s is attributed to %s, which is the Applied release (by %s).", at(o.At), applied, m.Applied.Source)
		if m.Applied.Source == "adoption" {
			add("Adopted from the node's own bytes; the release's compiled configuration may differ. The adopt plan's pendingConvergence evidence shows whether it does; a later apply-config plan withholds its diff when it does.")
			if deref(m.Desired) == applied {
				add("The read may match the adopted bytes or this release's compiled configuration; the API does not say which.")
			}
		}
	default:
		add("The configuration read at %s is attributed to the selected release %s, not the Applied one, and no drift is open; an operation may hold the machine. See the timeline.", at(o.At), *o.Release)
	}
	if m.Applied == nil {
		add("Not applied or adopted by Bronzeward.")
	}
	switch {
	case m.Desired == nil:
		add("No release selected.")
	case m.PendingConvergence && m.Applied == nil:
		add("Pending convergence: the selected release %s has not been applied to this machine.", *m.Desired)
	case m.OpenDrift != nil:
		// Drift is the sentence above; pending convergence and drift are different (§2.4).
	case m.PendingConvergence:
		add("Pending convergence: the configuration of the selected release %s for this machine differs from what Bronzeward applied.", *m.Desired)
	case m.Applied != nil && *m.Desired != m.Applied.Release:
		add("The selected release %s carries this machine's applied configuration unchanged.", *m.Desired)
	}
	if latest != nil && latest.Basis > basis {
		var causes []string
		for _, k := range slices.Sorted(maps.Keys(latest.Unread)) {
			causes = append(causes, k+": "+latest.Unread[k].Cause)
		}
		add("The latest read at %s could not read: %s.", at(latest.At), strings.Join(causes, "; "))
	}
	if m.Frozen {
		add("Frozen: no new commitment or attempt until an approver unfreezes; an attempt already recorded is not stopped, and an adoption can still be recorded.")
	}
	return append(s, "Health: not checked.")
}

// statusCmd combines GET /server, GET /recovery, the machines with open drift and the unresolved
// operations (#254 contracts); every page of both lists is read, since the drift filter is the
// client's own.
func (e *env) statusCmd(args []string) error {
	if _, err := e.parse("status", args, 0, nil); err != nil {
		return err
	}
	return e.session(func(c *client.Client, sr *client.Response, srv *apitypes.ServerBody) error {
		r, err := c.Get("/recovery")
		if err != nil {
			return err
		}
		recs, err := decode[apitypes.RecoveryBody](c, r.Body)
		if err != nil {
			return err
		}
		items, err := c.List("/machines")
		if err != nil {
			return err
		}
		ms, err := decode[apitypes.MachineBody](c, items...)
		if err != nil {
			return err
		}
		drifted, ids := []json.RawMessage{}, []string{}
		for i, m := range ms {
			if m.OpenDrift != nil {
				drifted, ids = append(drifted, items[i]), append(ids, m.Id)
			}
		}
		ops, err := c.List("/operations?state=unresolved")
		if err != nil {
			return err
		}
		unresolved, err := decode[apitypes.OperationBody](c, ops...)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(map[string]any{"server": json.RawMessage(sr.Body), "recovery": json.RawMessage(r.Body),
			"driftedMachines": drifted, "unresolvedOperations": ops})
		if err != nil {
			return err
		}
		rec := recs[0]
		return e.emit(raw, func(w *tabwriter.Writer) {
			fmt.Fprintf(w, "Context\t%s\n", c.Context.Where())
			fmt.Fprintf(w, "Server\t%s, API revision %d, start mode %s\n", srv.Version, srv.ApiRevision, srv.StartMode)
			mode := "off"
			if rec.RecoveryMode {
				mode = "on"
				if rec.Entry != nil {
					mode += " since " + rec.Entry.EnteredAt.Format("2006-01-02 15:04:05 MST")
				}
				var scopes []string
				for _, s := range rec.Scopes {
					if s.State != "normal" {
						scopes = append(scopes, s.Machine+" "+s.State)
					}
				}
				mode += ". Scopes: " + count(scopes)
			}
			fmt.Fprintf(w, "Epoch\t%s\n", rec.Epoch)
			fmt.Fprintf(w, "Recovery mode\t%s\n", mode)
			fmt.Fprintf(w, "Open drift\t%s\n", count(ids))
			var opIDs []string
			for _, o := range unresolved {
				opIDs = append(opIDs, o.Id+" ("+o.Kind+")")
			}
			fmt.Fprintf(w, "Unresolved operations\t%s\n", count(opIDs))
			fmt.Fprintln(w, "Health\tnot checked")
		})
	})
}

func count(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d: %s", len(ids), strings.Join(ids, ", "))
}

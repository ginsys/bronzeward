package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The shared (library) verbs of operator-experience.md §5.4 and §10: adopt, detach, adoptions and
// draft impact, and the view of a name that the pins of a profile read.

// sharedKind is what a shared verb works on: a fragment or a profile.
type sharedKind struct {
	name, plural, revision string
	entry                  apitypes.SourceEntryKind
	adoptNote              string // what an adoption of the kind does not move, "" for none
}

var (
	fragmentKind = sharedKind{"fragment", "fragments", "frv_", apitypes.SourceEntryKindFragment, adoptNote}
	profileKind  = sharedKind{"profile", "profiles", "prv_", apitypes.SourceEntryKindProfile, ""}
)

// adoptionPath is the route of a name's adoption in a draft, with suffix after it.
func (k sharedKind) adoptionPath(draft, name, suffix string) string {
	return "/drafts/" + url.PathEscape(draft) + "/adoptions/" + k.plural + "/" + url.PathEscape(name) + suffix
}

// sourceHead is the part of a fragment or profile head these verbs read; a shared one has no cluster.
type sourceHead struct {
	Id       string  `json:"id"`
	Cluster  *string `json:"cluster"`
	Name     string  `json:"name"`
	Revision *string `json:"revision"`
}

// readHeads reads every page of the kind's heads (GET serves no name filter), with the list's epoch.
func readHeads(c *client.Client, k sharedKind) ([]sourceHead, string, error) {
	items, epoch, err := c.ListAt("/" + k.plural)
	if err != nil {
		return nil, "", err
	}
	heads := make([]sourceHead, len(items))
	for i, raw := range items {
		if err := json.Unmarshal(raw, &heads[i]); err != nil {
			return nil, "", ef("GET /api/v1/%s: item %d cannot be read: %w", k.plural, i, err)
		}
	}
	return heads, epoch, nil
}

// libraryHead is the shared head of a name, nil when the library has none.
func libraryHead(heads []sourceHead, name string) *sourceHead {
	for i := range heads {
		if heads[i].Cluster == nil && heads[i].Name == name {
			return &heads[i]
		}
	}
	return nil
}

// slot is a draft entry of a name that is not the cluster's own: an edit of the shared head
// (library), an adoption or the removal of one (library false). A nil revision is a removal.
type slot struct {
	revision *string
	library  bool
}

// draftView is one draft read, with the heads read to answer for it: a cluster's view of a shared name
// (contract 12 of ginsys/bronzeward#1193).
type draftView struct {
	c     *client.Client
	d     *apitypes.DraftBody
	first string
	heads map[string][]sourceHead
}

// readView reads the draft and binds w to that read, which pins as it names a revision (see pins).
func readView(c *client.Client, w *client.Write, draft string) (*draftView, error) {
	d, first, moves, err := readDraft(c, draft)
	if err != nil {
		return nil, err
	}
	w.ReadsAt(first, moves)
	return &draftView{c: c, d: d, first: first, heads: map[string][]sourceHead{}}, nil
}

// headsOf reads a kind's heads once. The list answer is compared with the draft answer itself: the write
// checks the client's last epoch, which a later read can set back (client-contracts.md section 8.5).
func (v *draftView) headsOf(k sharedKind) ([]sourceHead, error) {
	if h, ok := v.heads[k.name]; ok {
		return h, nil
	}
	h, listed, err := readHeads(v.c, k)
	if err != nil {
		return nil, err
	}
	if v.first != "" && listed != "" && listed != v.first {
		return nil, ef("the epoch changed from %s to %s while the pins were read, so nothing was sent; read the draft again with: bronzewardctl draft show %s",
			client.ShellWord(v.first), client.ShellWord(listed), client.ShellWord(v.d.Id))
	}
	v.heads[k.name] = h
	return h, nil
}

// slots are the draft's entries of the name: the cluster's own and the shared one.
func (v *draftView) slots(k sharedKind, name string) (own, shared *slot, err error) {
	for _, item := range v.d.Entries {
		// An entry that cannot be read may be the one that decides, so a revision chosen without it
		// could be the wrong one: refuse, as entryRead does.
		x, err := item.AsSourceEntry()
		if err != nil {
			return nil, nil, ef("GET /api/v1/drafts/%s: an entry cannot be read: %w; nothing was sent", url.PathEscape(v.d.Id), err)
		}
		switch {
		case x.Kind == adoptionEntryKind:
			a, err := item.AsAdoptionEntry()
			if err != nil {
				return nil, nil, ef("GET /api/v1/drafts/%s: an entry cannot be read: %w; nothing was sent", url.PathEscape(v.d.Id), err)
			}
			if string(a.Of) == k.name && a.Name == name {
				shared = &slot{revision: a.Revision}
			}
		case x.Kind == k.entry && x.Name != nil && *x.Name == name && x.Scope != nil:
			shared = &slot{revision: x.Revision, library: true}
		case x.Kind == k.entry && x.Name != nil && *x.Name == name:
			own = &slot{revision: x.Revision}
		}
	}
	return own, shared, nil
}

// revision is the revision the draft's cluster composes for a name, "" for none: the draft's own entry,
// else its adoption or shared edit (a shared edit is adopted by the cluster that publishes it), else the
// cluster's own head, else the revision the cluster adopted, read from the adoptions of the shared head
// (a row with a via is no adoption). ownRemoved says the draft removes the cluster's own entry.
func (v *draftView) revision(k sharedKind, name string) (rev string, ownRemoved bool, err error) {
	own, shared, err := v.slots(k, name)
	switch {
	case err != nil:
		return "", false, err
	case own != nil && own.revision != nil:
		return *own.revision, false, nil
	case shared != nil && shared.revision != nil:
		return *shared.revision, false, nil
	}
	heads, err := v.headsOf(k)
	if err != nil {
		return "", false, err
	}
	if own == nil {
		for _, h := range heads {
			if h.Cluster != nil && *h.Cluster == v.d.Cluster && h.Name == name && h.Revision != nil {
				return *h.Revision, false, nil
			}
		}
	}
	lib := libraryHead(heads, name)
	if lib == nil || shared != nil && !shared.library { // no shared name, or the draft stops following it
		return "", own != nil, nil
	}
	rows, err := adoptionRows(v.c, k, lib.Id, nil)
	if err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if r.Cluster == v.d.Cluster && r.Via == nil {
			return r.Revision, false, nil
		}
	}
	return "", own != nil, nil
}

// sharedRevision is the revision a shared profile pins for a name: the draft's shared edit of it, else
// the shared head's revision; "" for none, and removed when the draft removes the shared head.
func (v *draftView) sharedRevision(k sharedKind, name string) (rev string, removed bool, err error) {
	if _, shared, err := v.slots(k, name); err != nil {
		return "", false, err
	} else if shared != nil && shared.library {
		if shared.revision == nil {
			return "", true, nil
		}
		return *shared.revision, false, nil
	}
	heads, err := v.headsOf(k)
	if err != nil {
		return "", false, err
	}
	if h := libraryHead(heads, name); h != nil && h.Revision != nil {
		return *h.Revision, false, nil
	}
	return "", false, nil
}

// adoptionRows reads the adoptions of a shared head: every page, or the page l asks for.
func adoptionRows(c *client.Client, k sharedKind, head string, q url.Values) ([]apitypes.ClusterAdoptionBody, error) {
	path := "/" + k.plural + "/" + url.PathEscape(head) + "/adoptions"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	items, err := c.List(path)
	if err != nil {
		return nil, err
	}
	return decode[apitypes.ClusterAdoptionBody](c, items...)
}

// clusterWords names a cluster by its name, else its identifier.
func clusterWords(c *client.Client, id string) string {
	if names, err := clusterNames(c); err == nil && names[id] != "" {
		return names[id]
	}
	return id
}

// sharedAdopt puts an adoption of a shared name in a draft (PUT /drafts/{id}/adoptions/{kind}/{name}), or
// with --remove removes it (DELETE). Without --revision it adopts the head's revision, which it resolves
// before it sends, so a repeat gives the revision that was sent.
func (e *env) sharedAdopt(k sharedKind, args []string) error {
	var draft, revision string
	var remove bool
	var rf repeatFlags
	pos, err := e.parse(k.name+" adopt", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&revision, "revision", "", "")
		fs.BoolVar(&remove, "remove", false, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	switch {
	case draft == "":
		return usageError{fmt.Errorf("%s adopt needs --draft", k.name)}
	case remove && revision != "":
		return usageError{errors.New("--remove stops following the name and takes no --revision")}
	case revision != "" && !strings.HasPrefix(revision, k.revision):
		return usageError{fmt.Errorf("--revision is a %s revision ID (%s...)", k.name, k.revision)}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	if rp != nil && !remove && revision == "" {
		// The head can have moved since the first attempt, and a changed body executes where that attempt
		// left no record (§8.4 item 4): a repeat gives the revision sent.
		return usageError{fmt.Errorf("a repeat of %s adopt gives --revision as the outcome-unknown block printed it; nothing was sent", k.name)}
	}
	name := pos[0]
	w := &client.Write{Method: http.MethodPut, Path: k.adoptionPath(draft, name, ""), Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp,
		Check: "draft show " + client.ShellWord(draft), Read: entryRead(draft, "", "")}
	if remove {
		w.Method = http.MethodDelete
	}
	var was, cluster string
	return e.writeAfter(func(c *client.Client) (err error) {
		v, err := readView(c, w, draft)
		if err != nil {
			return err
		}
		cluster = v.d.Cluster
		if was, _, err = v.revision(k, name); err != nil || remove {
			return err
		}
		if revision == "" {
			heads, err := v.headsOf(k)
			if err != nil {
				return err
			}
			if h := libraryHead(heads, name); h == nil || h.Revision == nil {
				return ef("there is no shared %s %s with a revision to adopt; nothing was sent", k.name, client.ShellWord(name))
			} else {
				revision = *h.Revision
			}
		}
		w.Input = "--revision " + client.ShellWord(revision)
		w.Body, err = json.Marshal(apitypes.AdoptionInput{Revision: revision})
		return err
	}, w, func(c *client.Client, r *client.Response) ([]string, error) {
		who, rev := clusterWords(c, cluster), client.DraftRevision(r.Header.Get("ETag"))
		if remove {
			return []string{sf("Cluster %s stops following %s %s, was %s (draft revision %s).", who, k.name, name, revisionWords(was), rev)}, nil
		}
		line := sf("Cluster %s adopts %s %s %s, was %s (draft revision %s).", who, k.name, name, revision, revisionWords(was), rev)
		if n, err := reached(c, draft, k, name); err == nil {
			line += " Reaches " + machineCount(n) + "."
		}
		if k.adoptNote == "" {
			return []string{line}, nil
		}
		return []string{line, k.adoptNote}, nil
	})
}

// reached is the number of this cluster's machines the draft's entry of the name gains or loses a
// revision for, from the impact read after the write.
func reached(c *client.Client, draft string, k sharedKind, name string) (int, error) {
	r, err := c.Get("/drafts/" + url.PathEscape(draft) + "/impact")
	if err != nil {
		return 0, err
	}
	var b apitypes.ImpactBody
	if err := json.Unmarshal(r.Body, &b); err != nil {
		return 0, err
	}
	n := 0
	for _, it := range b.Items {
		if string(it.Kind) == k.name && it.Name == name && it.Scope == apitypes.ImpactItemScopeLibrary {
			for _, m := range it.Machines {
				if m.Change != apitypes.ImpactMachineChangeKeeps {
					n++
				}
			}
		}
	}
	return n, nil
}

// sharedDetach copies an adopted name into the draft's cluster and drops the adoption in one step
// (POST /drafts/{id}/adoptions/{kind}/{name}/detachments).
func (e *env) sharedDetach(k sharedKind, args []string) error {
	var draft string
	var rf repeatFlags
	pos, err := e.parse(k.name+" detach", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if draft == "" {
		return usageError{fmt.Errorf("%s detach needs --draft", k.name)}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	name := pos[0]
	return e.write(&client.Write{Method: http.MethodPost, Path: k.adoptionPath(draft, name, "/detachments"), Draft: draft,
		IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft), Read: entryRead(draft, "", "")},
		func(c *client.Client, r *client.Response) ([]string, error) {
			var x apitypes.DetachmentUpdate
			if err := json.Unmarshal(r.Body, &x); err != nil {
				return nil, err
			}
			return []string{sf("Cluster %s stops following %s %s; its own %s %s now holds %s (draft revision %s).", clusterWords(c, draftCluster(c, x.Draft)), k.name, name,
				k.name, name, or(x.Copy.Revision, "none"), client.DraftRevision(r.Header.Get("ETag")))}, nil
		})
}

// draftCluster is the cluster of a draft, read after a write; when the read fails, words that name it.
func draftCluster(c *client.Client, draft string) string {
	if d, _, _, err := readDraft(c, draft); err == nil {
		return d.Cluster
	}
	return "this draft's cluster"
}

// ptr is a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }

// scopeWords is a source kind, shared when it is the library's.
func scopeWords(library bool, kind apitypes.SourceEntryKind) string {
	if library {
		return "shared " + string(kind)
	}
	return string(kind)
}

// revisionWords is a revision, or none for "".
func revisionWords(rev string) string {
	if rev == "" {
		return "none"
	}
	return rev
}

// machineCount is a number of machines, in words.
func machineCount(n int) string {
	if n == 1 {
		return "1 machine"
	}
	return sf("%d machines", n)
}

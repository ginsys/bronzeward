package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The draft writes of operator-experience.md §5.4 and §10 (author; not human only). Every write to
// an existing draft sends If-Match: --if-match, else the ETag kept from the last read or write of
// the draft (decision T11 of #256), which client.Send reads and refuses without.

// draftCreate opens a draft (POST /drafts) and keeps its ETag for the next write.
func (e *env) draftCreate(args []string) error {
	var in apitypes.DraftInput
	var rf repeatFlags
	if _, err := e.parse("draft create", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&in.Cluster, "cluster", "", "")
		fs.StringVar(&in.Title, "title", "", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(in)
	return e.write(&client.Write{Method: http.MethodPost, Path: "/drafts", Body: body, Repeat: rp,
		Check: "draft list --cluster " + client.ShellWord(in.Cluster),
		Read: func(c *client.Client) (string, error) {
			return find(c, "/drafts", "open draft with this title in this cluster", func(d apitypes.DraftBody) string {
				return map[bool]string{true: "the open draft " + d.Id + " with this title in this cluster"}[d.Cluster == in.Cluster && d.Title == in.Title && d.State == "open"]
			})
		}}, func(c *client.Client, r *client.Response) ([]string, error) {
		var d apitypes.DraftBody
		if err := json.Unmarshal(r.Body, &d); err != nil {
			return nil, err
		}
		keep(e, c, d.Id, r.Header.Get("ETag"))
		return []string{sf("Draft %s %s, revision %d.", d.Id, d.State, d.Revision)}, nil
	})
}

// draftDiscard closes a draft (POST /drafts/{id}/discard) and drops its kept ETag: the answer
// carries none, and a closed draft takes no write.
func (e *env) draftDiscard(args []string) error {
	var rf repeatFlags
	pos, err := e.parse("draft discard", args, 1, rf.flags)
	if err != nil {
		return err
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	d := pos[0]
	return e.write(&client.Write{Method: http.MethodPost, Path: "/drafts/" + url.PathEscape(d) + "/discard", Body: []byte("{}"),
		Draft: d, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(d), Read: entryRead(d, "", "")},
		func(c *client.Client, r *client.Response) ([]string, error) {
			keep(e, c, d, "")
			var x apitypes.DraftBody
			err := json.Unmarshal(r.Body, &x)
			return []string{sf("Draft %s %s (revision %d).", x.Id, x.State, x.Revision)}, err
		})
}

// profileSet puts a profile in a draft (PUT /drafts/{id}/profiles/{name}). It pins revisions, and
// --fragments names them by fragment name or frv_ ID (T17).
func (e *env) profileSet(args []string) error {
	var draft, fragments string
	var library bool
	var rf repeatFlags
	pos, err := e.parse("profile set", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&fragments, "fragments", "", "")
		fs.BoolVar(&library, "library", false, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if draft == "" || fragments == "" {
		return usageError{errors.New("profile set needs --draft and --fragments")}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	names := strings.Split(fragments, ",")
	if rp != nil {
		// A name resolved now can pin another revision than the first attempt did, and a changed body
		// executes where that attempt left no record (§8.4 item 4): a repeat gives the revisions sent.
		for _, n := range names {
			if !strings.HasPrefix(n, "frv_") {
				return usageError{fmt.Errorf("a repeat of profile set gives --fragments as the revisions the outcome-unknown block printed, not %s; nothing was sent", client.ShellWord(n))}
			}
		}
	}
	var pinned []string
	w := &client.Write{Method: http.MethodPut, Path: "/drafts/" + url.PathEscape(draft) + "/profiles/" + url.PathEscape(pos[0]),
		Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft),
		Read: entryReadScoped(draft, apitypes.SourceEntryKindProfile, pos[0], library)}
	return e.writeAfter(func(c *client.Client) (err error) {
		if rp != nil {
			pinned = names
		} else if pinned, err = pins(c, w, draft, names, library); err != nil {
			return err
		}
		in := apitypes.ProfileInput{Fragments: pinned}
		if library {
			in.Scope = ptr(apitypes.ProfileInputScopeLibrary)
		}
		w.Input = "--fragments " + client.ShellWord(strings.Join(pinned, ","))
		w.Body, err = json.Marshal(in)
		return err
	}, w, func(c *client.Client, r *client.Response) ([]string, error) {
		var pairs []string
		for i, n := range names {
			if n != pinned[i] {
				n += " " + pinned[i]
			}
			pairs = append(pairs, n)
		}
		saved := sf("%s %s saved: pins %s (draft revision %s).", map[bool]string{true: "Shared profile", false: "Profile"}[library], pos[0], strings.Join(pairs, ", "),
			client.DraftRevision(r.Header.Get("ETag")))
		if library {
			saved += sf(" Publishing this draft moves cluster %s to it. No other cluster changes.", clusterWords(c, draftCluster(c, draft)))
		}
		return []string{saved, "A profile pins revisions: after changing a fragment, set the profile again to use it."}, nil
	})
}

// pins resolves --fragments (T17): an frv_ ID is used as given; a name is the revision the draft's
// cluster composes for that fragment (draftView.revision, contract 12 of ginsys/bronzeward#1193), and
// with library the shared revision a shared profile can pin. The server stays authoritative and
// refuses a pin it does not accept. It binds w to the epoch of the draft answer and to the count of
// moves as that answer left it (not the count after Get's own GET /server), which the write checks
// before it sends: a change that a later answer set back is still seen.
func pins(c *client.Client, w *client.Write, draft string, names []string, library bool) ([]string, error) {
	v, err := readView(c, w, draft)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(names))
	for i, n := range names {
		if strings.HasPrefix(n, "frv_") {
			out[i] = n
			continue
		}
		var removed bool
		if library {
			out[i], err = v.sharedRevision(fragmentKind, n)
		} else {
			out[i], removed, err = v.revision(fragmentKind, n)
		}
		switch {
		case err != nil:
			return nil, err
		case out[i] != "":
		case removed:
			return nil, ef("draft %s removes the fragment %s, so a profile cannot pin it; nothing was sent", v.d.Id, client.ShellWord(n))
		case library:
			return nil, ef("there is no shared fragment %s with a revision, and draft %s proposes none; nothing was sent", client.ShellWord(n), v.d.Id)
		default:
			return nil, ef("cluster %s has no fragment %s with a revision, adopts none and draft %s proposes none; nothing was sent", v.d.Cluster, client.ShellWord(n), v.d.Id)
		}
	}
	return out, nil
}

// layerFragments is assignment set's repeated --fragment <layer>=<name>[,<name>].
type layerFragments map[string][]string

func (l layerFragments) String() string { return "" }
func (l layerFragments) Set(v string) error {
	layer, names, ok := strings.Cut(v, "=")
	if !ok || names == "" || !slices.Contains([]string{"global", "site", "cluster", "role", "workload", "override"}, layer) {
		return errors.New("give <layer>=<name>[,<name>], the layer one of global, site, cluster, role, workload and override")
	}
	l[layer] = append(l[layer], strings.Split(names, ",")...)
	return nil
}

// assignmentSet puts a machine's assignment in a draft (PUT /drafts/{id}/assignments/{machine}):
// profiles and per-layer fragments, by name, or with --none an assignment that selects nothing, so the
// machine's configuration is its import base, with its own name as its hostname where the base sets none (compilation §6.1 decision 2).
func (e *env) assignmentSet(args []string) error {
	var draft, profiles, cluster string
	var none bool
	fragments := layerFragments{}
	var rf repeatFlags
	pos, err := e.parse("assignment set", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&cluster, "cluster", "", "")
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&profiles, "profiles", "", "")
		fs.Var(fragments, "fragment", "")
		fs.BoolVar(&none, "none", false, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	selects := profiles != "" || len(fragments) > 0
	if draft == "" || selects == none {
		return usageError{errors.New("assignment set needs --draft and one of --profiles or --fragment, or --none to select nothing (the machine's configuration is then its import base, with its own name as its hostname where the base sets none)")}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	in := map[string]any{}
	if profiles != "" {
		in["profiles"] = strings.Split(profiles, ",")
	}
	if len(fragments) > 0 {
		in["fragments"] = fragments
	}
	body, _ := json.Marshal(in)
	return e.writeNamed(pos[0], cluster, nil, func(id string) *client.Write {
		return &client.Write{Method: http.MethodPut, Path: "/drafts/" + url.PathEscape(draft) + "/assignments/" + url.PathEscape(id),
			Body: body, Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft),
			Read: entryRead(draft, apitypes.SourceEntryKindAssignment, id)}
	}, func(_ *client.Client, r *client.Response) ([]string, error) {
		return []string{fmt.Sprintf("Assignment saved (draft revision %s).", client.DraftRevision(r.Header.Get("ETag")))}, nil
	})
}

// sourceRm removes a profile or an assignment from a draft (DELETE /drafts/{id}/<kind>s/<name>).
func (e *env) sourceRm(kind apitypes.SourceEntryKind, args []string) error {
	var draft, cluster string
	var library bool
	var rf repeatFlags
	pos, err := e.parse(string(kind)+" rm", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		if kind == apitypes.SourceEntryKindAssignment { // an assignment is a machine's; a profile is named
			fs.StringVar(&cluster, "cluster", "", "")
		} else { // a fragment or a profile can be shared
			fs.BoolVar(&library, "library", false, "")
		}
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if draft == "" {
		return usageError{fmt.Errorf("%s rm needs --draft", kind)}
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	removal := func(id string) *client.Write {
		path := "/drafts/" + url.PathEscape(draft) + "/" + string(kind) + "s/" + url.PathEscape(id)
		if library { // the one mutation that takes a query, which the key's fingerprint covers (contract 5)
			path += "?scope=library"
		}
		return &client.Write{Method: http.MethodDelete, Path: path,
			Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft),
			Read: entryReadScoped(draft, kind, id, library)}
	}
	label := pos[0]
	say := func(_ *client.Client, r *client.Response) ([]string, error) {
		what := strings.ToUpper(string(kind[:1])) + string(kind[1:])
		if library {
			what = "Shared " + string(kind)
		}
		return []string{sf("%s %s removed from draft %s (draft revision %s).", what, label, draft,
			client.DraftRevision(r.Header.Get("ETag")))}, nil
	}
	if kind != apitypes.SourceEntryKindAssignment { // a profile is named in the draft, never resolved
		return e.write(removal(pos[0]), say)
	}
	return e.writeNamed(pos[0], cluster, nil, func(id string) *client.Write {
		label = givenLabel(pos[0], id)
		return removal(id)
	}, say)
}

// entryRead reads the draft after an unknown outcome and says what it shows of the entry kind and
// name, or of the draft itself when kind is "".
func entryRead(draft string, kind apitypes.SourceEntryKind, name string) func(*client.Client) (string, error) {
	return entryReadScoped(draft, kind, name, false)
}

// entryReadScoped is entryRead for the draft's shared entry of the name when library is set, else for
// the cluster's own: the two are different slots of the draft.
func entryReadScoped(draft string, kind apitypes.SourceEntryKind, name string, library bool) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		d, _, _, err := readDraft(c, draft)
		if err != nil {
			return "", err
		}
		shows := sf("GET /api/v1/drafts/%s shows the draft %s at revision %d", url.PathEscape(draft), d.State, d.Revision)
		if kind == "" {
			return shows, nil
		}
		for _, item := range d.Entries {
			x, err := item.AsSourceEntry()
			if err != nil {
				return "", ef("GET /api/v1/drafts/%s: an entry cannot be read: %w", url.PathEscape(draft), err)
			}
			// A write without --library is the cluster's own entry, and a shared one of the name is another slot.
			if x.Kind == kind && (x.Scope != nil) == library && (x.Name != nil && *x.Name == name || x.Machine != nil && *x.Machine == name) {
				return sf("%s, with %s %s at %s", shows, scopeWords(library, kind), name, or(x.Revision, "removed")), nil
			}
		}
		return sf("%s, with no %s %s entry", shows, scopeWords(library, kind), name), nil
	}
}

// readDraft reads a draft and returns it with the epoch its answer carried and the count of moves as
// that answer left it. Get may read GET /server after the answer, so the client's own count can be
// higher by the time readDraft returns.
func readDraft(c *client.Client, draft string) (*apitypes.DraftBody, string, int, error) {
	r, err := c.Get("/drafts/" + url.PathEscape(draft))
	if err != nil {
		return nil, "", 0, err
	}
	var d apitypes.DraftBody
	if err := json.Unmarshal(r.Body, &d); err != nil {
		return nil, "", 0, ef("GET /api/v1/drafts/%s cannot be read: %w", url.PathEscape(draft), err)
	}
	return &d, r.Header.Get("Bronzeward-Epoch"), r.Moves, nil
}

// keep keeps etag as the draft's last-seen ETag, "" forgetting it (T11). A failure is shown: the
// next write to the draft then needs a read first.
func keep(e *env, c *client.Client, draft, etag string) {
	if err := client.KeepETag(c.Context, draft, etag); err != nil {
		tf(e.err, "bronzewardctl: %s: keeping the ETag of %s: %v\n", c.Context.Where(), draft, err)
	}
}

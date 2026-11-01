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
		return []string{fmt.Sprintf("Draft %s %s, revision %d.", d.Id, d.State, d.Revision)}, nil
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
			return []string{fmt.Sprintf("Draft %s %s (revision %d).", x.Id, x.State, x.Revision)}, err
		})
}

// profileSet puts a profile in a draft (PUT /drafts/{id}/profiles/{name}). It pins revisions, and
// --fragments names them by fragment name or frv_ ID (T17).
func (e *env) profileSet(args []string) error {
	var draft, fragments string
	var rf repeatFlags
	pos, err := e.parse("profile set", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&fragments, "fragments", "", "")
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
	var pinned []string
	w := &client.Write{Method: http.MethodPut, Path: "/drafts/" + url.PathEscape(draft) + "/profiles/" + url.PathEscape(pos[0]),
		Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft),
		Read: entryRead(draft, apitypes.SourceEntryKindProfile, pos[0])}
	return e.writeAfter(func(c *client.Client) (err error) {
		if pinned, err = pins(c, draft, names); err == nil {
			w.Body, err = json.Marshal(apitypes.ProfileInput{Fragments: pinned})
		}
		return err
	}, w, func(_ *client.Client, r *client.Response) ([]string, error) {
		var pairs []string
		for i, n := range names {
			if n != pinned[i] {
				n += " " + pinned[i]
			}
			pairs = append(pairs, n)
		}
		return []string{fmt.Sprintf("Profile %s saved: pins %s (draft revision %s).", pos[0], strings.Join(pairs, ", "), client.DraftRevision(r.Header.Get("ETag"))),
			"A profile pins revisions: after changing a fragment, set the profile again to use it."}, nil
	})
}

// pins resolves --fragments (T17): an frv_ ID is used as given; a name is the revision the draft
// proposes for that fragment, refused when the draft removes it, or else the fragment head's
// revision in the draft's cluster. The server stays authoritative and refuses a pin it does not
// accept.
func pins(c *client.Client, draft string, names []string) ([]string, error) {
	d, err := readDraft(c, draft)
	if err != nil {
		return nil, err
	}
	proposed := map[string]*string{}
	for _, item := range d.Entries {
		if x, err := item.AsSourceEntry(); err == nil && x.Kind == apitypes.SourceEntryKindFragment && x.Name != nil {
			proposed[*x.Name] = x.Revision
		}
	}
	var heads map[string]*string
	out := make([]string, len(names))
	for i, n := range names {
		rev, inDraft := proposed[n]
		switch {
		case strings.HasPrefix(n, "frv_"):
			out[i] = n
			continue
		case inDraft && rev == nil:
			return nil, fmt.Errorf("draft %s removes the fragment %s, so a profile cannot pin it; nothing was sent", d.Id, client.ShellWord(n))
		case inDraft:
			out[i] = *rev
			continue
		case heads == nil:
			if heads, err = fragmentHeads(c, d.Cluster); err != nil {
				return nil, err
			}
		}
		if heads[n] == nil {
			return nil, fmt.Errorf("cluster %s has no fragment %s with a revision, and draft %s proposes none; nothing was sent", d.Cluster, client.ShellWord(n), d.Id)
		}
		out[i] = *heads[n]
	}
	return out, nil
}

// fragmentHeads maps the names of cluster's fragments to their head revisions, read from every
// page of GET /fragments, which serves no name filter (api/openapi.yaml).
func fragmentHeads(c *client.Client, cluster string) (map[string]*string, error) {
	items, err := c.List("/fragments")
	if err != nil {
		return nil, err
	}
	heads := map[string]*string{}
	for i, raw := range items {
		var f apitypes.FragmentBody
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("GET /api/v1/fragments: item %d cannot be read: %w", i, err)
		}
		if f.Cluster == cluster {
			heads[f.Name] = f.Revision
		}
	}
	return heads, nil
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
// profiles and per-layer fragments, by name.
func (e *env) assignmentSet(args []string) error {
	var draft, profiles string
	fragments := layerFragments{}
	var rf repeatFlags
	pos, err := e.parse("assignment set", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&profiles, "profiles", "", "")
		fs.Var(fragments, "fragment", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if draft == "" || profiles == "" && len(fragments) == 0 {
		return usageError{errors.New("assignment set needs --draft and --profiles or --fragment")}
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
	return e.write(&client.Write{Method: http.MethodPut, Path: "/drafts/" + url.PathEscape(draft) + "/assignments/" + url.PathEscape(pos[0]),
		Body: body, Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft),
		Read: entryRead(draft, apitypes.SourceEntryKindAssignment, pos[0])}, func(_ *client.Client, r *client.Response) ([]string, error) {
		return []string{fmt.Sprintf("Assignment saved (draft revision %s).", client.DraftRevision(r.Header.Get("ETag")))}, nil
	})
}

// sourceRm removes a profile or an assignment from a draft (DELETE /drafts/{id}/<kind>s/<name>).
func (e *env) sourceRm(kind apitypes.SourceEntryKind, args []string) error {
	var draft string
	var rf repeatFlags
	pos, err := e.parse(string(kind)+" rm", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
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
	return e.write(&client.Write{Method: http.MethodDelete, Path: "/drafts/" + url.PathEscape(draft) + "/" + string(kind) + "s/" + url.PathEscape(pos[0]),
		Draft: draft, IfMatch: rf.ifMatch.v, Repeat: rp, Check: "draft show " + client.ShellWord(draft),
		Read: entryRead(draft, kind, pos[0])}, func(_ *client.Client, r *client.Response) ([]string, error) {
		return []string{fmt.Sprintf("%s %s removed from draft %s (draft revision %s).", strings.ToUpper(string(kind[:1]))+string(kind[1:]), pos[0], draft,
			client.DraftRevision(r.Header.Get("ETag")))}, nil
	})
}

// entryRead reads the draft after an unknown outcome and says what it shows of the entry kind and
// name, or of the draft itself when kind is "".
func entryRead(draft string, kind apitypes.SourceEntryKind, name string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		d, err := readDraft(c, draft)
		if err != nil {
			return "", err
		}
		shows := fmt.Sprintf("GET /api/v1/drafts/%s shows the draft %s at revision %d", url.PathEscape(draft), d.State, d.Revision)
		if kind == "" {
			return shows, nil
		}
		for _, item := range d.Entries {
			x, err := item.AsSourceEntry()
			if err != nil {
				return "", fmt.Errorf("GET /api/v1/drafts/%s: an entry cannot be read: %w", url.PathEscape(draft), err)
			}
			if x.Kind == kind && (x.Name != nil && *x.Name == name || x.Machine != nil && *x.Machine == name) {
				return fmt.Sprintf("%s, with %s %s at %s", shows, kind, name, or(x.Revision, "removed")), nil
			}
		}
		return fmt.Sprintf("%s, with no %s %s entry", shows, kind, name), nil
	}
}

func readDraft(c *client.Client, draft string) (*apitypes.DraftBody, error) {
	r, err := c.Get("/drafts/" + url.PathEscape(draft))
	if err != nil {
		return nil, err
	}
	var d apitypes.DraftBody
	if err := json.Unmarshal(r.Body, &d); err != nil {
		return nil, fmt.Errorf("GET /api/v1/drafts/%s cannot be read: %w", url.PathEscape(draft), err)
	}
	return &d, nil
}

// keep keeps etag as the draft's last-seen ETag, "" forgetting it (T11). A failure is shown: the
// next write to the draft then needs a read first.
func keep(e *env, c *client.Client, draft, etag string) {
	if err := client.KeepETag(c.Context, draft, etag); err != nil {
		fmt.Fprintf(e.err, "bronzewardctl: %s: keeping the ETag of %s: %v\n", c.Context.Where(), draft, err)
	}
}

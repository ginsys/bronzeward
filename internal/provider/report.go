package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ginsys/bronzeward/internal/id"
)

// Report is the orphan-report identity's provider client (persistence-api.md §6.4): it lists the
// generation tree, gen/<cluster>/<claim>/<value>, and does nothing else. It never reads a
// generation's metadata: a listing carries names only, so no custom metadata can reach its caller.
type Report struct{ c *client }

// NewReport builds the client without contacting the provider.
func NewReport(addr string, tok Token) (*Report, error) {
	c, err := newClient(addr, tok)
	if err != nil {
		return nil, err
	}
	return &Report{c: c}, nil
}

// Listing is one directory's identifiers, in the order the provider listed them, and the number of
// names it listed that are not of Bronzeward's form there. Those are counted, never returned: a
// name under gen/ that Bronzeward did not mint is not a generation path.
type Listing struct {
	Names   []string
	Skipped int
}

// Generations is one claim directory's generation paths, and the names skipped as in Listing.
type Generations struct {
	Paths   []GenerationPath
	Skipped int
}

// Clusters lists gen/: the cluster directories.
func (r *Report) Clusters(ctx context.Context) (Listing, error) {
	return r.dirs(ctx, "gen/", id.Cluster)
}

// Claims lists gen/<cluster>/: the claim directories.
func (r *Report) Claims(ctx context.Context, cluster string) (Listing, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return Listing{}, fmt.Errorf("provider: listing claims: %w", err)
	}
	return r.dirs(ctx, "gen/"+cluster+"/", id.Ingestion)
}

// Values lists gen/<cluster>/<claim>/: the generations.
func (r *Report) Values(ctx context.Context, cluster, claim string) (Generations, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return Generations{}, fmt.Errorf("provider: listing generations: %w", err)
	}
	if err := id.MustHave(claim, id.Ingestion); err != nil {
		return Generations{}, fmt.Errorf("provider: listing generations: %w", err)
	}
	names, err := r.list(ctx, "gen/"+cluster+"/"+claim+"/")
	if err != nil {
		return Generations{}, err
	}
	var g Generations
	for _, n := range names {
		p, err := NewGenerationPath(cluster, claim, n)
		if err != nil {
			g.Skipped++
			continue
		}
		g.Paths = append(g.Paths, p)
	}
	return g, nil
}

// dirs lists dir and keeps the subdirectories named by an identifier with prefix p.
func (r *Report) dirs(ctx context.Context, dir string, p id.Prefix) (Listing, error) {
	names, err := r.list(ctx, dir)
	if err != nil {
		return Listing{}, err
	}
	var l Listing
	for _, n := range names {
		sub, ok := strings.CutSuffix(n, "/")
		if !ok || id.MustHave(sub, p) != nil {
			l.Skipped++
			continue
		}
		l.Names = append(l.Names, sub)
	}
	return l, nil
}

// list is KV v2's LIST of dir below secret/metadata/, sent as GET with list=true so that a missing
// directory is the 404 every read already maps to ErrAbsent; an empty directory is that 404.
func (r *Report) list(ctx context.Context, dir string) ([]string, error) {
	resp, err := r.c.do(ctx, http.MethodGet, "/v1/secret/metadata/"+dir+"?list=true", nil, false)
	if errors.Is(err, ErrAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Keys *[]string `json:"keys"`
		} `json:"data"`
	}
	if err := resp.decode(&out); err != nil {
		return nil, err
	}
	if out.Data.Keys == nil {
		return nil, resp.bad("the listing has no keys")
	}
	return *out.Data.Keys, nil
}

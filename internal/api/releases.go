package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// rendererBody is a release's renderer and contract record (compilation.md §10.2).
type rendererBody struct {
	Contract          string `json:"contract"`
	MachineryVersion  string `json:"machineryVersion"`
	MachineryChecksum string `json:"machineryChecksum"`
	KubernetesVersion string `json:"kubernetesVersion"`
}

// releaseSourceBody is one head a release used: the revision it left the head at (null: removed)
// and the head revision (PA §6.2).
type releaseSourceBody struct {
	Kind         string  `json:"kind"`
	Head         string  `json:"head"`
	Revision     *string `json:"revision"`
	HeadRevision int     `json:"headRevision"`
}

// releaseMachineBody is one machine a release covers, with the mode it was validated in and the
// route of its review data.
type releaseMachineBody struct {
	Machine string `json:"machine"`
	Mode    string `json:"mode"`
	Review  string `json:"review"`
}

// releaseBody is PA §9.2's release resource, as its read example answers it. It holds no
// ciphertext and no digest (§9.1).
type releaseBody struct {
	ID            string               `json:"id"`
	Cluster       string               `json:"cluster"`
	Draft         string               `json:"draft"`
	DraftRevision int                  `json:"draftRevision"`
	Operation     string               `json:"operation"`
	PublishedBy   createdBy            `json:"publishedBy"`
	PublishedAt   time.Time            `json:"publishedAt"`
	Renderer      rendererBody         `json:"renderer"`
	Sources       []releaseSourceBody  `json:"sources"`
	Machines      []releaseMachineBody `json:"machines"`
}

// reviewBody is one release machine's redacted review data (compilation.md §8.2, §8.3, §11), as
// publication stored it: the redacted configuration and the provenance records. A configuration
// that could not be redacted shows nothing and says so.
type reviewBody struct {
	Release       string          `json:"release"`
	Machine       string          `json:"machine"`
	Mode          string          `json:"mode"`
	Configuration *string         `json:"configuration"`
	Notice        string          `json:"notice,omitempty"`
	Provenance    json.RawMessage `json:"provenance"`
}

// withheldNotice says that a configuration is not shown (compilation.md §8.3).
const withheldNotice = "the configuration could not be redacted, so it is not shown"

const selectRelease = `SELECT id, cluster, draft, draft_revision, operation, published_by, published_role, published_at,
		contract, machinery_version, machinery_checksum, kubernetes_version
	FROM release`

func scanRelease(r interface{ Scan(...any) error }) (*releaseBody, error) {
	b := &releaseBody{Sources: []releaseSourceBody{}, Machines: []releaseMachineBody{}}
	err := r.Scan(&b.ID, &b.Cluster, &b.Draft, &b.DraftRevision, &b.Operation, &b.PublishedBy.Principal, &b.PublishedBy.Role,
		&b.PublishedAt, &b.Renderer.Contract, &b.Renderer.MachineryVersion, &b.Renderer.MachineryChecksum,
		&b.Renderer.KubernetesVersion)
	b.PublishedAt = b.PublishedAt.UTC()
	return b, err
}

// withReleaseParts reads the sources of rs, in head order, and their machines, in machine order.
// A release's rows are immutable and written with it (PA §3), so a second statement reads the
// same rows the first saw.
func withReleaseParts(ctx context.Context, tx *sql.Tx, rs []*releaseBody) error {
	if len(rs) == 0 {
		return nil
	}
	byID := map[string]*releaseBody{}
	var ids []string
	for _, r := range rs {
		byID[r.ID] = r
		ids = append(ids, r.ID)
	}
	err := eachRow(ctx, tx, `SELECT release, kind, COALESCE(fragment, profile, assignment) AS head,
			COALESCE(fragment_revision, profile_revision, assignment_revision), head_revision
		FROM release_source WHERE release = ANY (string_to_array($1, ',')) ORDER BY release, head`,
		strings.Join(ids, ","), func(row *sql.Rows) error {
			var rel string
			var s releaseSourceBody
			if err := row.Scan(&rel, &s.Kind, &s.Head, &s.Revision, &s.HeadRevision); err != nil {
				return err
			}
			byID[rel].Sources = append(byID[rel].Sources, s)
			return nil
		})
	if err != nil {
		return err
	}
	return eachRow(ctx, tx, `SELECT release, machine, mode FROM release_machine
		WHERE release = ANY (string_to_array($1, ',')) ORDER BY release, machine`,
		strings.Join(ids, ","), func(row *sql.Rows) error {
			var rel string
			var m releaseMachineBody
			if err := row.Scan(&rel, &m.Machine, &m.Mode); err != nil {
				return err
			}
			m.Review = prefix + "/releases/" + rel + "/machines/" + m.Machine + "/review"
			byID[rel].Machines = append(byID[rel].Machines, m)
			return nil
		})
}

func listReleases(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		out, err := listRows(ctx, tx, q, id.Release, selectRelease+` WHERE id > $1 ORDER BY id LIMIT $2`,
			func(r *sql.Rows) (*releaseBody, string, error) {
				b, err := scanRelease(r)
				return b, b.ID, err
			})
		if err != nil {
			return "", nil, err
		}
		return "", out, withReleaseParts(ctx, tx, out.Items)
	})
}

var getRelease = item(id.Release, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
	b, err := scanRelease(tx.QueryRowContext(ctx, selectRelease+` WHERE id = $1`, v))
	if err != nil {
		return "", nil, err
	}
	return "", b, withReleaseParts(ctx, tx, []*releaseBody{b})
})

// getReview answers one release machine's review data. A release that does not cover the machine,
// as an unknown release or machine, is 404.
func getReview(a *API, w http.ResponseWriter, q *request) {
	rel, m := q.r.PathValue("id"), q.r.PathValue("m")
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		if q.r.URL.RawQuery != "" {
			return "", nil, refuse(http.StatusBadRequest, "invalid-request", "an item read takes no query")
		}
		if id.MustHave(rel, id.Release) != nil || id.MustHave(m, id.Machine) != nil {
			return "", nil, refuse(http.StatusNotFound, "not-found", "no such resource")
		}
		var b reviewBody
		var provenance string
		err := tx.QueryRowContext(ctx, `SELECT release, machine, mode, redacted, provenance::text FROM release_machine
			WHERE release = $1 AND machine = $2`, rel, m).Scan(&b.Release, &b.Machine, &b.Mode, &b.Configuration, &provenance)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, refuse(http.StatusNotFound, "not-found", "no such resource")
		}
		if err != nil {
			return "", nil, err
		}
		if b.Configuration == nil {
			b.Notice = withheldNotice
		}
		b.Provenance = json.RawMessage(provenance)
		return "", b, nil
	})
}

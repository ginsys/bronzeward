package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// TalosAccessPath is the KV path of a cluster's Talos access credential, access/talos/<cluster>
// (persistence-api.md §3.3). It is derived from a cl_ identifier only, so no caller can point the
// read at another secret.
func TalosAccessPath(cluster string) (string, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return "", fmt.Errorf("provider: talos access path cluster: %w", err)
	}
	return "access/talos/" + cluster, nil
}

// TalosAccessVersion identifies one version of a cluster's Talos credential: its path, its KV
// version and that version's created_time. The version number alone is not an identity: deleting
// the path's metadata, or a provider restore, and writing again issues the same number for other
// bytes (persistence-api.md §3.3). It holds no secret and renders.
type TalosAccessVersion struct {
	Path        string
	Version     int
	CreatedTime time.Time
}

// TalosAccess is a cluster's Talos credential as read: its version identity and its talosconfig.
// It does not render: every fmt verb prints a placeholder and the marshallers fail. The text sits
// behind a pointer to a string: fmt prints a struct holding a TalosAccess in an unexported field
// by reflection, past those methods, and under a verb a pointer does not take (%s, %q) it
// dereferences a pointer to a slice, array, struct or map, but never one to a string.
// Talosconfig is the one way out.
type TalosAccess struct {
	v TalosAccessVersion
	s *string
}

// NewTalosAccess is a TalosAccess holding a copy of talosconfig at v, for a caller that stands in
// for the provider (a test of ingestion). It reads nothing.
func NewTalosAccess(v TalosAccessVersion, talosconfig []byte) TalosAccess {
	s := string(talosconfig)
	return TalosAccess{v: v, s: &s}
}

// Version is the identity of the version read.
func (a TalosAccess) Version() TalosAccessVersion { return a.v }

// Talosconfig is a copy of the talosconfig document.
func (a TalosAccess) Talosconfig() []byte {
	if a.s == nil {
		return nil
	}
	return []byte(*a.s)
}

const accessText = "[talos access]"

var errAccessRender = errors.New("provider: a talos access is not marshalled")

func (TalosAccess) Format(f fmt.State, _ rune)   { io.WriteString(f, accessText) }
func (TalosAccess) MarshalJSON() ([]byte, error) { return nil, errAccessRender }
func (TalosAccess) MarshalText() ([]byte, error) { return nil, errAccessRender }
func (TalosAccess) MarshalYAML() (any, error)    { return nil, errAccessRender }

// TalosAccess reads the latest version of cluster's Talos credential: a KV v2 read of
// secret/data/access/talos/<cluster>, whose data is exactly {"talosconfig": "<document>"}. No
// version is ErrAbsent; a policy refusal is ErrDenied; an answer that is not one live version
// holding a non-empty talosconfig is ErrProtocol. It is the one secret ingestion reads.
func (i *Ingestion) TalosAccess(ctx context.Context, cluster string) (TalosAccess, error) {
	p, err := TalosAccessPath(cluster)
	if err != nil {
		return TalosAccess{}, err
	}
	r, err := i.c.do(ctx, http.MethodGet, "/v1/secret/data/"+p, nil, false)
	if err != nil {
		return TalosAccess{}, err
	}
	var out struct {
		Data *struct {
			Data     map[string]json.RawMessage `json:"data"`
			Metadata *struct {
				Version      *int    `json:"version"`
				CreatedTime  *string `json:"created_time"`
				DeletionTime *string `json:"deletion_time"`
				Destroyed    *bool   `json:"destroyed"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		return TalosAccess{}, err
	}
	if out.Data == nil || out.Data.Metadata == nil {
		return TalosAccess{}, r.bad("no data or metadata")
	}
	m := out.Data.Metadata
	if m.Version == nil || *m.Version < 1 {
		return TalosAccess{}, r.bad("no version")
	}
	if m.CreatedTime == nil {
		return TalosAccess{}, r.bad("no created_time")
	}
	created, err := time.Parse(time.RFC3339Nano, *m.CreatedTime)
	if err != nil || created.IsZero() {
		return TalosAccess{}, r.bad("the created_time is not a time")
	}
	// A live version states it: deletion_time "" and destroyed false. Absent or null is not live.
	if m.DeletionTime == nil || m.Destroyed == nil {
		return TalosAccess{}, r.bad("no deletion_time or destroyed")
	}
	if *m.DeletionTime != "" || *m.Destroyed {
		return TalosAccess{}, r.bad("the version is deleted or destroyed")
	}
	raw, ok := out.Data.Data["talosconfig"]
	if !ok || len(out.Data.Data) != 1 {
		return TalosAccess{}, r.bad("the data is not exactly a talosconfig")
	}
	var tc string
	dec := json.NewDecoder(bytes.NewReader(raw))
	if dec.Decode(&tc) != nil || tc == "" {
		return TalosAccess{}, r.bad("the talosconfig is not a non-empty string")
	}
	return TalosAccess{v: TalosAccessVersion{Path: p, Version: *m.Version, CreatedTime: created}, s: &tc}, nil
}

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	return readTalosAccess(ctx, i.c, cluster)
}

// kvLatest is the latest version of a KV v2 secret as read: its number, its created_time and its
// data. r is the response, for the caller's own ErrProtocol words.
type kvLatest struct {
	version int
	created time.Time
	data    map[string]json.RawMessage
	r       *response
}

// stringMember is the data member name as a non-empty string; any other member shape is ErrProtocol.
func (l kvLatest) stringMember(name string) (string, error) {
	raw, ok := l.data[name]
	var s string
	if !ok || json.NewDecoder(bytes.NewReader(raw)).Decode(&s) != nil || s == "" {
		return "", l.r.bad("the data member " + name + " is not a non-empty string")
	}
	return s, nil
}

// readLatest is a KV v2 read of secret/data/<path>: the latest version, which must be live (no
// version is ErrAbsent). A policy refusal is ErrDenied, and an answer that is not one live version
// with its metadata is ErrProtocol.
func readLatest(ctx context.Context, c *client, path string) (kvLatest, error) {
	return readKV(ctx, c, path, 0)
}

// readKV is readLatest for one named version (a KV v2 read of secret/data/<path>?version=N), or for
// the latest when version is 0. A named version that is missing, soft-deleted or destroyed is
// OpenBao's 404, ErrAbsent, and no other version is read in its place; an answer that is not that
// version, live, is ErrProtocol.
func readKV(ctx context.Context, c *client, path string, version int) (kvLatest, error) {
	url := "/v1/secret/data/" + path
	if version > 0 {
		url += "?version=" + strconv.Itoa(version)
	}
	r, err := c.do(ctx, http.MethodGet, url, nil, false)
	if err != nil {
		return kvLatest{}, err
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
		return kvLatest{}, err
	}
	if out.Data == nil || out.Data.Metadata == nil {
		return kvLatest{}, r.bad("no data or metadata")
	}
	m := out.Data.Metadata
	if m.Version == nil || *m.Version < 1 {
		return kvLatest{}, r.bad("no version")
	}
	if version > 0 && *m.Version != version {
		return kvLatest{}, r.bad("the answer is not the asked version")
	}
	if m.CreatedTime == nil {
		return kvLatest{}, r.bad("no created_time")
	}
	created, err := time.Parse(time.RFC3339Nano, *m.CreatedTime)
	if err != nil || created.IsZero() {
		return kvLatest{}, r.bad("the created_time is not a time")
	}
	// A live version states it: deletion_time "" and destroyed false. Absent or null is not live.
	if m.DeletionTime == nil || m.Destroyed == nil {
		return kvLatest{}, r.bad("no deletion_time or destroyed")
	}
	if *m.DeletionTime != "" || *m.Destroyed {
		return kvLatest{}, r.bad("the version is deleted or destroyed")
	}
	return kvLatest{version: *m.Version, created: created, data: out.Data.Data, r: r}, nil
}

// readTalosAccess is the Talos access read under c's token, for ingestion and the executor alike.
func readTalosAccess(ctx context.Context, c *client, cluster string) (TalosAccess, error) {
	p, err := TalosAccessPath(cluster)
	if err != nil {
		return TalosAccess{}, err
	}
	l, err := readLatest(ctx, c, p)
	if err != nil {
		return TalosAccess{}, err
	}
	if len(l.data) != 1 {
		return TalosAccess{}, l.r.bad("the data is not exactly a talosconfig")
	}
	tc, err := l.stringMember("talosconfig")
	if err != nil {
		return TalosAccess{}, err
	}
	return TalosAccess{v: TalosAccessVersion{Path: p, Version: l.version, CreatedTime: l.created}, s: &tc}, nil
}

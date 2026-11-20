package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

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

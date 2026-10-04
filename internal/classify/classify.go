// Package classify is the dependency monitor's classification procedure (dependency-monitor.md
// §3): one provider answer to one by-name metadata request, judged against the recorded
// dependency by the first matching row of §3's table. It is a pure function of the record and
// the answer's status, Date header and body; the monitor and publication (compilation §6 step 3,
// §11) call the same function. It reads no value: the answers are metadata.
package classify

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Provider is the kind of provider object a dependency names.
type Provider string

const (
	// KV is a KV v2 secret version, asked for at secret/metadata/<path>.
	KV Provider = "kv"
	// Transit is a Transit key version, asked for at transit/keys/<name>.
	Transit Provider = "transit"
)

// Class is a classification's class.
type Class string

const (
	Retained Class = "retained"
	Blocked  Class = "blocked"
	Lost     Class = "lost"
	Unknown  Class = "unknown"
)

// Reason is a classification's reason, as §3's table names it; None for a plain retained.
type Reason string

const (
	None                           Reason = ""
	Malformed                      Reason = "malformed"
	Denied                         Reason = "denied"
	Absent                         Reason = "absent"
	Unavailable                    Reason = "unavailable"
	Unreachable                    Reason = "unreachable"
	Unreadable                     Reason = "unreadable"
	Destroyed                      Reason = "destroyed"
	Pruned                         Reason = "pruned"
	InsufficientEvidence           Reason = "insufficient-evidence"
	IdentityMismatch               Reason = "identity-mismatch"
	DeletionTimeUndecidable        Reason = "deletion-time-undecidable"
	SoftDeleted                    Reason = "soft-deleted"
	DeletionScheduled              Reason = "deletion-scheduled"
	SoftDeleteUnobserved           Reason = "soft-delete-unobserved"
	TrimmedUnverified              Reason = "trimmed-unverified"
	BelowDecryptionFloorUnverified Reason = "below-decryption-floor-unverified"
	Trimmed                        Reason = "trimmed"
	BelowDecryptionFloor           Reason = "below-decryption-floor"
)

// Dependency is the recorded dependency: a provider object (a KV path or a Transit key name), its
// version, and the version's creation time (compilation §9). A zero Created means none is
// recorded yet: publication's own classification, which takes the answer's (compilation §6 step 3)
// and returns it in the Result.
type Dependency struct {
	Provider Provider
	Object   string
	Version  int64
	Created  time.Time
}

// Answer is one provider answer: its status, its Date header as sent, and its body. Unreachable
// says no answer came (no connection, or none within the request timeout); the rest is then
// empty.
type Answer struct {
	Status      int
	Date        string
	Body        []byte
	Unreachable bool
}

// Result is one classification. Created is the version's creation time from the answer, when the
// answer gives the recorded version with the recorded identity; Deletion is a KV version's
// scheduled or past deletion time, when set; Date is the answer's Date, when readable.
type Result struct {
	Class    Class
	Reason   Reason
	Created  time.Time
	Deletion time.Time
	Date     time.Time
}

// errUnreadable is an answer that does not parse as the provider's metadata.
var errUnreadable = errors.New("unreadable")

// Classify applies §3's table to the answer for dependency d.
func Classify(d Dependency, a Answer) Result {
	if d.Version < 1 {
		return Result{Class: Unknown, Reason: Malformed}
	}
	switch {
	case a.Unreachable:
		return Result{Class: Unknown, Reason: Unreachable}
	case a.Status == http.StatusForbidden:
		return Result{Class: Unknown, Reason: Denied}
	case a.Status == http.StatusNotFound:
		return Result{Class: Unknown, Reason: Absent}
	case a.Status == http.StatusServiceUnavailable:
		return Result{Class: Unknown, Reason: Unavailable}
	case a.Status != http.StatusOK:
		return Result{Class: Unknown, Reason: Unreadable}
	}
	var date time.Time
	if t, err := http.ParseTime(a.Date); err == nil {
		date = t.UTC()
	}
	var r Result
	var err error
	switch d.Provider {
	case KV:
		r, err = classifyKV(d, a.Body, date)
	case Transit:
		r, err = classifyTransit(d, a.Body)
	default:
		err = errUnreadable
	}
	if err != nil {
		return Result{Class: Unknown, Reason: Unreadable, Date: date}
	}
	r.Date = date
	return r
}

// kvEntry is one version of a KV v2 metadata answer. Pointers tell a missing field from a zero
// one.
type kvEntry struct {
	CreatedTime  *string `json:"created_time"`
	DeletionTime *string `json:"deletion_time"`
	Destroyed    *bool   `json:"destroyed"`
}

func classifyKV(d Dependency, body []byte, date time.Time) (Result, error) {
	var data struct {
		CurrentVersion *int64                     `json:"current_version"`
		OldestVersion  *int64                     `json:"oldest_version"`
		Versions       map[string]json.RawMessage `json:"versions"`
	}
	if err := decodeData(body, &data); err != nil {
		return Result{}, err
	}
	if data.CurrentVersion == nil || data.OldestVersion == nil || data.Versions == nil {
		return Result{}, errUnreadable
	}
	var entry *kvEntry
	var deletion time.Time
	if raw, ok := data.Versions[strconv.FormatInt(d.Version, 10)]; ok {
		entry = new(kvEntry)
		if err := json.Unmarshal(raw, entry); err != nil || entry.Destroyed == nil || entry.DeletionTime == nil {
			return Result{}, errUnreadable
		}
		if *entry.DeletionTime != "" {
			t, err := time.Parse(time.RFC3339Nano, *entry.DeletionTime)
			if err != nil {
				return Result{}, errUnreadable
			}
			deletion = t.UTC()
		}
	}
	switch {
	case entry != nil && *entry.Destroyed:
		return Result{Class: Lost, Reason: Destroyed}, nil
	case *data.OldestVersion > 0 && d.Version < *data.OldestVersion:
		return Result{Class: Lost, Reason: Pruned}, nil
	case d.Version > *data.CurrentVersion || entry == nil:
		return Result{Class: Unknown, Reason: InsufficientEvidence}, nil
	}
	created, ok := kvCreated(entry.CreatedTime)
	if !ok || !d.Created.IsZero() && !created.Equal(d.Created) {
		return Result{Class: Unknown, Reason: IdentityMismatch}, nil
	}
	r := Result{Created: created, Deletion: deletion}
	switch {
	case deletion.IsZero():
		r.Class, r.Reason = Retained, None
	case date.IsZero() || deletion.Truncate(time.Second).Equal(date):
		r.Class, r.Reason = Unknown, DeletionTimeUndecidable
	case deletion.Before(date):
		r.Class, r.Reason = Blocked, SoftDeleted
	default:
		r.Class, r.Reason = Retained, DeletionScheduled
	}
	return r, nil
}

// kvCreated is a version's created_time; false when missing or not a time.
func kvCreated(s *string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func classifyTransit(d Dependency, body []byte) (Result, error) {
	var data struct {
		Keys                 map[string]json.RawMessage `json:"keys"`
		LatestVersion        *int64                     `json:"latest_version"`
		MinAvailableVersion  *int64                     `json:"min_available_version"`
		MinDecryptionVersion *int64                     `json:"min_decryption_version"`
		SoftDeleted          json.RawMessage            `json:"soft_deleted"`
	}
	if err := decodeData(body, &data); err != nil {
		return Result{}, err
	}
	if data.Keys == nil {
		return Result{}, errUnreadable
	}
	var created time.Time
	raw, listed := data.Keys[strconv.FormatInt(d.Version, 10)]
	if listed {
		var secs int64
		if err := json.Unmarshal(raw, &secs); err != nil {
			return Result{}, errUnreadable
		}
		created = time.Unix(secs, 0).UTC()
	}
	switch {
	case listed && !d.Created.IsZero() && !created.Equal(d.Created):
		return Result{Class: Unknown, Reason: IdentityMismatch}, nil
	case !bytes.Equal(data.SoftDeleted, []byte("false")):
		return Result{Class: Unknown, Reason: SoftDeleteUnobserved}, nil
	case data.MinAvailableVersion == nil || data.MinDecryptionVersion == nil || data.LatestVersion == nil:
		return Result{Class: Unknown, Reason: InsufficientEvidence}, nil
	}
	minAvailable, minDecryption := *data.MinAvailableVersion, *data.MinDecryptionVersion
	belowAvailable := minAvailable > 0 && d.Version < minAvailable
	belowDecryption := d.Version < minDecryption
	switch {
	case !listed && belowAvailable:
		return Result{Class: Unknown, Reason: TrimmedUnverified}, nil
	case !listed && belowDecryption:
		return Result{Class: Unknown, Reason: BelowDecryptionFloorUnverified}, nil
	case !listed:
		return Result{Class: Unknown, Reason: InsufficientEvidence}, nil
	case belowAvailable:
		return Result{Class: Lost, Reason: Trimmed, Created: created}, nil
	case belowDecryption:
		return Result{Class: Blocked, Reason: BelowDecryptionFloor, Created: created}, nil
	}
	return Result{Class: Retained, Reason: None, Created: created}, nil
}

// decodeData decodes the answer's data object into v: one JSON object with a data member, and
// nothing after it.
func decodeData(body []byte, v any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&envelope); err != nil {
		return errUnreadable
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errUnreadable
	}
	if len(envelope.Data) == 0 || envelope.Data[0] != '{' {
		return errUnreadable
	}
	if err := json.Unmarshal(envelope.Data, v); err != nil {
		return errUnreadable
	}
	return nil
}

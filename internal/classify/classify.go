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
	"fmt"
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
	Body        Body
	Unreachable bool
}

// Body is a provider answer's body, as received. It renders as "[provider answer body]" under
// every fmt verb, refuses marshaling, and holds the bytes behind a pointer: a struct holding a
// Body in an unexported field is printed by reflection, which no method can intercept, and then
// shows the pointer, not the bytes (as provider.Token).
// The pointer is to a string, not a slice: fmt's bad-verb output dereferences a pointer to a
// slice or struct (%!s(*[]uint8=&[...])) but prints any other pointer as its address.
type Body struct{ p *string }

const bodyText = "[provider answer body]"

// NewBody holds a copy of b.
func NewBody(b []byte) Body {
	if b == nil {
		return Body{}
	}
	s := string(b)
	return Body{&s}
}

// Bytes is a copy of the body as received; nil when none was held.
func (b Body) Bytes() []byte {
	if b.p == nil {
		return nil
	}
	return []byte(*b.p)
}

func (Body) String() string               { return bodyText }
func (Body) GoString() string             { return bodyText }
func (Body) Format(f fmt.State, _ rune)   { io.WriteString(f, bodyText) }
func (Body) MarshalJSON() ([]byte, error) { return nil, errAnswerMarshal }
func (Body) MarshalText() ([]byte, error) { return nil, errAnswerMarshal }

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

// errAnswerMarshal refuses to marshal an answer: its body is the provider's raw bytes, and an
// error answer may echo what it was sent.
var errAnswerMarshal = errors.New("classify: an answer is not marshaled")

// Format renders an answer's status only, for every verb: never its body.
func (a Answer) Format(f fmt.State, _ rune) {
	if a.Unreachable {
		fmt.Fprint(f, "answer{unreachable}")
		return
	}
	fmt.Fprintf(f, "answer{status %d}", a.Status)
}

// MarshalJSON refuses, as Format hides the body.
func (Answer) MarshalJSON() ([]byte, error) { return nil, errAnswerMarshal }

// MarshalText refuses, as MarshalJSON.
func (Answer) MarshalText() ([]byte, error) { return nil, errAnswerMarshal }

var dateLayouts = []string{
	http.TimeFormat,
	"Monday, 02-Jan-06 15:04:05 GMT",
	time.ANSIC,
	"Mon Jan 02 15:04:05 2006",
}

// ParseDate reads a Date header in one of RFC 9110 §5.6.7's three forms, in UTC. The text must be
// exactly what its form writes for the time it names: time.Parse also takes a fractional second
// (with a dot or a comma, and drops digits past the ninth), which §3's same-second rule and the
// encryption bracket (compilation §11) cannot use. The RFC 850 form ends in a literal GMT
// (time.RFC850's zone element takes any abbreviation), and the asctime form's day is either
// space-padded (time.ANSIC) or two digits.
func ParseDate(s string) (time.Time, bool) {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil && t.Format(layout) == s {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// errUnreadable is an answer that does not parse as the provider's metadata.
var errUnreadable = errors.New("unreadable")

// Classify applies §3's table to the answer for dependency d. Every result carries the answer's
// Date when readable, the status rows' included: the recorded status keeps it (§5.1).
func Classify(d Dependency, a Answer) Result {
	date, _ := ParseDate(a.Date)
	if d.Version < 1 {
		return Result{Class: Unknown, Reason: Malformed, Date: date}
	}
	if r := statusReason(a); r != None {
		return Result{Class: Unknown, Reason: r, Date: date}
	}
	var r Result
	var err error
	switch d.Provider {
	case KV:
		r, err = classifyKV(d, a.Body.Bytes(), date)
	case Transit:
		r, err = classifyTransit(d, a.Body.Bytes())
	default:
		err = errUnreadable
	}
	if err != nil {
		return Result{Class: Unknown, Reason: Unreadable, Date: date}
	}
	r.Date = date
	return r
}

// AnswerReason is the reason the answer fails for every version of the object: one of §3's status
// rows, or a body that does not parse as the provider's metadata. It is None when each version's
// own entry decides; an entry for one version never makes the answer fail for another.
func AnswerReason(p Provider, a Answer) Reason {
	if r := statusReason(a); r != None {
		return r
	}
	var err error
	switch p {
	case KV:
		_, err = decodeKV(a.Body.Bytes())
	case Transit:
		_, err = decodeTransit(a.Body.Bytes())
	default:
		err = errUnreadable
	}
	if err != nil {
		return Unreadable
	}
	return None
}

// statusReason is §3's row for an answer that is not a 200, or None.
func statusReason(a Answer) Reason {
	switch {
	case a.Unreachable:
		return Unreachable
	case a.Status == http.StatusForbidden:
		return Denied
	case a.Status == http.StatusNotFound:
		return Absent
	case a.Status == http.StatusServiceUnavailable:
		return Unavailable
	case a.Status != http.StatusOK:
		return Unreadable
	}
	return None
}

// kvEntry is one version of a KV v2 metadata answer. Pointers tell a missing field from a zero
// one.
type kvEntry struct {
	CreatedTime  *string `json:"created_time"`
	DeletionTime *string `json:"deletion_time"`
	Destroyed    *bool   `json:"destroyed"`
}

// kvData is a KV v2 metadata answer's data, its required fields present.
type kvData struct {
	CurrentVersion *int64                     `json:"current_version"`
	OldestVersion  *int64                     `json:"oldest_version"`
	Versions       map[string]json.RawMessage `json:"versions"`
}

func decodeKV(body []byte) (kvData, error) {
	var data kvData
	if err := decodeData(body, &data); err != nil {
		return kvData{}, err
	}
	if data.CurrentVersion == nil || data.OldestVersion == nil || data.Versions == nil {
		return kvData{}, errUnreadable
	}
	return data, nil
}

func classifyKV(d Dependency, body []byte, date time.Time) (Result, error) {
	data, err := decodeKV(body)
	if err != nil {
		return Result{}, err
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
	case *entry.DeletionTime == "":
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

// transitData is a Transit key answer's data, its keys map present.
type transitData struct {
	Keys                 map[string]json.RawMessage `json:"keys"`
	LatestVersion        *int64                     `json:"latest_version"`
	MinAvailableVersion  *int64                     `json:"min_available_version"`
	MinDecryptionVersion *int64                     `json:"min_decryption_version"`
	SoftDeleted          json.RawMessage            `json:"soft_deleted"`
}

func decodeTransit(body []byte) (transitData, error) {
	var data transitData
	if err := decodeData(body, &data); err != nil {
		return transitData{}, err
	}
	if data.Keys == nil {
		return transitData{}, errUnreadable
	}
	return data, nil
}

func classifyTransit(d Dependency, body []byte) (Result, error) {
	data, err := decodeTransit(body)
	if err != nil {
		return Result{}, err
	}
	var created time.Time
	raw, listed := data.Keys[strconv.FormatInt(d.Version, 10)]
	if listed {
		var secs *int64
		if err := json.Unmarshal(raw, &secs); err != nil || secs == nil {
			return Result{}, errUnreadable
		}
		created = time.Unix(*secs, 0).UTC()
	}
	switch {
	case listed && !d.Created.IsZero() && !created.Equal(d.Created):
		return Result{Class: Unknown, Reason: IdentityMismatch}, nil
	case !bytes.Equal(data.SoftDeleted, []byte("false")):
		return Result{Class: Unknown, Reason: SoftDeleteUnobserved}, nil
	case data.MinAvailableVersion == nil || data.MinDecryptionVersion == nil || data.LatestVersion == nil:
		return Result{Class: Unknown, Reason: InsufficientEvidence}, nil
	case d.Version > *data.LatestVersion:
		// Contradictory metadata: RC never classified a version the key says it has not reached.
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

package classify

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// The provider's Date and the times around it, in whole seconds as the header gives them.
var (
	date     = time.Date(2026, 9, 24, 19, 51, 17, 0, time.UTC)
	dateText = date.Format(http.TimeFormat)
	created  = time.Date(2026, 9, 24, 19, 51, 3, 464091849, time.UTC)
	keyTime  = time.Unix(1790279466, 0).UTC()
)

// kv is a KV v2 metadata answer's data: version 1 live, current 1, oldest 0, as transcript 001.
func kv() map[string]any {
	return map[string]any{
		"current_version": 1, "oldest_version": 0, "custom_metadata": nil,
		"versions": map[string]any{
			"1": map[string]any{"created_time": created.Format(time.RFC3339Nano), "deletion_time": "", "destroyed": false},
		},
	}
}

// version1 is kv's version 1 entry, to edit.
func version1(d map[string]any) map[string]any {
	return d["versions"].(map[string]any)["1"].(map[string]any)
}

// transit is a Transit key answer's data: version 1 at keyTime, floors as transcript 007.
func transit() map[string]any {
	return map[string]any{
		"keys": map[string]any{"1": keyTime.Unix()}, "latest_version": 1,
		"min_available_version": 0, "min_decryption_version": 1, "soft_deleted": false,
	}
}

func answer(t *testing.T, data map[string]any) Answer {
	t.Helper()
	body, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		t.Fatal(err)
	}
	return Answer{Status: http.StatusOK, Date: dateText, Body: body}
}

func kvDep(version int64) Dependency {
	return Dependency{Provider: KV, Object: "gen/a/b/c", Version: version, Created: created}
}

func transitDep(version int64) Dependency {
	return Dependency{Provider: Transit, Object: "bw-artifact", Version: version, Created: keyTime}
}

func TestClassify(t *testing.T) {
	edit := func(base func() map[string]any, f func(map[string]any)) map[string]any {
		d := base()
		f(d)
		return d
	}
	cases := []struct {
		name   string
		dep    Dependency
		answer func(t *testing.T) Answer
		class  Class
		reason Reason
	}{
		// Rows for any provider, in table order.
		{"version zero", kvDep(0), func(t *testing.T) Answer { return answer(t, kv()) }, Unknown, Malformed},
		{"version negative", transitDep(-1), func(t *testing.T) Answer { return answer(t, transit()) }, Unknown, Malformed},
		{"malformed before denied", kvDep(0), func(*testing.T) Answer { return Answer{Status: 403} }, Unknown, Malformed},
		{"403", kvDep(1), func(*testing.T) Answer { return Answer{Status: 403} }, Unknown, Denied},
		{"404", transitDep(1), func(*testing.T) Answer { return Answer{Status: 404} }, Unknown, Absent},
		{"503", kvDep(1), func(*testing.T) Answer { return Answer{Status: 503} }, Unknown, Unavailable},
		{"no answer", kvDep(1), func(*testing.T) Answer { return Answer{Unreachable: true} }, Unknown, Unreachable},
		{"429", kvDep(1), func(*testing.T) Answer { return Answer{Status: 429} }, Unknown, Unreadable},
		{"307", kvDep(1), func(*testing.T) Answer { return Answer{Status: 307} }, Unknown, Unreadable},
		{"204", kvDep(1), func(*testing.T) Answer { return Answer{Status: 204} }, Unknown, Unreadable},
		{"500", transitDep(1), func(*testing.T) Answer { return Answer{Status: 500} }, Unknown, Unreadable},
		{"not JSON", kvDep(1), func(*testing.T) Answer { return Answer{Status: 200, Date: dateText, Body: []byte("{")} }, Unknown, Unreadable},
		{"trailing data", kvDep(1), func(t *testing.T) Answer {
			a := answer(t, kv())
			a.Body = append(a.Body, []byte("{}")...)
			return a
		}, Unknown, Unreadable},
		{"no data", transitDep(1), func(*testing.T) Answer { return Answer{Status: 200, Date: dateText, Body: []byte(`{}`)} }, Unknown, Unreadable},

		// KV v2.
		{"kv retained", kvDep(1), func(t *testing.T) Answer { return answer(t, kv()) }, Retained, None},
		{"kv destroyed", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { version1(d)["destroyed"] = true }))
		}, Lost, Destroyed},
		{"kv destroyed before pruned", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				version1(d)["destroyed"] = true
				d["oldest_version"] = 2
				d["current_version"] = 2
			}))
		}, Lost, Destroyed},
		{"kv pruned", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				d["oldest_version"], d["current_version"] = 2, 2
				d["versions"] = map[string]any{}
			}))
		}, Lost, Pruned},
		{"kv above current", kvDep(2), func(t *testing.T) Answer { return answer(t, kv()) }, Unknown, InsufficientEvidence},
		{"kv above current, listed", kvDep(2), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { d["versions"].(map[string]any)["2"] = version1(d) }))
		}, Unknown, InsufficientEvidence},
		{"kv absent from the answer", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { d["versions"] = map[string]any{} }))
		}, Unknown, InsufficientEvidence},
		{"kv created_time missing", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { delete(version1(d), "created_time") }))
		}, Unknown, IdentityMismatch},
		{"kv created_time not a time", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { version1(d)["created_time"] = "yesterday" }))
		}, Unknown, IdentityMismatch},
		{"kv created_time differs", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				version1(d)["created_time"] = created.Add(time.Nanosecond).Format(time.RFC3339Nano)
			}))
		}, Unknown, IdentityMismatch},
		{"kv identity before deletion", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				version1(d)["created_time"] = created.Add(time.Second).Format(time.RFC3339Nano)
				version1(d)["deletion_time"] = date.Add(-time.Hour).Format(time.RFC3339Nano)
			}))
		}, Unknown, IdentityMismatch},
		{"kv deletion in Date's second", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				version1(d)["deletion_time"] = date.Add(999 * time.Millisecond).Format(time.RFC3339Nano)
			}))
		}, Unknown, DeletionTimeUndecidable},
		{"kv deletion without Date", kvDep(1), func(t *testing.T) Answer {
			a := answer(t, edit(kv, func(d map[string]any) {
				version1(d)["deletion_time"] = date.Add(-time.Hour).Format(time.RFC3339Nano)
			}))
			a.Date = ""
			return a
		}, Unknown, DeletionTimeUndecidable},
		{"kv deletion with an unreadable Date", kvDep(1), func(t *testing.T) Answer {
			a := answer(t, edit(kv, func(d map[string]any) {
				version1(d)["deletion_time"] = date.Add(-time.Hour).Format(time.RFC3339Nano)
			}))
			a.Date = "soon"
			return a
		}, Unknown, DeletionTimeUndecidable},
		{"kv soft-deleted", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				version1(d)["deletion_time"] = date.Add(-time.Nanosecond).Format(time.RFC3339Nano)
			}))
		}, Blocked, SoftDeleted},
		{"kv deletion scheduled", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				version1(d)["deletion_time"] = date.Add(time.Second).Format(time.RFC3339Nano)
			}))
		}, Retained, DeletionScheduled},
		{"kv no Date needed without a deletion", kvDep(1), func(t *testing.T) Answer {
			a := answer(t, kv())
			a.Date = ""
			return a
		}, Retained, None},
		{"kv deletion_time not a time", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { version1(d)["deletion_time"] = "later" }))
		}, Unknown, Unreadable},
		{"kv destroyed missing", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { delete(version1(d), "destroyed") }))
		}, Unknown, Unreadable},
		{"kv deletion_time missing", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { delete(version1(d), "deletion_time") }))
		}, Unknown, Unreadable},
		{"kv current_version missing", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { delete(d, "current_version") }))
		}, Unknown, Unreadable},
		{"kv oldest_version missing", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { delete(d, "oldest_version") }))
		}, Unknown, Unreadable},
		{"kv versions missing", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) { delete(d, "versions") }))
		}, Unknown, Unreadable},

		// Transit.
		{"transit retained", transitDep(1), func(t *testing.T) Answer { return answer(t, transit()) }, Retained, None},
		{"transit identity mismatch", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { d["keys"] = map[string]any{"1": keyTime.Unix() + 1} }))
		}, Unknown, IdentityMismatch},
		{"transit identity before soft delete", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) {
				d["keys"] = map[string]any{"1": keyTime.Unix() + 1}
				d["soft_deleted"] = true
			}))
		}, Unknown, IdentityMismatch},
		{"transit soft deleted", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { d["soft_deleted"] = true }))
		}, Unknown, SoftDeleteUnobserved},
		{"transit soft_deleted missing", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { delete(d, "soft_deleted") }))
		}, Unknown, SoftDeleteUnobserved},
		{"transit soft_deleted not a boolean", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { d["soft_deleted"] = "false" }))
		}, Unknown, SoftDeleteUnobserved},
		{"transit latest missing", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { delete(d, "latest_version") }))
		}, Unknown, InsufficientEvidence},
		{"transit min_available missing", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { delete(d, "min_available_version") }))
		}, Unknown, InsufficientEvidence},
		{"transit min_decryption missing", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { delete(d, "min_decryption_version") }))
		}, Unknown, InsufficientEvidence},
		{"transit absent, below min_available", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) {
				d["keys"] = map[string]any{"2": keyTime.Unix()}
				d["latest_version"], d["min_available_version"], d["min_decryption_version"] = 2, 2, 2
			}))
		}, Unknown, TrimmedUnverified},
		{"transit absent, below min_decryption", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) {
				d["keys"] = map[string]any{"2": keyTime.Unix()}
				d["latest_version"], d["min_decryption_version"] = 2, 2
			}))
		}, Unknown, BelowDecryptionFloorUnverified},
		{"transit absent", transitDep(3), func(t *testing.T) Answer { return answer(t, transit()) }, Unknown, InsufficientEvidence},
		{"transit listed below min_available", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) {
				d["keys"] = map[string]any{"1": keyTime.Unix(), "2": keyTime.Unix() + 60}
				d["latest_version"], d["min_available_version"], d["min_decryption_version"] = 2, 2, 2
			}))
		}, Lost, Trimmed},
		{"transit listed below min_decryption", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) {
				d["keys"] = map[string]any{"1": keyTime.Unix(), "2": keyTime.Unix() + 60}
				d["latest_version"], d["min_decryption_version"] = 2, 2
			}))
		}, Blocked, BelowDecryptionFloor},
		{"transit other versions do not matter", transitDep(2), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) {
				d["keys"] = map[string]any{"1": "unreadable", "2": keyTime.Unix()}
				d["latest_version"] = 2
			}))
		}, Retained, None},
		{"kv other versions do not matter", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				d["versions"].(map[string]any)["2"] = map[string]any{"created_time": 7}
				d["current_version"] = 2
			}))
		}, Retained, None},
		{"kv a malformed version key is not ours", kvDep(1), func(t *testing.T) Answer {
			return answer(t, edit(kv, func(d map[string]any) {
				d["versions"] = map[string]any{"01": version1(d)}
			}))
		}, Unknown, InsufficientEvidence},
		{"transit creation time not a number", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { d["keys"] = map[string]any{"1": "then"} }))
		}, Unknown, Unreadable},
		{"transit keys missing", transitDep(1), func(t *testing.T) Answer {
			return answer(t, edit(transit, func(d map[string]any) { delete(d, "keys") }))
		}, Unknown, Unreadable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(c.dep, c.answer(t))
			if got.Class != c.class || got.Reason != c.reason {
				t.Fatalf("Classify = %s/%q, want %s/%q", got.Class, got.Reason, c.class, c.reason)
			}
		})
	}
}

// A dependency recorded without a creation time (publication's own first classification,
// compilation §6 step 3) takes the answer's, and the result carries it for §9.
func TestClassifyAdoptsCreatedTime(t *testing.T) {
	d := kvDep(1)
	d.Created = time.Time{}
	got := Classify(d, answer(t, kv()))
	if got.Class != Retained || !got.Created.Equal(created) {
		t.Fatalf("Classify = %s, created %v; want retained, created %v", got.Class, got.Created, created)
	}
	tr := transitDep(1)
	tr.Created = time.Time{}
	got = Classify(tr, answer(t, transit()))
	if got.Class != Retained || !got.Created.Equal(keyTime) {
		t.Fatalf("Classify = %s, created %v; want retained, created %v", got.Class, got.Created, keyTime)
	}
}

// The result carries the provider's Date and, for a scheduled deletion, its time.
func TestClassifyTimes(t *testing.T) {
	when := date.Add(time.Hour)
	got := Classify(kvDep(1), answer(t, func() map[string]any {
		d := kv()
		version1(d)["deletion_time"] = when.Format(time.RFC3339Nano)
		return d
	}()))
	if got.Reason != DeletionScheduled || !got.Deletion.Equal(when) || !got.Date.Equal(date) {
		t.Fatalf("Classify = %q, deletion %v, date %v; want deletion-scheduled at %v, date %v", got.Reason, got.Deletion, got.Date, when, date)
	}
	if got := Classify(kvDep(1), answer(t, kv())); !got.Deletion.IsZero() || !got.Date.Equal(date) {
		t.Fatalf("a live version: deletion %v, date %v", got.Deletion, got.Date)
	}
}

// A provider kind the procedure does not know is unknown, not retained.
func TestClassifyUnknownProvider(t *testing.T) {
	d := kvDep(1)
	d.Provider = "age"
	if got := Classify(d, answer(t, kv())); got.Class != Unknown || got.Reason != Unreadable {
		t.Fatalf("Classify = %s/%q, want unknown/unreadable", got.Class, got.Reason)
	}
}

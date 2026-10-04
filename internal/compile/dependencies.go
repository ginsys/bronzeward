package compile

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/provider"
)

// MetadataReader is the metadata identity (dependency-monitor.md §4): one answer by name, as the
// provider gave it.
type MetadataReader interface {
	KV(ctx context.Context, p provider.GenerationPath) (classify.Answer, error)
	Transit(ctx context.Context, key string) (classify.Answer, error)
}

// PinnedReader is the compiler identity's read of one pinned KV version (compilation.md §1).
type PinnedReader interface {
	ReadGeneration(ctx context.Context, p provider.GenerationPath, version int64) (provider.Value, time.Time, error)
}

// ArtifactEncrypter is the compiler identity's encryption under the artifact key.
type ArtifactEncrypter interface {
	EncryptArtifact(ctx context.Context, plaintext []byte) (provider.Ciphertext, error)
}

// Pin is one declared reference at its pinned version (compilation.md §6 step 2): the reference
// name, the generation path it names and the version. Recorded is the version's recorded
// created_time, or zero when none is recorded yet; publication's own check takes the answer's.
type Pin struct {
	Reference string
	Path      provider.GenerationPath
	Version   int64
	Recorded  time.Time
}

// Pinned is a pin step 3 classified retained, with the created_time its metadata answer gave: the
// identity step 4's read must repeat and §9 records.
type Pinned struct {
	Pin
	Created time.Time
}

// The refusals of a changed identity, beside the classification's own reasons.
const (
	// ReasonCreatedChanged: the value read gave a created_time other than step 3's (§6 step 4).
	ReasonCreatedChanged = "created-time-changed"
	// ReasonNoDate: the artifact key's first read gave no readable Date (§11).
	ReasonNoDate = "no-date"
	// ReasonNotInFirstRead: the first read did not give the used key version's creation time (§11).
	ReasonNotInFirstRead = "not-in-first-read"
	// ReasonCreatedNotBeforeDate: the used key version was not created in a second before the
	// first read's Date (§11).
	ReasonCreatedNotBeforeDate = "created-not-before-date"
)

// DependencyError refuses publication for one dependency: a pinned secret version (Reference set)
// or the artifact key (Reference empty), by its provider object and version. Class and Reason are
// the classification's when it was not retained; Class is empty for a changed identity, Reason
// then one of the Reason constants. It names references, paths and key names only, never a value.
type DependencyError struct {
	Reference string
	Object    string
	Version   int64
	Class     classify.Class
	Reason    string
}

func (e *DependencyError) Error() string {
	what := fmt.Sprintf("the artifact key %s version %d", e.Object, e.Version)
	if e.Reference != "" {
		what = fmt.Sprintf("reference %q (%s version %d)", e.Reference, e.Object, e.Version)
	}
	if e.Class != "" {
		return fmt.Sprintf("compile: %s classified %s (%s)", what, e.Class, e.Reason)
	}
	return fmt.Sprintf("compile: %s: %s", what, e.Reason)
}

// CheckPinned performs compilation.md §6 step 3: it classifies every pin afresh by the dependency
// monitor's procedure, one metadata request each, and refuses the first that is not retained.
// A pin's recorded created_time, when set, must be the answer's.
func CheckPinned(ctx context.Context, m MetadataReader, pins []Pin) ([]Pinned, error) {
	out := make([]Pinned, 0, len(pins))
	for _, p := range pins {
		a, err := m.KV(ctx, p.Path)
		if err != nil {
			return nil, fmt.Errorf("compile: classifying reference %q (%s version %d): %w", p.Reference, p.Path, p.Version, err)
		}
		r := classify.Classify(classify.Dependency{Provider: classify.KV, Object: p.Path.String(), Version: p.Version, Created: p.Recorded}, a)
		if r.Class != classify.Retained {
			return nil, &DependencyError{Reference: p.Reference, Object: p.Path.String(), Version: p.Version, Class: r.Class, Reason: string(r.Reason)}
		}
		out = append(out, Pinned{Pin: p, Created: r.Created})
	}
	return out, nil
}

// ReadPinned performs the reads of compilation.md §6 step 4 with the compiler identity, one per
// pin, in order, and refuses a value whose created_time is not the one step 3 took.
func ReadPinned(ctx context.Context, r PinnedReader, pins []Pinned) ([]provider.Value, error) {
	out := make([]provider.Value, 0, len(pins))
	for _, p := range pins {
		v, created, err := r.ReadGeneration(ctx, p.Path, p.Version)
		if err != nil {
			return nil, fmt.Errorf("compile: reading reference %q (%s version %d): %w", p.Reference, p.Path, p.Version, err)
		}
		if !created.Equal(p.Created) {
			return nil, &DependencyError{Reference: p.Reference, Object: p.Path.String(), Version: p.Version, Reason: ReasonCreatedChanged}
		}
		out = append(out, v)
	}
	return out, nil
}

// Encrypted is one artifact's ciphertext and the artifact key version it used, with that
// version's creation time: the encryption dependency §9 records.
type Encrypted struct {
	Ciphertext provider.Ciphertext
	Key        string
	Version    int64
	Created    time.Time
}

// EncryptArtifacts performs compilation.md §11's encryption of every artifact under key: it reads
// the key's metadata first, encrypts each artifact in order, then reads the metadata again and
// classifies every version used, with the first read's creation time as the recorded identity.
// It refuses a first read that is not a readable answer with a readable Date, before encrypting;
// a version the first read gave no creation time for, or one not created in a second before the
// first read's Date; and any used version that does not then classify retained.
func EncryptArtifacts(ctx context.Context, m MetadataReader, e ArtifactEncrypter, key string, artifacts []Materialized) ([]Encrypted, error) {
	first, err := m.Transit(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("compile: reading the artifact key %s: %w", key, err)
	}
	// The status and the body's shape do not depend on the version asked.
	if r := classify.Classify(classify.Dependency{Provider: classify.Transit, Object: key, Version: 1}, first); r.Class == classify.Unknown && answerFailed(r.Reason) {
		return nil, &DependencyError{Object: key, Version: 1, Class: r.Class, Reason: string(r.Reason)}
	}
	date, err := http.ParseTime(first.Date)
	if err != nil {
		return nil, &DependencyError{Object: key, Reason: ReasonNoDate}
	}
	out := make([]Encrypted, len(artifacts))
	created := map[int64]time.Time{}
	for i, a := range artifacts {
		ct, err := e.EncryptArtifact(ctx, a.bytes())
		if err != nil {
			return nil, fmt.Errorf("compile: encrypting artifact %d under %s: %w", i, key, err)
		}
		n, err := ct.KeyVersion()
		if err != nil {
			return nil, fmt.Errorf("compile: encrypting artifact %d under %s: %w", i, key, err)
		}
		v := int64(n)
		if _, seen := created[v]; !seen {
			c := classify.Classify(classify.Dependency{Provider: classify.Transit, Object: key, Version: v}, first).Created
			switch {
			case c.IsZero():
				return nil, &DependencyError{Object: key, Version: v, Reason: ReasonNotInFirstRead}
			case !c.Before(date.Truncate(time.Second)):
				return nil, &DependencyError{Object: key, Version: v, Reason: ReasonCreatedNotBeforeDate}
			}
			created[v] = c
		}
		out[i] = Encrypted{Ciphertext: ct, Key: key, Version: v, Created: created[v]}
	}
	if len(artifacts) == 0 {
		return out, nil
	}
	second, err := m.Transit(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("compile: reading the artifact key %s: %w", key, err)
	}
	versions := make([]int64, 0, len(created))
	for v := range created {
		versions = append(versions, v)
	}
	slices.Sort(versions)
	for _, v := range versions {
		r := classify.Classify(classify.Dependency{Provider: classify.Transit, Object: key, Version: v, Created: created[v]}, second)
		if r.Class != classify.Retained {
			return nil, &DependencyError{Object: key, Version: v, Class: r.Class, Reason: string(r.Reason)}
		}
	}
	return out, nil
}

// answerFailed is a reason that comes from the answer as a whole, not from one version's entry.
func answerFailed(r classify.Reason) bool {
	switch r {
	case classify.Unreachable, classify.Denied, classify.Absent, classify.Unavailable, classify.Unreadable:
		return true
	}
	return false
}

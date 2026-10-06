package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/provider"
)

// pinned is what compilation.md §6 steps 2-4 give a publication: each composed source's pinned
// values by reference name, each pinned version's created_time as step 3 classified it, and each
// classification as the status T3 seeds (dependency monitor §5.2).
type pinned struct {
	values   map[string]map[string]provider.Value // source revision -> reference -> value
	created  map[pinKey]time.Time
	statuses []unitStatus
}

// readPinned performs compilation.md §6 steps 2-4 over s: the release's pins, each KV version
// once (the first reference naming it, by source revision then name, names it in a refusal),
// with its recorded identity (ruling R31); every pin classified, then every pin read. The
// database time read just before the classifications is each status's began (ruling R32): no
// later than its request began. A refusal follows ruling R33.
func (a *API) readPinned(ctx context.Context, j publishJob, s snapshot, m compile.MetadataReader, r compile.PinnedReader) (pinned, *refusal, error) {
	revisions := slices.Sorted(maps.Keys(s.sources))
	var pins []compile.Pin
	index := map[pinKey]int{}
	for _, rev := range revisions {
		src := s.sources[rev]
		for _, name := range slices.Sorted(maps.Keys(src.declarations.References)) {
			k := pinKey{src.generations[name], src.declarations.References[name].Version}
			if _, seen := index[k]; seen {
				continue
			}
			path, err := provider.ParseGenerationPath(k.path)
			if err != nil {
				return pinned{}, nil, fmt.Errorf("source %s reference %q: %w", rev, name, err)
			}
			index[k] = len(pins)
			pins = append(pins, compile.Pin{Reference: name, Path: path, Version: k.version, Recorded: s.recorded[k]})
		}
	}
	out := pinned{values: map[string]map[string]provider.Value{}, created: map[pinKey]time.Time{}}
	if len(pins) == 0 {
		return out, nil, nil
	}
	var began time.Time
	if err := a.db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&began); err != nil {
		return pinned{}, nil, err
	}
	// One pin at a time, so a failure names its dependency; all classified before any is read.
	checked := make([]compile.Pinned, len(pins))
	for i, p := range pins {
		c, err := compile.CheckPinned(ctx, m, pins[i:i+1])
		if ref, err := a.dependencyRefusal(j, p.Reference, "kv", p.Path.String(), p.Version, err); ref != nil || err != nil {
			return pinned{}, ref, err
		}
		checked[i] = c[0]
	}
	values := make([]provider.Value, len(pins))
	for i, p := range checked {
		v, err := compile.ReadPinned(ctx, r, checked[i:i+1])
		if ref, err := a.dependencyRefusal(j, p.Reference, "kv", p.Path.String(), p.Version, err); ref != nil || err != nil {
			return pinned{}, ref, err
		}
		values[i] = v[0]
	}
	for _, p := range checked {
		k := pinKey{p.Path.String(), p.Version}
		out.created[k] = p.Status.Created
		out.statuses = append(out.statuses, unitStatus{provider: classify.KV, object: k.path, version: k.version, result: p.Status, began: began})
	}
	for _, rev := range revisions {
		src := s.sources[rev]
		for name, ref := range src.declarations.References {
			if out.values[rev] == nil {
				out.values[rev] = map[string]provider.Value{}
			}
			out.values[rev][name] = values[index[pinKey{src.generations[name], ref.Version}]]
		}
	}
	return out, nil, nil
}

// dependencyRefusal is ruling R33 for one dependency's failure (reference "" for the artifact
// key): not retained, or an identity that changed, is 422 naming it; a classification the
// provider's answer leaves undecided, a value read or encryption the provider refuses or answers
// unintelligibly, or a provider unreachable, is 503; anything else is an error. A refusal names
// the dependency, never a value; a provider's error behind a 503 that is not a classification is
// logged.
func (a *API) dependencyRefusal(j publishJob, reference, prov, object string, version int64, err error) (*refusal, error) {
	if err == nil {
		return nil, nil
	}
	dep := map[string]any{"provider": prov, "object": object, "version": version}
	if reference != "" {
		dep["reference"] = reference
	}
	var d *compile.DependencyError
	if errors.As(err, &d) {
		object, version = d.Object, d.Version // the artifact key's version is known only here
		dep["object"], dep["version"] = object, version
	}
	switch {
	case errors.As(err, &d) && d.Class == classify.Unknown && d.Reason != string(classify.IdentityMismatch):
		dep["class"], dep["reason"] = string(d.Class), d.Reason
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"Publication refused: the provider's answer leaves a dependency undecided; nothing was committed.").with("dependency", dep), nil
	case errors.As(err, &d):
		if d.Class != "" {
			dep["class"] = string(d.Class)
		}
		dep["reason"] = d.Reason
		return refuse(http.StatusUnprocessableEntity, "validation-failed",
			fmt.Sprintf("Publication refused: the %s dependency %s version %d cannot be used.", prov, object, version)).
			with("dependency", dep), nil
	case errors.Is(err, provider.ErrUnavailable):
		a.o.logf("%s: publication: %v", j.op, err)
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"Publication refused: the provider could not be reached; nothing was committed.").with("dependency", dep), nil
	case errors.Is(err, provider.ErrAbsent), errors.Is(err, provider.ErrDenied), errors.Is(err, provider.ErrProtocol),
		errors.Is(err, provider.ErrStatus):
		// A read or encryption refused or not understood after the classification: unknown.
		a.o.logf("%s: publication: %v", j.op, err)
		dep["class"] = string(classify.Unknown)
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"Publication refused: the provider's answer leaves a dependency undecided; nothing was committed.").with("dependency", dep), nil
	}
	return nil, err
}

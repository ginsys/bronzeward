package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"slices"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/ingest"
)

// publishClients are a publication's provider identities (compilation.md §1): the metadata
// identity's classifications, and the compiler identity's pinned reads and artifact encryption.
type publishClients struct {
	meta      compile.MetadataReader
	reader    compile.PinnedReader
	encrypter compile.ArtifactEncrypter
}

// buildRelease performs compilation.md §6 and §11 for j up to the hand-off: the snapshot, the
// pins classified and read, each covered machine compiled in its platform mode, the renderer
// record (§10.2), the provenance, effective and reproduction records (§8.2, §9), and every
// artifact encrypted under one artifact key. It holds no transaction: T3 re-checks what it read.
// A refusal is the publication's failure (ruling R33); an error is the server's.
func (a *API) buildRelease(ctx context.Context, j publishJob, c publishClients) (releaseUnit, *refusal, error) {
	s, ref, err := a.readSnapshot(ctx, j)
	if err != nil || ref != nil {
		return releaseUnit{}, ref, err
	}
	if err := a.step("publish-snapshot"); err != nil {
		return releaseUnit{}, nil, err
	}
	pins, ref, err := a.readPinned(ctx, j, s, c.meta, c.reader)
	if err != nil || ref != nil {
		return releaseUnit{}, ref, err
	}
	if err := a.step("publish-pins"); err != nil { // the resolved values held
		return releaseUnit{}, nil, err
	}
	version, checksum, err := compile.Machinery()
	if err != nil {
		return releaseUnit{}, nil, err
	}
	if ref, err := compileRefusal("", compile.CheckContract(version, s.contract)); ref != nil || err != nil {
		return releaseUnit{}, ref, err
	}
	sources := map[string]compile.Source{}
	for rev, st := range s.sources {
		text, err := ingest.Stored(st.document, st.declarations)
		if err != nil {
			return releaseUnit{}, nil, fmt.Errorf("source %s: %w", rev, err)
		}
		sum := sha256.Sum256([]byte(st.document))
		sources[rev] = compile.Source{Revision: rev, Digest: hex.EncodeToString(sum[:]), Text: text, Values: pins.values[rev]}
	}

	u := releaseUnit{unchanged: s.unchanged, statuses: pins.statuses}
	var kubernetes string
	artifacts := make([]compile.Materialized, 0, len(s.machines))
	for _, m := range s.machines {
		mode, err := compile.ParseMode(m.mode)
		if err != nil {
			return releaseUnit{}, nil, fmt.Errorf("machine %s: %w", m.machine, err)
		}
		in := compile.Input{Base: sources[m.importBase], Mode: mode}
		for _, frv := range m.fragments {
			in.Fragments = append(in.Fragments, sources[frv])
		}
		compiled, err := compile.Compile(in)
		if ref, err := compileRefusal(m.machine, err); ref != nil || err != nil {
			return releaseUnit{}, ref, err
		}
		k, err := compiled.KubernetesVersion(version)
		if ref, err := compileRefusal(m.machine, err); ref != nil || err != nil {
			return releaseUnit{}, ref, err
		}
		if kubernetes != "" && k != kubernetes {
			return releaseUnit{}, refuse(http.StatusUnprocessableEntity, "validation-failed",
				"Publication refused: the covered machines' configurations name different Kubernetes versions.").
				with("machine", m.machine).with("rule", string(compile.RuleKubernetes)), nil
		}
		kubernetes = k
		um, err := machineRecords(m, compiled, s, pins)
		if err != nil {
			return releaseUnit{}, nil, fmt.Errorf("machine %s: %w", m.machine, err)
		}
		u.machines = append(u.machines, um)
		artifacts = append(artifacts, compiled.Materialized())
	}
	if err := a.step("publish-compile"); err != nil { // every configuration held in plaintext
		return releaseUnit{}, nil, err
	}
	u.renderer = rendererBody{Contract: s.contract, MachineryVersion: version, MachineryChecksum: checksum, KubernetesVersion: kubernetes}

	var began time.Time
	if err := a.db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&began); err != nil {
		return releaseUnit{}, nil, err
	}
	encrypted, err := compile.EncryptArtifacts(ctx, c.meta, c.encrypter, artifacts)
	if ref, err := a.dependencyRefusal(j, "", "transit", c.encrypter.ArtifactKey(), 0, err); ref != nil || err != nil {
		return releaseUnit{}, ref, err
	}
	seen := map[int64]bool{}
	for i, e := range encrypted {
		u.machines[i].ciphertext = e.Ciphertext
		u.machines[i].encryption = unitDependency{object: e.Key, version: e.Version, created: e.Status.Created}
		if !seen[e.Version] {
			seen[e.Version] = true
			u.statuses = append(u.statuses, unitStatus{provider: classify.Transit, object: e.Key, version: e.Version, result: e.Status, began: began})
		}
	}
	// The ciphertexts, the plaintexts still held: kept reachable past the point, since their last
	// use was the encryption and a collection could free them before a stop there.
	err = a.step("publish-encrypt")
	runtime.KeepAlive(artifacts)
	if err != nil {
		return releaseUnit{}, nil, err
	}
	return u, nil, nil
}

// machineRecords is m's artifact without its ciphertext: the configuration digest, the redacted
// configuration (nil when it cannot be redacted) and the §8.2 and §9 records, each dependency
// object the generation its source declares and each identity the one step 3 classified.
func machineRecords(m snapshotMachine, c compile.Compiled, s snapshot, pins pinned) (unitMachine, error) {
	um := unitMachine{machine: m.machine, importBase: m.importBase, assignment: m.assignment, mode: m.mode, provenance: c.Provenance()}
	var err error
	if um.configuration, err = c.Materialized().Digest(); err != nil {
		return unitMachine{}, err
	}
	if r, err := c.Redacted(); err == nil {
		um.redacted = &r
	}
	declared := func(rev, reference string, version int64) (unitDependency, error) {
		obj, ok := s.sources[rev].generations[reference]
		if !ok {
			return unitDependency{}, fmt.Errorf("source %s declares no reference %q", rev, reference)
		}
		created, ok := pins.created[pinKey{obj, version}]
		if !ok {
			return unitDependency{}, fmt.Errorf("reference %q (%s version %d) was not classified", reference, obj, version)
		}
		return unitDependency{reference: reference, object: obj, version: version, created: created}, nil
	}
	// Effective (ruling R35): a name can repeat across sources, so each object is the generation
	// of the source whose occurrence reached the output; the set must be Effective()'s.
	type refVersion struct {
		reference string
		version   int64
	}
	reached := map[refVersion]bool{}
	for _, r := range um.provenance {
		if r.Output == "" {
			continue
		}
		d, err := declared(r.Source.Revision, r.Reference, r.Version)
		if err != nil {
			return unitMachine{}, err
		}
		if !slices.Contains(um.effective, d) {
			um.effective = append(um.effective, d)
		}
		reached[refVersion{r.Reference, r.Version}] = true
	}
	effective := c.Effective()
	if len(reached) != len(effective) {
		return unitMachine{}, errors.New("the provenance records' outputs are not the effective dependencies")
	}
	for _, d := range effective {
		if !reached[refVersion{d.Reference, d.Version}] {
			return unitMachine{}, errors.New("the provenance records' outputs are not the effective dependencies")
		}
	}
	for _, o := range c.Reproduction() {
		d, err := declared(o.Source.Revision, o.Reference, o.Version)
		if err != nil {
			return unitMachine{}, err
		}
		digest, err := hex.DecodeString(o.Source.Digest)
		if err != nil || len(digest) != sha256.Size {
			return unitMachine{}, fmt.Errorf("source %s: a digest that is not a SHA-256", o.Source.Revision)
		}
		d.source, d.digest, d.path, d.occurrence = o.Source.Revision, [32]byte(digest), o.Path, o.Occurrence
		um.reproduction = append(um.reproduction, d)
	}
	return um, nil
}

// compileRefusal is ruling R33 for a compilation's failure on machine ("" for the cluster): a
// refused composition, validation, contract or Kubernetes version, and an input ingestion's
// rules refuse, are 422 naming the rule, the input and the paths (redacted, §8.3) and the
// message (§8.3's redacted text, or a fixed one); anything else is an error.
func compileRefusal(machine string, err error) (*refusal, error) {
	if err == nil {
		return nil, nil
	}
	var rule, input, message string
	var paths []string
	var ce *compile.Error
	var ie *ingest.Refusal
	switch {
	case errors.As(err, &ce):
		rule, input, paths, message = string(ce.Rule), ce.Input, ce.Paths, ce.Message
	case errors.As(err, &ie):
		rule, paths = string(ie.Rule), ie.Paths
		if at := (*compile.InputError)(nil); errors.As(err, &at) {
			input = at.Input
		}
	default:
		return nil, err
	}
	ref := refuse(http.StatusUnprocessableEntity, "validation-failed",
		fmt.Sprintf("Publication refused: the configuration was refused (%s).", rule)).with("rule", rule)
	if machine != "" {
		ref = ref.with("machine", machine)
	}
	if input != "" {
		ref = ref.with("input", input)
	}
	if len(paths) > 0 {
		ref = ref.with("paths", paths)
	}
	if message != "" {
		ref = ref.with("message", message)
	}
	return ref, nil
}

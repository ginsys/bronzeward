package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
)

// declaration is one reference row of a source revision (compilation §5.2): its kind, version,
// encoding ("" for none) and the provider generation it resolves to.
type declaration struct {
	kind, encoding, generation string
	version                    int64
}

// declaredSource is a source revision a release composes: the SHA-256 of its stored text and its
// declarations by reference name, with the names in order.
type declaredSource struct {
	digest       [32]byte
	declarations map[string]declaration
	names        []string
}

// checkDeclarations holds each covered machine's reproduction dependencies and provenance records
// to the declarations and stored text of the sources its composition names (compilation §5.2,
// §8.2, §9): its import base, the fragments its assignment's profiles pin and those its assignment
// selects, read from the rows this transaction wrote (ruling R20). A record agrees when its source
// is one of these, with that source's digest, and its reference is declared there at the same
// version, generation, encoding and kind; every declaration then has a reproduction dependency and
// a provenance record, and both name the same occurrences. It returns the first disagreement as
// "machine <id>: <clause> (...)", or "" when all agree. The compiler alone enumerates the
// occurrences and a mapping's members; nothing here parses a document (ruling R17).
func checkDeclarations(ctx context.Context, tx *sql.Tx, rel string, u releaseUnit) (string, error) {
	compositions := map[string]map[string]bool{} // machine -> source revision -> is the import base
	var revisions []string
	if err := eachQueried(ctx, tx, `SELECT m.machine, m.import_base_revision, true FROM release_machine m WHERE m.release = $1
		UNION SELECT m.machine, p.fragment_revision, false FROM release_machine m
			JOIN assignment_revision_profile a ON a.revision = m.assignment_revision
			JOIN release_source s ON s.release = m.release AND s.kind = 'profile' AND s.name = a.profile
			JOIN profile_revision_fragment p ON p.revision = s.profile_revision
			WHERE m.release = $1
		UNION SELECT m.machine, s.fragment_revision, false FROM release_machine m
			JOIN assignment_revision_fragment a ON a.revision = m.assignment_revision
			JOIN release_source s ON s.release = m.release AND s.kind = 'fragment' AND s.name = a.fragment
			WHERE m.release = $1 AND s.fragment_revision IS NOT NULL`, rel, func(row *sql.Rows) error {
		var machine, revision string
		var base bool
		if err := row.Scan(&machine, &revision, &base); err != nil {
			return err
		}
		if compositions[machine] == nil {
			compositions[machine] = map[string]bool{}
		}
		compositions[machine][revision] = base
		if !slices.Contains(revisions, revision) {
			revisions = append(revisions, revision)
		}
		return nil
	}); err != nil {
		return "", err
	}

	sources := map[string]*declaredSource{}
	if err := eachQueried(ctx, tx, `SELECT r.id, r.document, d.name, d.kind, d.version, coalesce(d.encoding, ''), d.generation
			FROM import_base_revision r LEFT JOIN import_base_reference d ON d.revision = r.id WHERE r.id = ANY ($1)
		UNION ALL SELECT r.id, r.document, d.name, d.kind, d.version, coalesce(d.encoding, ''), d.generation
			FROM fragment_revision r LEFT JOIN fragment_reference d ON d.revision = r.id WHERE r.id = ANY ($1)
		ORDER BY 1, 3`, revisions, func(row *sql.Rows) error {
		var revision, document string
		var name, kind, encoding, generation sql.NullString
		var version sql.NullInt64
		if err := row.Scan(&revision, &document, &name, &kind, &version, &encoding, &generation); err != nil {
			return err
		}
		s := sources[revision]
		if s == nil {
			s = &declaredSource{digest: sha256.Sum256([]byte(document)), declarations: map[string]declaration{}}
			sources[revision] = s
		}
		if name.Valid {
			s.declarations[name.String] = declaration{kind: kind.String, encoding: encoding.String, generation: generation.String,
				version: version.Int64}
			s.names = append(s.names, name.String)
		}
		return nil
	}); err != nil {
		return "", err
	}
	if len(sources) != len(revisions) {
		return "", fmt.Errorf("release %s: %d of %d composed source revisions read", rel, len(sources), len(revisions))
	}

	for _, m := range u.machines {
		if clause := m.disagreement(compositions[m.machine], sources); clause != "" {
			return "machine " + m.machine + ": " + clause, nil
		}
	}
	return "", nil
}

// occurrence is one reference occurrence: its source revision, reference and redacted path, and
// its position among its source revision's occurrences, which tells apart two whose redacted
// paths read the same (§9).
type occurrence struct {
	source, reference, path string
	n                       int
}

// disagreement is the first clause on which m's records disagree with the sources its composition
// names (composition: source revision -> is the import base), or "".
func (m unitMachine) disagreement(composition map[string]bool, sources map[string]*declaredSource) string {
	at := func(clause, source, reference string) string {
		return fmt.Sprintf("%s (source %s, reference %s)", clause, source, reference)
	}
	reproduced := map[occurrence]bool{}
	occurrences := map[string][]int{}
	var order []string
	for _, d := range m.reproduction {
		if _, ok := composition[d.source]; !ok {
			return at("reproduction source outside the composition", d.source, d.reference)
		}
		s := sources[d.source]
		decl, ok := s.declarations[d.reference]
		switch {
		case !ok:
			return at("reproduction names no declaration", d.source, d.reference)
		case d.version != decl.version:
			return at("reproduction version differs from the declaration", d.source, d.reference)
		case d.object != decl.generation:
			return at("reproduction object differs from the declared generation", d.source, d.reference)
		case d.digest != s.digest:
			return at("reproduction source digest differs", d.source, d.reference)
		}
		reproduced[occurrence{d.source, d.reference, d.path, d.occurrence}] = true
		if occurrences[d.source] == nil {
			order = append(order, d.source)
		}
		occurrences[d.source] = append(occurrences[d.source], d.occurrence)
	}
	// Occurrences are told apart by their position among their source revision's (§9).
	for _, source := range order {
		o := slices.Sorted(slices.Values(occurrences[source]))
		for i, n := range o {
			if n != i {
				return at("reproduction occurrences are not numbered from 0", source, "")
			}
		}
	}

	recorded := map[occurrence]bool{}
	// An occurrence's member has one outcome: one override, or one record per distinct output path.
	type outcome struct {
		occurrence
		member int
	}
	overridden := map[outcome]bool{}
	outputs := map[outcome]map[string]bool{}
	for _, r := range m.provenance {
		if _, ok := composition[r.Source.Revision]; !ok {
			return at("provenance source outside the composition", r.Source.Revision, r.Reference)
		}
		s := sources[r.Source.Revision]
		decl, ok := s.declarations[r.Reference]
		switch {
		case r.Source.Digest != hex.EncodeToString(s.digest[:]):
			return at("provenance source digest differs", r.Source.Revision, r.Reference)
		case !ok:
			return at("provenance names no declaration", r.Source.Revision, r.Reference)
		case r.Version != decl.version:
			return at("provenance version differs from the declaration", r.Source.Revision, r.Reference)
		case r.Encoding != decl.encoding:
			return at("provenance encoding differs from the declaration", r.Source.Revision, r.Reference)
		case decl.kind == "mapping" && r.Member < 0, decl.kind != "mapping" && r.Member != -1:
			return at("provenance member does not fit the declared kind", r.Source.Revision, r.Reference)
		}
		if by := r.OverriddenBy; by != nil {
			if base, ok := composition[by.Revision]; !ok || base {
				return at("provenance override is not a fragment of the composition", r.Source.Revision, r.Reference)
			}
			if d := sources[by.Revision].digest; by.Digest != hex.EncodeToString(d[:]) {
				return at("provenance override digest differs", r.Source.Revision, r.Reference)
			}
		}
		o := occurrence{r.Source.Revision, r.Reference, r.SourcePath, r.Occurrence}
		k := outcome{o, r.Member}
		if overridden[k] || (r.OverriddenBy != nil && outputs[k] != nil) || outputs[k][r.Output] {
			return at("provenance outcome repeated", r.Source.Revision, r.Reference)
		}
		if r.OverriddenBy != nil {
			overridden[k] = true
		} else {
			if outputs[k] == nil {
				outputs[k] = map[string]bool{}
			}
			outputs[k][r.Output] = true
		}
		recorded[o] = true
	}

	// Every declaration is used (compilation §5.2, stage 1), so each has an occurrence in both.
	composed := make([]string, 0, len(composition))
	for source := range composition {
		composed = append(composed, source)
	}
	slices.Sort(composed)
	for _, source := range composed {
		for _, name := range sources[source].names {
			if !hasOccurrence(reproduced, source, name) {
				return at("declaration without a reproduction dependency", source, name)
			}
			if !hasOccurrence(recorded, source, name) {
				return at("declaration without a provenance record", source, name)
			}
		}
	}
	for _, r := range m.provenance {
		if !reproduced[occurrence{r.Source.Revision, r.Reference, r.SourcePath, r.Occurrence}] {
			return at("provenance occurrence without a reproduction dependency", r.Source.Revision, r.Reference)
		}
	}
	for _, d := range m.reproduction {
		if !recorded[occurrence{d.source, d.reference, d.path, d.occurrence}] {
			return at("reproduction dependency without a provenance record", d.source, d.reference)
		}
	}
	return ""
}

func hasOccurrence(set map[occurrence]bool, source, reference string) bool {
	for o := range set {
		if o.source == source && o.reference == reference {
			return true
		}
	}
	return false
}

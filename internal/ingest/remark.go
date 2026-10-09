package ingest

import (
	"bytes"
	"errors"
	"maps"

	"go.yaml.in/yaml/v3"
)

// Remark re-enters compilation.md §2.3 at step 3 on a paused claim's staged sanitized document
// with an operator's new marks (§3.6 item 3). The guard checks only the values these marks
// extract; the references already declared stay as they are, their values in the provider. The
// result must be the staged document byte for byte except at the nodes the marks replace, an
// embedded document's text included: anything else is refused mark-rewrites-text, since no guard
// can run on text whose earlier values it does not hold.
func Remark(st Staged, marks []Path) (_ *Candidate, err error) {
	if len(marks) == 0 {
		return nil, errors.New("ingest: a remark needs at least one mark")
	}
	if err := st.Sanitized.Check(); err != nil {
		return nil, err
	}
	staged := st.Sanitized.Documents()
	old, err := parseStream(staged)
	if err != nil {
		return nil, err
	}
	known, complete := knownSecrets(old, marks)
	defer func() {
		err = position(err, marks)
		if complete {
			err = redactRefusal(err, known)
		} else {
			err = documentsOnly(err)
		}
	}()
	if again, err := encodeStream(old); err != nil || !bytes.Equal(again, staged) {
		return nil, refuse(RuleMarkRewritesText, marks[0].String())
	}
	holders := map[*yaml.Node]Path{}
	for _, m := range marks {
		if n, ok := resolve(old, m.Doc, m.Pointer); ok && m.Format != "" {
			if _, seen := holders[n]; !seen {
				holders[n] = m
			}
		}
	}
	text := string(staged)
	c, err := Extract(Request{Input: Unresolved{s: &text}, Marks: marks, Declarations: st.Sanitized.Declarations()})
	if err != nil {
		return nil, err
	}
	next, err := parseStream(c.docs)
	if err != nil || len(next) != len(old) {
		return nil, errors.New("ingest: the sanitized stream does not parse back")
	}
	minted := map[string]bool{}
	for _, v := range c.values {
		minted[v.name] = true
	}
	for i := range old {
		if bad := changedBesides(old[i], next[i], minted); bad != nil {
			if m, ok := holders[bad]; ok {
				return nil, refuseAt(RuleMarkRewritesText, []Path{m}, m.String())
			}
			return nil, refuse(RuleMarkRewritesText, marks[0].String())
		}
	}
	return c, nil
}

// changedBesides compares a staged node with its remarked counterpart and returns the staged node
// where they differ other than by a reference minted in minted, or nil. A scalar whose text
// changed is accepted only as an embedded document already in the encoder's form whose own
// nodes differ only so; the scalar holding it is returned otherwise. Positions are not compared.
func changedBesides(old, next *yaml.Node, minted map[string]bool) *yaml.Node {
	if next.Tag == refTag && minted[next.Value] {
		return nil
	}
	if old.Kind != next.Kind || old.Tag != next.Tag || old.Style != next.Style || old.Anchor != next.Anchor ||
		old.HeadComment != next.HeadComment || old.LineComment != next.LineComment ||
		old.FootComment != next.FootComment || len(old.Content) != len(next.Content) {
		return old
	}
	if old.Value != next.Value {
		if old.Kind != yaml.ScalarNode || !embeddedChangedOnlyBy(old, next, minted) {
			return old
		}
		return nil
	}
	for i := range old.Content {
		if bad := changedBesides(old.Content[i], next.Content[i], minted); bad != nil {
			return bad
		}
	}
	return nil
}

// embeddedChangedOnlyBy reports whether old and next hold embedded documents, old's text is what
// extraction writes back for it (§5.4: styles and comments dropped, two-space block YAML), and
// next differs from it only by references in minted.
func embeddedChangedOnlyBy(old, next *yaml.Node, minted map[string]bool) bool {
	o, err1 := embeddedDocument(old)
	n, err2 := embeddedDocument(next)
	plain, err3 := embeddedDocument(old)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	plainStyle(plain)
	text, err := encodeStream([]*yaml.Node{plain})
	if err != nil || string(text) != old.Value {
		return false
	}
	return changedBesides(o, n, minted) == nil
}

// Remarked is the envelope a remark stages (compilation.md §3.6 item 3): s, every earlier
// generation with created's added, and the baseline carried over unchanged. st is not modified.
func (st Staged) Remarked(s Sanitized, created map[string]string) Staged {
	gens := maps.Clone(st.Generations)
	if gens == nil {
		gens = map[string]string{}
	}
	maps.Copy(gens, created)
	return Staged{Sanitized: s, Generations: gens, Baseline: st.Baseline}
}

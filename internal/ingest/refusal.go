package ingest

import (
	"fmt"
	"strings"
)

// Refusal is an input refused by a rule (compilation.md §13). It names the rule and the paths
// involved, never a value: a path token holding an extracted value is already shown as
// <redacted> when the refusal is made.
type Refusal struct {
	Rule  Rule
	Paths []string
}

// Rule names why an input was refused.
type Rule string

const (
	RuleParse            Rule = "parse"             // the stream does not parse
	RuleSchemaUnloadable Rule = "schema-unloadable" // the machinery cannot load a document
	RuleSchemaIndirect   Rule = "schema-indirect"   // a secret field the machinery finds is not one plain input node
	RuleMarkUnaddressed  Rule = "mark-unaddressed"  // a mark addresses no node
	RuleMarkKind         Rule = "mark-kind"         // an identified node is not a string, integer, boolean or mapping of those
	RuleBadPath          Rule = "bad-path"          // a path does not parse or names an undeclared embedded document
	RuleGuardValue       Rule = "guard-value"       // a scalar equals an extracted value
	RuleGuardSubstring   Rule = "guard-substring"   // a scalar contains an extracted value
	RuleReservedText     Rule = "reserved-text"     // a string holds the text !bwref
	RuleLocalTag         Rule = "local-tag"         // a tag other than !bwref and the YAML core tags
	RuleTagPlacement     Rule = "tag-placement"     // !bwref on a mapping key or a sequence
	RuleUndeclaredName   Rule = "undeclared-name"   // a !bwref name with no declaration
	RuleUnusedName       Rule = "unused-declaration"
	RuleBadName          Rule = "bad-name"        // a name outside the grammar of §5.1
	RuleBadDeclaration   Rule = "bad-declaration" // a kind, version or encoding outside §5.2
	RuleEmbedded         Rule = "embedded"        // an identified embedded document is not a string or does not parse
)

func (r *Refusal) Error() string {
	if len(r.Paths) == 0 {
		return fmt.Sprintf("ingest: refused (%s)", r.Rule)
	}
	return fmt.Sprintf("ingest: refused (%s) at %s", r.Rule, strings.Join(r.Paths, ", "))
}

func refuse(rule Rule, paths ...string) error { return &Refusal{Rule: rule, Paths: paths} }

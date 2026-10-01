// Package ingest is the ingestion pipeline's pure part (compilation.md §2): the typed boundary
// between input that may hold secrets and what may be persisted, the multi-document parse, path
// addressing, identification of the values to extract, their substitution by `!bwref` references
// with declarations, the extraction guard and the authoring checks of §5.
//
// Input enters as an Unresolved, which renders nothing and is read only by this package's parser.
// A Sanitized, the only form ordinary persistence accepts, is constructed only inside this
// package: by Candidate.Commit, after the guard has passed and every extracted value has been
// handed to the caller's create function (§2.3 step 6, which writes the provider generations).
// The zero Sanitized is refused by Check.
//
// Choices made here, not in the specification:
//
//   - Schema identification loads each document of the stream on its own with the pinned
//     machinery and diffs its encoding with and without RedactSecrets, as the sensitivity and
//     provenance evidence did; a document the machinery cannot load refuses the input, because
//     identification would otherwise be incomplete.
//   - A refusal names its rule and the paths involved. A path token that holds a value the
//     request extracts (a mapping key can be the secret) is shown as <redacted>, for refusals
//     made before substitution too: the values are those the machinery redacts and those under
//     each mark that resolves. Parser errors keep only a line. A create callback's error is
//     returned as a CreateError that names the reference and does not render the cause.
//   - An alias inside the node it names is refused as a parse failure: the graph is infinite.
//   - A field the machinery redacts must be one plain input scalar whose text is the value the
//     machinery encodes. A secret reached through a merge key, folded over lines or tagged
//     !!binary refuses the input (schema-indirect) rather than staying unextracted.
//   - A marked node's kind is its YAML kind: !!str, !!int in canonical decimal, !!bool spelled
//     true or false, or a mapping of those. Any other marked node, 0x1F, -0 and True
//     included, refuses the input: it would render back differently from how it was authored.
//   - A minted name is "s-" followed by 26 random base32 characters.
//   - The guard also searches every comment and every anchor and alias name, which §4.2 does
//     not name: they are persisted with the stream and no other check reads them. A declared
//     embedded document is searched both as its whole text, without this run's references, and
//     node by node. The members of an extracted mapping are guard values, its keys are not; an
//     empty value is not searched for, since it matches everything.
//   - Marks inside an identified embedded document are resolved in its parsed form; the document
//     is then written back as block YAML with two-space indentation, without the author's styles
//     or comments, whether it was declared yaml or json (§5.4).
package ingest

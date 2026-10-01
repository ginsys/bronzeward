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
//     each mark that resolves, aliases followed. A token is also redacted when it reads as the
//     same number or boolean as one of them (0x4cb2f, 3.14159e5 and 314159 are one number), as
//     the guard compares; a token has lost its key's tag, so it is read under every scalar tag. A rendered path that still holds a value, unescaped or split
//     differently from its keys (a key ending in |yaml), names its document only. When the
//     machinery cannot load a document, or a value it redacts is not the text of its input
//     scalar, the input's spelling of those values is not known, and a refusal names documents
//     only. A string that does not parse as a path is shown only if it is a reference name
//     holding no value.
//     Parser errors keep only a line. A create callback's error is returned as a CreateError
//     that names the reference and answers errors.Is for the cause, but holds it where no fmt
//     verb, reflection or Unwrap reaches it; errors.As cannot reach it either.
//   - An alias inside the node it names is refused as a parse failure: the graph is infinite. So
//     is a mapping key whose text an earlier key of the same mapping holds (1 and "1" included,
//     as a pointer token matches either): a path through it would name two nodes, and
//     extracting one would leave the other value in the stream. So is a key that is not a
//     scalar, which no pointer token names.
//   - A target that is a member of a marked mapping, reached only through it, is extracted as
//     part of the mapping. A member that aliases a target, or a target also reached elsewhere,
//     refuses the mapping as mark-kind.
//   - Every target is stored as a reference, which a later ingestion loads as a null. A
//     document the machinery cannot load with its targets as nulls (a mark on its kind) refuses
//     the input as schema-unloadable: it could never be ingested again.
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
//     node by node. The keys and members of an extracted mapping are guard values, as the
//     provider stores both; an empty value is not searched for, since it matches everything.
//   - Marks inside an identified embedded document are resolved in its parsed form; the document
//     is then written back as block YAML with two-space indentation, without the author's styles
//     or comments, whether it was declared yaml or json (§5.4).
package ingest

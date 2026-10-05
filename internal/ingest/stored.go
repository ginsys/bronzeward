package ingest

import "errors"

// errStored refuses a stored source that is not a well-formed sanitized source. Its text names
// no part of the source.
var errStored = errors.New("ingest: the stored source is not a well-formed sanitized source")

// Stored rebuilds a source revision's sanitized value from what persistence stored (compilation
// §6 step 1): its sanitized text, and the declarations its reference rows and embedded
// identifications hold. It checks the stream as authored, as a resume does (§3.1), and only then
// constructs the value; it cannot run the guard, which ingestion ran before the text was stored.
func Stored(documents string, decl Declarations) (Sanitized, error) {
	if documents == "" {
		return Sanitized{}, errStored
	}
	docs, err := parseStream([]byte(documents))
	if err != nil {
		return Sanitized{}, errStored
	}
	if err := validate(docs, decl); err != nil {
		return Sanitized{}, errStored
	}
	return newSanitized([]byte(documents), decl), nil
}

package main

import "testing"

// TestImportBaseDocumentRefused checks that import-base document reads nothing unless standard
// input and output are a terminal, and that it has no -o json form and takes neither --no-input nor
// --token-stdin (client-contracts.md §11.3).
func TestImportBaseDocumentRefused(t *testing.T) {
	f, step := importSteps(t)
	doc := []string{"import-base", "document", "ibr_BWSYNTH"}
	step("standard output not a terminal", doc, 2, "", "only when standard input and standard output are a terminal, with no override; nothing was read or written")
	step("-o json", append(doc, "-o", "json"), 2, "", "has no -o json or yaml form")
	step("-o yaml", append(doc, "-o", "yaml"), 2, "", "has no -o json or yaml form")
	step("--no-input", append(doc, "--no-input"), 2, "", "takes neither --no-input nor --token-stdin")
	step("--token-stdin", append(doc, "--token-stdin"), 2, "", "takes neither --no-input nor --token-stdin")
	step("no identifier", []string{"import-base", "document"}, 2, "", "import-base document")
	step("no verb", []string{"import-base"}, 2, "", "import-base needs document")
	if f.documents != 0 {
		t.Errorf("the document was read %d times", f.documents)
	}
}

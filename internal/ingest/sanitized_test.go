package ingest

import (
	"errors"
	"reflect"
	"testing"
)

// TestZeroSanitizedRefused: the zero value is refused by Check, which every persistence function
// calls first (compilation.md §2.1).
func TestZeroSanitizedRefused(t *testing.T) {
	var s Sanitized
	if err := s.Check(); !errors.Is(err, ErrZeroSanitized) {
		t.Fatalf("Check() on the zero value = %v", err)
	}
	if s.Documents() != nil {
		t.Fatal("the zero value has documents")
	}
}

// TestSanitizedFieldsUnexported: no caller outside the package can fill a Sanitized; the only
// constructor is newSanitized.
func TestSanitizedFieldsUnexported(t *testing.T) {
	for f := range reflect.TypeFor[Sanitized]().Fields() {
		if f.IsExported() {
			t.Errorf("Sanitized.%s is exported", f.Name)
		}
	}
}

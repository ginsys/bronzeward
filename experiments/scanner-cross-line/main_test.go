package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestCases holds the oracle to the expectations of sets/cases.json. The expectations were written
// from the issues before the oracle ran; a mismatch means the case, not the oracle, is rechecked
// (the oracle is go-yaml itself). "want": null marks a stream go-yaml refuses.
func TestCases(t *testing.T) {
	raw, err := os.ReadFile("sets/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string          `json:"name"`
		Doc  string          `json:"doc"`
		Want json.RawMessage `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// A missing key is a malformed case, not a refusal: only an explicit null is one.
			if len(c.Want) == 0 {
				t.Fatal(`the case has no "want" key`)
			}
			got := References(c.Doc)
			if string(c.Want) == "null" {
				if got.Err == "" {
					t.Fatalf("go-yaml accepted the stream, listing %v", got.Tags)
				}
				return
			}
			var want []string
			if err := json.Unmarshal(c.Want, &want); err != nil {
				t.Fatalf(`the case has a "want" that is no list of names: %v`, err)
			}
			if got.Err != "" {
				t.Fatalf("go-yaml refused the stream: %s", got.Err)
			}
			if !slices.Equal(got.Tags, want) {
				t.Fatalf("go-yaml lists %v, want %v", got.Tags, want)
			}
		})
	}
}

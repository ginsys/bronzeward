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
		Name string   `json:"name"`
		Doc  string   `json:"doc"`
		Want []string `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := References(c.Doc)
			if c.Want == nil {
				if got.Err == "" {
					t.Fatalf("go-yaml accepted the stream, listing %v", got.Tags)
				}
				return
			}
			if got.Err != "" {
				t.Fatalf("go-yaml refused the stream: %s", got.Err)
			}
			if !slices.Equal(got.Tags, c.Want) {
				t.Fatalf("go-yaml lists %v, want %v", got.Tags, c.Want)
			}
		})
	}
}

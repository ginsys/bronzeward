package clitest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// shapeRecord is the shape of one conformance record in testdata (client-contracts.md §12): the text
// members are strings and the exchange and absent members are lists of strings. A plain scalar that
// holds a colon and a space reads as a one-key mapping, not as a string, so a list entry written
// that way fails the strict decode below instead of passing unread (#787).
type shapeRecord struct {
	ID       string   `yaml:"id"`
	Setup    string   `yaml:"setup"`
	Action   string   `yaml:"action"`
	Exchange []string `yaml:"exchange"`
	Fault    string   `yaml:"fault"`
	Expect   struct {
		Client string   `yaml:"client"`
		Server string   `yaml:"server"`
		Shown  string   `yaml:"shown"`
		Absent []string `yaml:"absent"`
	} `yaml:"expect"`
	Test   string `yaml:"test"`
	Unit   string `yaml:"unit"`
	NotRun string `yaml:"not run"`
}

// decodeShape decodes one record file strictly: an unknown member or a member of the wrong type is an error.
func decodeShape(b []byte) ([]shapeRecord, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var records []shapeRecord
	if err := dec.Decode(&records); err != nil {
		return nil, err
	}
	return records, nil
}

// TestRecordShape reads every testdata/*cases.yaml file and fails on a record whose member has the
// wrong type. It starts no server, so it needs no database.
func TestRecordShape(t *testing.T) {
	// The control: the strict decode refuses an exchange entry that holds a colon and a space in a
	// plain scalar, so a decode that stopped being strict fails here and not silently.
	if _, err := decodeShape([]byte("- id: X-01\n  exchange:\n    - GET /a (variants): refused\n")); err == nil {
		t.Fatal("the control entry decoded as a string, so the check below proves nothing")
	}
	if _, err := decodeShape([]byte("- id: X-01\n  exchange:\n    - 'GET /a (variants): refused'\n")); err != nil {
		t.Fatalf("the control entry, quoted, did not decode: %v", err)
	}
	files, err := filepath.Glob("testdata/*cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no record files found")
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		records, err := decodeShape(b)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if len(records) == 0 {
			t.Errorf("%s holds no record", file)
		}
		for _, r := range records {
			if r.ID == "" {
				t.Errorf("%s holds a record with no id", file)
			}
		}
	}
}

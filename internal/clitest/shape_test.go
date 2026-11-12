package clitest

import (
	"bytes"
	"fmt"
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

// onlyStrings fails on the first scalar the YAML core schema does not tag as a string. The typed decode
// turns a number or a boolean into a Go string and skips a null, so it cannot check this itself. Every
// scalar in a record file is a string, so one walk over the node tree ends the class.
func onlyStrings(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.ShortTag() != "!!str" {
		return fmt.Errorf("line %d: want a string, found %s %q", n.Line, n.ShortTag(), n.Value)
	}
	for _, c := range n.Content {
		if err := onlyStrings(c); err != nil {
			return err
		}
	}
	return nil
}

// decodeShape decodes one record file strictly: a scalar that is not a string, an unknown member or a
// member of the wrong type is an error.
func decodeShape(b []byte) ([]shapeRecord, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if err := onlyStrings(&doc); err != nil {
		return nil, err
	}
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
	// More controls, each one a shape the check must refuse. A number or a boolean is not a string,
	// and an unknown member of a record or of expect is an error.
	for name, doc := range map[string]string{
		"a number in exchange":         "- id: X-01\n  exchange:\n    - 123\n",
		"a boolean in exchange":        "- id: X-01\n  exchange:\n    - true\n",
		"a number in a text member":    "- id: X-01\n  setup: 5\n",
		"an unknown record member":     "- id: X-01\n  extra: x\n",
		"an unknown expect member":     "- id: X-01\n  expect:\n    extra: x\n",
		"a mapping in absent":          "- id: X-01\n  expect:\n    absent:\n      - a: b\n",
		"a mapping where text belongs": "- id: X-01\n  setup:\n    a: b\n",
		"a null in a text member":      "- id: X-01\n  setup: null\n",
		"a blank text member":          "- id: X-01\n  setup:\n",
		"a tilde in a text member":     "- id: X-01\n  setup: ~\n",
		"a null in exchange":           "- id: X-01\n  exchange: [null]\n",
	} {
		if _, err := decodeShape([]byte(doc)); err == nil {
			t.Fatalf("the control %q decoded, so the check below proves nothing", name)
		}
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

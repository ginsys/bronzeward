package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"testing"
	"testing/fstest"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// §11 rule 6: until the first release the schema is one migration, edited in place.
func TestEmbeddedLoads(t *testing.T) {
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Version != 1 || ms[0].Name != "schema" {
		t.Fatalf("got %+v", ms)
	}
}

func TestChecksumIsFileSHA256(t *testing.T) {
	ms, err := load(fstest.MapFS{"m/0001_a.sql": file("SELECT 1")}, "m")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("SELECT 1"))
	if ms[0].Checksum != hex.EncodeToString(sum[:]) || ms[0].SQL != "SELECT 1" || ms[0].Name != "a" {
		t.Fatalf("got %+v", ms[0])
	}
}

func TestLoadRefuses(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"short number": {"m/1_a.sql": file("SELECT 1")},
		"no name":      {"m/0001_.sql": file("SELECT 1")},
		"not sql":      {"m/0001_a.txt": file("SELECT 1")},
		"zero":         {"m/0000_a.sql": file("SELECT 1")},
		"gap":          {"m/0001_a.sql": file("SELECT 1"), "m/0003_c.sql": file("SELECT 1")},
		"duplicate":    {"m/0001_a.sql": file("SELECT 1"), "m/0001_b.sql": file("SELECT 1")},
		"empty":        {"m": &fstest.MapFile{Mode: fs.ModeDir}},
	} {
		if _, err := load(fsys, "m"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

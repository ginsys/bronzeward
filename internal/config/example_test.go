package config

import (
	"os"
	"testing"
)

// The committed example must stay loadable: a field Load starts requiring fails here until the
// example carries it.
func TestExampleLoads(t *testing.T) {
	f, err := os.Open("../../examples/bronzeward.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" || c.Database.DSN != "postgres://bronzeward@127.0.0.1:55433/bronzeward?sslmode=disable" {
		t.Fatalf("example drifted from mise run dev-db: %+v", c)
	}
}

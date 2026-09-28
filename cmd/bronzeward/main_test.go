package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
)

func configFile(t *testing.T, dsn string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bronzeward.yaml")
	body := "listen: 127.0.0.1:0\ndatabase:\n  dsn: " + dsn + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestServeRefusesAnUnmigratedDatabase(t *testing.T) {
	_, dsn := dbtest.New(t)
	errc := make(chan error, 1)
	go func() { errc <- serve([]string{"-config", configFile(t, dsn)}) }()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "run bronzeward migrate") {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve started on a database without a schema")
	}
}

func TestMigrateCommandTwice(t *testing.T) {
	_, dsn := dbtest.New(t)
	cfg := configFile(t, dsn)
	var first, second bytes.Buffer
	if err := runMigrate([]string{"-config", cfg}, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "installation epoch ep_") {
		t.Fatalf("first run printed %q", first.String())
	}
	if err := runMigrate([]string{"-config", cfg}, &second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second.String(), "applied []") || strings.Contains(second.String(), "installation epoch") {
		t.Fatalf("second run printed %q", second.String())
	}
}

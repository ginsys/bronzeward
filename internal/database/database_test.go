package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenErrorsHidePassword(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, dsn := range []string{
		"postgres://u:hunter2secret@127.0.0.1:1/x?sslmode=disable&connect_timeout=2", // refused
		"postgres://u:hunter2secret@127.0.0.1:notaport/x",                            // unparsable
		"host=127.0.0.1 user=u password = hunter2secret port=notaport",               // pgx's redaction misses "password ="
	} {
		db, err := Open(ctx, dsn)
		if err == nil {
			db.Close()
			t.Fatalf("Open(%q) succeeded", dsn)
		}
		if strings.Contains(err.Error(), "hunter2secret") {
			t.Fatalf("error discloses the password: %v", err)
		}
	}
}

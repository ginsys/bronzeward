package config

import (
	"os"
	"testing"
	"time"
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
	if c.Auth.OIDC.Issuer != "http://127.0.0.1:5556" || c.Auth.OIDC.Audience != "bronzeward" {
		t.Fatalf("example's issuer drifted from the documented local issuer: %+v", c.Auth.OIDC)
	}
	if c.Execution.SettleFloor != 30*time.Second || c.Execution.MaxTransportDeadline != 5*time.Minute {
		t.Fatalf("example's execution settings: %+v", c.Execution)
	}
}

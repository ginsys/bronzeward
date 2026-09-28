package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	c, err := Load(strings.NewReader("listen: 127.0.0.1:8443\ndatabase:\n  dsn: postgres://bw@localhost/bw\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8443" || c.Database.DSN != "postgres://bw@localhost/bw" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"unknown field": "listen: :1\ndatabase: {dsn: x}\nlisten_addr: :2\n",
		"no listen":     "database: {dsn: x}\n",
		"no dsn":        "listen: :1\n",
		"empty":         "",
		"two documents": "listen: :1\ndatabase: {dsn: x}\n---\nlisten: :2\n",
	} {
		if _, err := Load(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

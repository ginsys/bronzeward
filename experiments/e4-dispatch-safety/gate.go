package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Gates let the harness stop an executor at a named point and act while it waits: revoke, take
// over, inject a fault or kill it. A gate is armed by creating <dir>/<name>.hold; the executor
// then creates <name>.reached and waits for <name>.go. Unarmed gates cost one stat.
//
//	evidence   after the execution-time observation, before the commitment transaction
//	commit     inside the commitment transaction, after every comparison, before COMMIT
//	committed  after the commitment committed, before the attempt transaction
//	attempt    inside the attempt transaction, after every comparison, before COMMIT
//	send       after the attempt committed, before talosctl starts
//	response   after talosctl returned, before the response is recorded
//	verify     after the response is recorded, before the first completion observation
//	complete   after the matching completion observation, before the completion transaction
type Gates struct {
	dir string
	log *Log
}

// gateLimit bounds a wait so a harness failure cannot leave an executor holding locks forever.
const gateLimit = 5 * time.Minute

var errGateTimeout = errors.New("gate: no release within the limit")

func (g Gates) Wait(name string) error {
	if g.dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(g.dir, name+".hold")); err != nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(g.dir, name+".reached"), nil, 0o644); err != nil {
		return err
	}
	g.log.Printf("event=gate-reached gate=%s", name)
	for limit := time.Now().Add(gateLimit); time.Now().Before(limit); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(g.dir, name+".go")); err == nil {
			g.log.Printf("event=gate-released gate=%s", name)
			return nil
		}
	}
	return errGateTimeout
}

// Hook is Wait for a point inside a transaction, where the store cannot take an error: a gate
// that is never released ends the process, and the database rolls the transaction back.
func (g Gates) Hook(name string) func() {
	return func() {
		if err := g.Wait(name); err != nil {
			g.log.Printf("event=gate-failed gate=%s err=%q", name, err)
			os.Exit(exitError)
		}
	}
}

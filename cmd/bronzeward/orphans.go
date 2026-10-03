package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ginsys/bronzeward/internal/database"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
	"github.com/ginsys/bronzeward/internal/orphans"
	"github.com/ginsys/bronzeward/internal/provider"
)

const orphansUsage = "usage: bronzeward orphans -config <file> [-cluster <cl>]"

// orphanTestHooks are the controls of the command's checks, each a wrong implementation the checks
// must catch; production sets none.
type orphanTestHooks struct {
	afterLoad    func()                                         // runs once the configuration is loaded
	tokenAtLoad  bool                                           // read the token before afterLoad
	lister       func(orphans.Lister, io.Writer) orphans.Lister // wraps the provider listing
	cluster      func(string) string                            // replaces -cluster after validation
	afterCollect func(*sql.DB) error                            // runs between collecting and printing
}

var orphanHooks orphanTestHooks

// runOrphans is the orphan report of persistence-api.md §6.4: an operator command, served by no
// route. It lists gen/ through the orphan-report identity, whose token it reads from
// provider.reportTokenFile immediately before the first request, then reads the reference rows and
// claims, and prints the report only once all of it succeeded. It deletes nothing and writes
// nothing to the database.
func runOrphans(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("orphans", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "deployment configuration file")
	cluster := fs.String("cluster", "", "report only this cluster's generations, a cl identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments\n%s", orphansUsage)
	}
	if *cluster != "" && id.MustHave(*cluster, id.Cluster) != nil {
		return errors.New("-cluster is not a cluster identifier")
	}
	// Each failure before the first listing names its step. Its reason names only the operator's
	// own files and settings, never a generation path, a value or metadata.
	cfg, err := readConfig(*path)
	if err != nil {
		return fmt.Errorf("reading the configuration: %w", err)
	}
	if cfg.Provider == nil || cfg.Provider.ReportTokenFile == "" {
		return errors.New("reading the configuration: it names no provider.reportTokenFile")
	}
	var early provider.Token
	if orphanHooks.tokenAtLoad {
		if early, err = provider.ReadTokenFile(cfg.Provider.ReportTokenFile); err != nil {
			return fmt.Errorf("reading the report token: %w", err)
		}
	}
	if orphanHooks.afterLoad != nil {
		orphanHooks.afterLoad()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer db.Close()
	ms, err := migrate.Embedded()
	if err != nil {
		return fmt.Errorf("checking the schema: %w", err)
	}
	if err := migrate.Check(ctx, db, ms); err != nil {
		return fmt.Errorf("checking the schema: %w", err)
	}
	tok := early
	if !orphanHooks.tokenAtLoad {
		if tok, err = provider.ReadTokenFile(cfg.Provider.ReportTokenFile); err != nil {
			return fmt.Errorf("reading the report token: %w", err)
		}
	}
	rep, err := provider.NewReport(cfg.Provider.Address, tok)
	if err != nil {
		return fmt.Errorf("reading the configuration: %w", err)
	}
	var l orphans.Lister = rep
	if orphanHooks.lister != nil {
		l = orphanHooks.lister(l, stdout)
	}
	scope := *cluster
	if orphanHooks.cluster != nil {
		scope = orphanHooks.cluster(scope)
	}
	r, err := orphans.Collect(ctx, l, db, scope)
	if err != nil {
		return err
	}
	if orphanHooks.afterCollect != nil {
		if err := orphanHooks.afterCollect(db); err != nil {
			return err
		}
	}
	return orphans.Write(stdout, r)
}

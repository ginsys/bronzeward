// Command bronzeward is the Bronzeward server and its operator commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/database"
	"github.com/ginsys/bronzeward/internal/migrate"
	"github.com/ginsys/bronzeward/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bronzeward serve|migrate -config <file>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bronzeward serve:", err)
			os.Exit(1)
		}
	case "migrate":
		if err := runMigrate(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "bronzeward migrate:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "bronzeward: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func loadConfig(name string, args []string) (config.Config, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	path := fs.String("config", "", "deployment configuration file")
	if err := fs.Parse(args); err != nil {
		return config.Config{}, err
	}
	f, err := os.Open(*path)
	if err != nil {
		return config.Config{}, err
	}
	defer f.Close()
	return config.Load(f)
}

// runMigrate applies the binary's migrations and records the installation (persistence-api.md
// §11: the only way the schema changes; run it with the service stopped).
func runMigrate(args []string, out io.Writer) error {
	cfg, err := loadConfig("migrate", args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ms, err := migrate.Embedded()
	if err != nil {
		return err
	}
	applied, err := migrate.Apply(ctx, db, ms)
	if err != nil {
		return err
	}
	epoch, created, err := migrate.Install(ctx, db)
	if err != nil {
		return err
	}
	if applied == nil {
		applied = []int{}
	}
	fmt.Fprintf(out, "applied %v; the schema is at version %d\n", applied, len(ms))
	if created {
		fmt.Fprintf(out, "installation epoch %s recorded\n", epoch)
	}
	return nil
}

func serve(args []string) error {
	cfg, err := loadConfig("serve", args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db, err := database.Open(startCtx, cfg.Database.DSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ms, err := migrate.Embedded()
	if err != nil {
		return err
	}
	if err := migrate.Check(startCtx, db, ms); err != nil {
		return err
	}
	srv := server.NewHTTP(cfg.Listen)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shut); err != nil {
			return err
		}
		if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

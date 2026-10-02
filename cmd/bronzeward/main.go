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

	"github.com/ginsys/bronzeward/internal/api"
	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/database"
	"github.com/ginsys/bronzeward/internal/migrate"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bronzeward serve|migrate|token ...")
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
	case "token":
		if err := runToken(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, "bronzeward token:", err)
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
	return readConfig(*path)
}

func readConfig(path string) (config.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config.Config{}, err
	}
	defer f.Close()
	return config.Load(f)
}

// runMigrate applies the binary's migrations, records the installation, then checks the schema as
// serve does (persistence-api.md §11: the only way the schema changes; run it with the service
// stopped).
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
	fmt.Fprintf(out, "applied %v\n", applied)
	if created {
		fmt.Fprintf(out, "installation epoch %s recorded\n", epoch)
	}
	// Apply refuses unknown migrations only in its preflight: one a newer binary commits after
	// that is seen here, so the schema version is stated only once the server's check passes.
	if err := migrate.Check(ctx, db, ms); err != nil {
		return err
	}
	fmt.Fprintf(out, "the schema is at version %d, exactly this binary's migrations\n", len(ms))
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
	// The epoch this process starts in: it issues ownership under it only (persistence-api §5.1).
	var epoch string
	if err := db.QueryRowContext(startCtx, `SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		return fmt.Errorf("installation state: %w", err)
	}
	var ing api.Ingester // nil without a provider: the ingestion routes answer 503
	if cfg.Provider != nil {
		tok, err := provider.ReadTokenFile(cfg.Provider.IngestionTokenFile)
		if err != nil {
			return err
		}
		k := cfg.Provider.Keys
		c, err := provider.NewIngestion(cfg.Provider.Address, tok, provider.Keys{Baseline: k.Baseline, Staging: k.Staging, Digest: k.Digest})
		if err != nil {
			return err
		}
		ing = c
	}
	// Discovery is lazy: serve starts while the issuer is down, and requests answer 503 until it is up.
	verifier := auth.NewVerifier(cfg.Auth, db, auth.Discover(cfg.Auth.OIDC))
	// The ingest runners stop with the signal; a claim left held lapses with its lease.
	srv := server.NewHTTP(cfg.Listen, server.New(api.New(ctx, db, verifier, cfg.Auth, ing, cfg.Ingestion, epoch)))
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

// Command bronzeward is the Bronzeward server and its operator commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bronzeward serve -config <file>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bronzeward serve:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "bronzeward: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := fs.String("config", "", "deployment configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := os.Open(*path)
	if err != nil {
		return err
	}
	cfg, err := config.Load(f)
	f.Close()
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Listen, Handler: server.New(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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

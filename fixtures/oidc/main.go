// Command oidc runs the fixture's disposable OIDC issuer (fixtures/README.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
)

const usage = `usage: oidc keygen  -out <key file>
       oidc serve   -key <key file> -issuer <url> [-listen 127.0.0.1:5556]
       oidc mint    -key <key file> -issuer <url> [-audience bronzeward] -human <name> [-defect <name>]
       oidc defects`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "oidc:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	out := fs.String("out", "", "key file to create (keygen)")
	keyPath := fs.String("key", "", "signing key file, as keygen writes it")
	issuerURL := fs.String("issuer", "", "issuer URL: the iss claim, with no path")
	audience := fs.String("audience", "bronzeward", "aud claim (mint)")
	listen := fs.String("listen", "127.0.0.1:5556", "listen address (serve)")
	human := fs.String("human", "", "synthetic human (mint): "+strings.Join(slices.Sorted(maps.Keys(issuer.Humans)), ", "))
	defect := fs.String("defect", "", "defect to put in the token (mint): see oidc defects")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch args[0] {
	case "keygen":
		if *out == "" {
			return errors.New("keygen needs -out")
		}
		key, err := issuer.NewKey()
		if err != nil {
			return err
		}
		return issuer.WriteKey(*out, key)
	case "defects":
		for _, d := range issuer.Defects {
			fmt.Fprintln(stdout, d)
		}
		return nil
	case "mint", "serve":
		key, err := issuer.ReadKey(*keyPath)
		if err != nil {
			return err
		}
		iss, err := issuer.New(*issuerURL, *audience, key)
		if err != nil {
			return err
		}
		if args[0] == "serve" {
			return serve(iss, *listen)
		}
		tok, err := iss.Mint(*human, *defect, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, tok)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func serve(iss *issuer.Issuer, listen string) error {
	srv := &http.Server{Addr: listen, Handler: iss.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shut)
	}
}

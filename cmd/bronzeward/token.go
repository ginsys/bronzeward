package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/database"
	"github.com/ginsys/bronzeward/internal/migrate"
)

const tokenUsage = `usage: bronzeward token issue  -config <file> -name <name> -roles <role,...> -responsible <sub> -operator <sub> [-expires 720h]
       bronzeward token rotate -config <file> -identity <idn> -operator <sub> [-roles <role,...>] [-expires 720h]
       bronzeward token revoke -config <file> -identity <idn> -operator <sub>
       bronzeward token list   -config <file>
roles: viewer, author, publisher. -responsible and -operator are subjects of the configured OIDC issuer.`

// Flags each command accepts, and those it requires.
var tokenFlags = map[string]struct{ allowed, required []string }{
	"issue":  {[]string{"config", "name", "roles", "responsible", "operator", "expires"}, []string{"config", "name", "roles", "responsible", "operator"}},
	"rotate": {[]string{"config", "identity", "roles", "operator", "expires"}, []string{"config", "identity", "operator"}},
	"revoke": {[]string{"config", "identity", "operator"}, []string{"config", "identity", "operator"}},
	"list":   {[]string{"config"}, []string{"config"}},
}

// runToken is the operator's automation-token tool (persistence-api.md §10.2), the only way to
// issue, rotate, list or revoke tokens; no API route does any of this. It needs database access
// and refuses a schema that is not exactly the binary's.
func runToken(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(tokenUsage)
	}
	cmd := args[0]
	spec, ok := tokenFlags[cmd]
	if !ok {
		return fmt.Errorf("unknown command %q\n%s", cmd, tokenUsage)
	}
	fs := flag.NewFlagSet("token "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "deployment configuration file")
	name := fs.String("name", "", "the new service identity's name")
	identity := fs.String("identity", "", "the service identity, an idn identifier")
	roles := fs.String("roles", "", "comma-separated roles: viewer, author, publisher")
	responsible := fs.String("responsible", "", "the responsible human's subject")
	operator := fs.String("operator", "", "the operator's subject, recorded in the act")
	expires := fs.Duration("expires", auth.DefaultExpiry, "token lifetime, at most 2160h")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %v", fs.Args())
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		if !slices.Contains(spec.allowed, f.Name) {
			bad = append(bad, "-"+f.Name)
		}
	})
	for _, n := range spec.required {
		if fs.Lookup(n).Value.String() == "" {
			bad = append(bad, "missing -"+n)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("token %s: %s\n%s", cmd, strings.Join(bad, ", "), tokenUsage)
	}
	cfg, err := readConfig(*path)
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
	if err := migrate.Check(ctx, db, ms); err != nil {
		return err
	}
	store := auth.NewStore(db, cfg.Auth)
	printIssued := func(is auth.Issued, err error) error {
		if err != nil {
			return err
		}
		// The printed token is its only copy, and the transaction has committed.
		if _, err := fmt.Fprintln(stdout, is.Token); err != nil {
			return fmt.Errorf("identity %s: token %s was issued but not printed (%w); rotate it to get a usable one", is.Identity, is.TokenID, err)
		}
		fmt.Fprintf(stderr, "identity %s: token %s expires %s; it is shown once, above\n",
			is.Identity, is.TokenID, is.Expires.UTC().Format(time.RFC3339))
		return nil
	}
	switch cmd {
	case "issue":
		return printIssued(store.Issue(ctx, *name, auth.ParseRoles(*roles), *expires, *responsible, *operator))
	case "rotate":
		return printIssued(store.Rotate(ctx, *identity, auth.ParseRoles(*roles), *expires, *operator))
	case "revoke":
		revoked, err := store.Revoke(ctx, *identity, *operator)
		if err != nil {
			return err
		}
		if len(revoked) == 0 {
			fmt.Fprintf(stderr, "identity %s had no unrevoked token\n", *identity)
		} else {
			fmt.Fprintf(stderr, "revoked %s; identity %s itself is not revoked, and rotate issues it a new token\n", strings.Join(revoked, ", "), *identity)
		}
		return nil
	default: // list
		ls, err := store.List(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "IDENTITY\tNAME\tRESPONSIBLE\tTOKEN\tROLES\tEXPIRES\tSTATE")
		for _, l := range ls {
			rs := make([]string, len(l.Roles))
			for i, r := range l.Roles {
				rs[i] = string(r)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", l.Identity, l.Name, l.Responsible, l.TokenID,
				strings.Join(rs, ","), l.Expires.UTC().Format(time.RFC3339), tokenState(l))
		}
		return w.Flush()
	}
}

func tokenState(l auth.Listed) string {
	switch {
	case l.IdentityRevoked:
		return "identity-revoked"
	case l.TokenRevoked != nil:
		return "revoked"
	case !l.CurrentEpoch:
		return "earlier-epoch"
	case l.Expired:
		return "expired"
	default:
		return "valid"
	}
}

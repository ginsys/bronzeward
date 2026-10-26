package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

func (e *env) contextCmd(args []string) error {
	args, err := e.parse("context", args, -1, nil)
	if err != nil {
		return err
	}
	verb := ""
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	switch verb {
	case "list":
		if _, err := e.parse("context list", args, 0, nil); err != nil {
			return err
		}
		cfg, err := client.LoadConfig()
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(cfg)
		return e.emit(raw, func(w *tabwriter.Writer) {
			fmt.Fprintln(w, "CURRENT\tNAME\tSERVER\tCREDENTIAL")
			for _, x := range cfg.Contexts {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", map[bool]string{true: "*"}[x.Name == cfg.Current], x.Name, x.Server, x.Credential)
			}
		})
	case "use", "remove":
		pos, err := e.parse("context "+verb, args, 1, nil)
		if err != nil {
			return err
		}
		var msg string
		var removed client.Context
		if err := client.UpdateConfig(func(cfg *client.Config) error {
			x := cfg.Find(pos[0])
			if x == nil {
				return fmt.Errorf("context %q does not exist", pos[0])
			}
			msg = "Current context: " + x.Where() + "."
			if verb == "use" {
				cfg.Current = x.Name
				return nil
			}
			removed, msg = *x, fmt.Sprintf("Context %q removed.", x.Name)
			if cfg.Current == x.Name {
				cfg.Current, msg = "", msg+" No context is current now."
			}
			cfg.Contexts = slices.DeleteFunc(cfg.Contexts, func(c client.Context) bool { return c.Name == pos[0] })
			return nil
		}); err != nil {
			return err
		}
		// Its credential-store entry goes with it; a D2 (b) file is left where the operator put it.
		if verb == "remove" {
			if err := client.ClearStored(&removed); err != nil {
				msg += fmt.Sprintf(" %v.", err)
			}
		}
		_, err = fmt.Fprintln(e.out, msg)
		return err
	case "add":
		return e.contextAdd(args)
	}
	return usageError{errors.New("context needs list, use, add or remove")}
}

// contextAdd records a context after its server answered GET /livez (client-contracts.md §5).
// The identity provider's values are given by hand, or with --from read from the client
// configuration file next to the web files, keeping the CLI's client ID (§5 item 4).
func (e *env) contextAdd(args []string) error {
	x := client.Context{}
	var automationFile, from, scopes string
	o := &client.OIDC{}
	pos, err := e.parse("context add", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&x.Server, "server", "", "")
		fs.StringVar(&x.CertificateAuthority, "certificate-authority", "", "")
		fs.StringVar(&x.Credential, "credential", "", "")
		fs.StringVar(&automationFile, "automation-token-file", "", "")
		fs.StringVar(&from, "from", "", "")
		fs.StringVar(&o.Issuer, "issuer", "", "")
		fs.StringVar(&o.ClientID, "client-id", "", "")
		fs.StringVar(&o.Audience, "audience", "", "")
		fs.StringVar(&scopes, "scopes", "", "")
		fs.StringVar(&o.CertificateAuthority, "oidc-certificate-authority", "", "")
		fs.StringVar(&o.TokenFile, "oidc-token-file", "", "")
	})
	if err != nil {
		return err
	}
	x.Name = pos[0]
	if scopes != "" {
		o.Scopes = strings.Split(scopes, ",")
	}
	byHand := o.Issuer != "" || o.ClientID != "" || o.Audience != ""
	oidc := from != "" || byHand || scopes != "" || o.CertificateAuthority != "" || o.TokenFile != ""
	switch {
	case from != "" && byHand:
		return usageError{errors.New("give --from or the identity provider's values by hand, not both")}
	case oidc && x.Credential != "" && x.Credential != "oidc":
		return usageError{fmt.Errorf("--from and the identity provider's flags need --credential oidc, not %q", x.Credential)}
	case from != "" && x.Server == "":
		x.Server = from
	}
	if x.Credential == "" {
		x.Credential = map[bool]string{true: "oidc", false: "automation"}[oidc]
	}
	// Every path is kept absolute, so the context works from any directory.
	for _, p := range []*string{&x.CertificateAuthority, &automationFile, &o.CertificateAuthority, &o.TokenFile} {
		if *p != "" {
			if *p, err = filepath.Abs(*p); err != nil {
				return err
			}
		}
	}
	if automationFile != "" {
		x.Automation = &client.Automation{TokenFile: automationFile}
	}
	if x.Credential == "oidc" {
		x.OIDC = o
	}
	if from != "" {
		cc, err := client.FetchClientConfig(&x, from, e.verboseOut())
		if err != nil {
			return fmt.Errorf("%s: %w", x.Where(), err)
		}
		o.Issuer, o.Audience, o.ClientID = cc.Issuer, cc.Audience, cc.CLIClientID
	}
	if err := x.Check(); err != nil {
		return usageError{err}
	}
	if err := client.Live(&x, e.verboseOut()); err != nil {
		return err
	}
	var current string
	if err := client.UpdateConfig(func(cfg *client.Config) error {
		if cfg.Find(x.Name) != nil {
			return fmt.Errorf("context %q exists: remove it first", x.Name)
		}
		cfg.Contexts = append(cfg.Contexts, x)
		if cfg.Current == "" {
			cfg.Current = x.Name
		}
		current = cfg.Current
		return nil
	}); err != nil {
		return err
	}
	trust := "the system trust store"
	if x.CertificateAuthority != "" {
		trust = "the bundle " + x.CertificateAuthority
	}
	_, err = fmt.Fprintf(e.out, "Context %q added: %s, TLS trust from %s, credential %s. Current context: %s.\n", x.Name, x.Server, trust, x.Credential, current)
	return err
}

// versionCmd prints both versions and the served API revision. The server's is "unknown" while
// it cannot be read, and the command then fails with the reason (client-contracts.md §7 item 5).
func (e *env) versionCmd(args []string) error {
	if _, err := e.parse("version", args, 0, nil); err != nil {
		return err
	}
	raw, where := []byte("null"), ""
	var srv *apitypes.ServerBody
	c, err := e.open()
	if err == nil {
		var r *client.Response
		where = c.Context.Where()
		if r, srv, err = c.Server(); r != nil {
			raw = r.Body
		}
		err = errors.Join(err, c.Save())
	}
	out := fmt.Appendf(nil, `{"client":{"version":%q,"minimumApiRevision":%d},"server":%s}`, clientVersion(), client.MinRevision, raw)
	return errors.Join(err, e.emit(out, func(w *tabwriter.Writer) {
		fmt.Fprintf(w, "Client\tbronzewardctl %s, needs API revision %d or later\n", clientVersion(), client.MinRevision)
		if srv == nil {
			fmt.Fprintln(w, "Server\tunknown")
		} else {
			fmt.Fprintf(w, "Server\t%s, API revision %d, start mode %s\t%s\n", srv.Version, srv.ApiRevision, srv.StartMode, where)
		}
	}))
}

// whoamiCmd shows what GET /identity reports for the credential in use.
func (e *env) whoamiCmd(args []string) (err error) {
	if _, err := e.parse("whoami", args, 0, nil); err != nil {
		return err
	}
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	if _, _, err := c.Server(); err != nil {
		return err
	}
	r, err := c.Get("/identity")
	if err != nil {
		return err
	}
	var id apitypes.IdentityBody
	if err := json.Unmarshal(r.Body, &id); err != nil {
		return fmt.Errorf("%s: GET /identity: %w", c.Context.Where(), err)
	}
	roles := []string{}
	for _, x := range id.Roles {
		roles = append(roles, string(x))
	}
	recovery := map[bool]string{true: "on", false: "off"}[r.Header.Get("Bronzeward-Recovery-Mode") == "true"]
	return e.emit(r.Body, func(w *tabwriter.Writer) {
		fmt.Fprintf(w, "Context\t%s\n", c.Context.Where())
		fmt.Fprintf(w, "Identity\t%s\t%s\n", deref(id.Service), id.Kind)
		fmt.Fprintf(w, "Principal\t%s\n", deref(id.Principal))
		fmt.Fprintf(w, "Roles\t%s\n", map[bool]string{true: "none"}[len(roles) == 0]+strings.Join(roles, ", "))
		fmt.Fprintf(w, "Epoch\t%s\trecovery mode: %s\n", id.Epoch, recovery)
		fmt.Fprintf(w, "Expires\t%s\n", id.ExpiresAt.Format("2006-01-02 15:04:05 MST"))
		// The client's own reading of its token, labelled as such, beside the server's (§9.2 item 5).
		if t := c.TokenClaims(); t != nil {
			fmt.Fprintf(w, "Token (bronzewardctl's reading)\tsubject %s, issuer %s, expires %s\n",
				t.Subject, t.Issuer, t.ExpiresAt().UTC().Format("2006-01-02 15:04:05 MST"))
		}
	})
}

// loginCmd signs in to the selected oidc context and keeps the sign-in (client-contracts.md §9.2).
func (e *env) loginCmd(args []string) error {
	var device bool
	if _, err := e.parse("login", args, 0, func(fs *flag.FlagSet) { fs.BoolVar(&device, "device", false, "") }); err != nil {
		return err
	}
	x, err := e.selected()
	if err != nil {
		return err
	}
	t, kept, err := client.SignIn(x, device, e.err)
	if err != nil {
		return err
	}
	who := "an unreadable token"
	if c := client.ReadClaims(t.AccessToken); c != nil {
		who = fmt.Sprintf("%s at %s (bronzewardctl's reading of the token), until %s", c.Subject, c.Issuer, c.ExpiresAt().UTC().Format("2006-01-02 15:04:05 MST"))
	}
	msg := fmt.Sprintf("Signed in to %s as %s, kept in %s.", x.Where(), who, kept)
	if strings.HasPrefix(kept, "this process's memory") {
		msg += " The sign-in ends with this command: each later command signs in for itself."
	} else if t.RefreshToken == "" {
		msg += " The identity provider issued no refresh token, so sign in again when the access token expires."
	}
	_, err = fmt.Fprintln(e.out, msg)
	return err
}

// logoutCmd removes an OIDC context's stored sign-in, one without an access token too, and revokes
// its refresh token at the provider's revocation endpoint where discovery names one (RFC 7009,
// client-contracts.md §9.7 item 1). An access token already issued stays valid at the server until
// it expires, plus its leeway. A failed revocation still signs out, and exits 1.
func (e *env) logoutCmd(args []string) error {
	if _, err := e.parse("logout", args, 0, nil); err != nil {
		return err
	}
	x, err := e.selected()
	if err != nil {
		return err
	}
	msg, failed := client.SignOut(x)
	if msg == "" {
		return failed
	}
	_, err = fmt.Fprintln(e.out, msg)
	return errors.Join(failed, err)
}

func deref(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}

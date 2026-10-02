// Package config reads the deployment settings held outside the database.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

type Config struct {
	Listen    string    `yaml:"listen"`
	Database  Database  `yaml:"database"`
	Auth      Auth      `yaml:"auth"`
	Execution Execution `yaml:"execution"`
	// Provider is optional: nil when absent, validated when present. Nothing in the server reads
	// it yet; it configures internal/provider. There is no Talos block: each cluster's
	// talosconfig is read from the provider at use (persistence-api §3.3).
	Provider *Provider `yaml:"provider"`
	// Ingestion is required with Provider and refused without it: ingestion is what uses it.
	Ingestion *Ingestion `yaml:"ingestion"`
}

// Ingestion is the staging claims' timers (compilation.md §3.2, §3.5) and the instance name its
// owner identity starts with. The values are open; only their order is required.
type Ingestion struct {
	Instance       string        `yaml:"instance"`
	Heartbeat      time.Duration `yaml:"heartbeat"`
	Lease          time.Duration `yaml:"lease"`
	AbsoluteExpiry time.Duration `yaml:"absoluteExpiry"`
	Sweep          time.Duration `yaml:"sweep"`
}

var instanceName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (i *Ingestion) validate() error {
	if !instanceName.MatchString(i.Instance) {
		return errors.New("config: ingestion.instance is required: 1 to 63 lower-case letters, digits and inner hyphens")
	}
	for _, d := range []struct {
		field string
		v     time.Duration
	}{{"heartbeat", i.Heartbeat}, {"lease", i.Lease}, {"absoluteExpiry", i.AbsoluteExpiry}, {"sweep", i.Sweep}} {
		if d.v <= 0 {
			return fmt.Errorf("config: ingestion.%s must be positive", d.field)
		}
		// The database keeps microseconds, so a finer timer would lose the order checked below.
		if d.v%time.Microsecond != 0 {
			return fmt.Errorf("config: ingestion.%s must be a whole number of microseconds", d.field)
		}
	}
	if i.Heartbeat >= i.Lease {
		return errors.New("config: ingestion.heartbeat must be shorter than ingestion.lease")
	}
	if i.Lease >= i.AbsoluteExpiry {
		return errors.New("config: ingestion.lease must be shorter than ingestion.absoluteExpiry")
	}
	return nil
}

// Provider is the OpenBao the server stores generations and encrypts under (compilation.md §4.1,
// §16.27, §16.28).
type Provider struct {
	Address string `yaml:"address"`
	// PlainHTTPHosts are hosts reached over plain http, by exact name, as for auth.oidc: loopback
	// needs no entry. A plain-http request goes directly to its host, never through a proxy the
	// environment names.
	PlainHTTPHosts []string     `yaml:"plainHTTPHosts"`
	Keys           ProviderKeys `yaml:"keys"`
	// IngestionTokenFile holds ingestion's static token: a regular file of mode 0600 or tighter,
	// read at use, one trailing newline trimmed, never renewed.
	IngestionTokenFile string `yaml:"ingestionTokenFile"`
}

// ProviderKeys names the three Transit keys ingestion uses, each its own.
type ProviderKeys struct {
	Baseline string `yaml:"baseline"`
	Staging  string `yaml:"staging"`
	Digest   string `yaml:"digest"`
}

type Database struct {
	DSN string `yaml:"dsn"`
}

// Auth is persistence-api.md §10.1's block: the OIDC issuer humans' tokens come from, the
// groups that grant each role, and the subjects refused whatever the database says (§10.4).
type Auth struct {
	OIDC           OIDC            `yaml:"oidc"`
	Roles          Roles           `yaml:"roles"`
	DeniedSubjects []DeniedSubject `yaml:"deniedSubjects"`
}

type OIDC struct {
	Issuer           string        `yaml:"issuer"`
	Audience         string        `yaml:"audience"`
	GroupsClaim      string        `yaml:"groupsClaim"`      // default "groups"
	MaxTokenLifetime time.Duration `yaml:"maxTokenLifetime"` // a Go duration string; default 15m
	// PlainHTTPHosts are hosts reached over plain http, by exact name, on a network the
	// deployment protects: a cluster-internal issuer, with TLS terminated in front of it
	// (persistence-api.md §10.1). Loopback needs no entry.
	PlainHTTPHosts []string `yaml:"plainHTTPHosts"`
}

// Execution is execution-recovery.md §5.2's settings.
type Execution struct {
	SettleFloor          time.Duration `yaml:"settleFloor"`          // default and minimum 30s
	MaxTransportDeadline time.Duration `yaml:"maxTransportDeadline"` // required, no default
}

const minSettleFloor = 30 * time.Second

func (e *Execution) validate() error {
	switch {
	case e.SettleFloor == 0:
		e.SettleFloor = minSettleFloor
	case e.SettleFloor < minSettleFloor:
		return fmt.Errorf("config: execution.settleFloor must be at least %s", minSettleFloor)
	}
	if e.MaxTransportDeadline <= 0 {
		return errors.New("config: execution.maxTransportDeadline is required and must be positive")
	}
	return nil
}

// Roles lists, per role, the groups that grant it (design §13.7 item 2).
type Roles struct {
	Viewer        []string `yaml:"viewer"`
	Author        []string `yaml:"author"`
	Publisher     []string `yaml:"publisher"`
	Approver      []string `yaml:"approver"`
	RecoveryAdmin []string `yaml:"recovery-admin"`
}

// DeniedSubject names a human by issuer and subject, or a service identity by its idn identifier.
type DeniedSubject struct {
	Iss      string `yaml:"iss"`
	Sub      string `yaml:"sub"`
	Identity string `yaml:"identity"`
}

// Load parses exactly one YAML document, refusing unknown fields and missing required ones.
func Load(r io.Reader) (Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config: more than one YAML document")
	}
	if c.Listen == "" {
		return Config{}, errors.New("config: listen is required")
	}
	if c.Database.DSN == "" {
		return Config{}, errors.New("config: database.dsn is required")
	}
	if err := c.Auth.validate(); err != nil {
		return Config{}, err
	}
	if err := c.Execution.validate(); err != nil {
		return Config{}, err
	}
	if c.Provider != nil {
		if err := c.Provider.validate(); err != nil {
			return Config{}, err
		}
	}
	switch {
	case c.Provider != nil && c.Ingestion == nil:
		return Config{}, errors.New("config: ingestion is required with provider")
	case c.Provider == nil && c.Ingestion != nil:
		return Config{}, errors.New("config: ingestion needs provider")
	case c.Ingestion != nil:
		if err := c.Ingestion.validate(); err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

func (p *Provider) validate() error {
	if p.Address == "" {
		return errors.New("config: provider.address is required")
	}
	u, err := provider.ParseAddress(p.Address)
	if err != nil {
		// Not quoted, nor is the parser's text: the address may carry a password.
		return fmt.Errorf("config: provider.address: %w", err)
	}
	if err := checkHosts("provider.plainHTTPHosts", p.PlainHTTPHosts); err != nil {
		return err
	}
	if !transport(u, p.PlainHTTPHosts) {
		return errors.New("config: provider.address uses http on a host that is neither loopback nor in provider.plainHTTPHosts; use https or list the host")
	}
	keys := []struct{ field, name string }{
		{"baseline", p.Keys.Baseline}, {"staging", p.Keys.Staging}, {"digest", p.Keys.Digest},
	}
	for i, k := range keys {
		if k.name == "" {
			return fmt.Errorf("config: provider.keys.%s is required", k.field)
		}
		if err := provider.CheckKeyName(k.name); err != nil {
			return fmt.Errorf("config: provider.keys.%s: %w", k.field, err)
		}
		for _, o := range keys[:i] {
			if o.name == k.name {
				return fmt.Errorf("config: provider.keys.%s and provider.keys.%s name the same key; the three must be distinct", o.field, k.field)
			}
		}
	}
	if p.IngestionTokenFile == "" {
		return errors.New("config: provider.ingestionTokenFile is required")
	}
	return nil
}

func (a *Auth) validate() error {
	o := &a.OIDC
	if o.Issuer == "" {
		return errors.New("config: auth.oidc.issuer is required")
	}
	u, err := url.Parse(o.Issuer)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("config: auth.oidc.issuer %q is not an http(s) URL without query or fragment", o.Issuer)
	}
	if err := checkHosts("auth.oidc.plainHTTPHosts", o.PlainHTTPHosts); err != nil {
		return err
	}
	// Discovery and key fetches trust whatever the issuer URL answers. Plain http is for this
	// host and the hosts the admin lists: elsewhere an on-path attacker could serve both.
	if !o.Transport(u) {
		return fmt.Errorf("config: auth.oidc.issuer %q uses http on a host that is neither loopback nor in auth.oidc.plainHTTPHosts; use https or list the host", o.Issuer)
	}
	if o.Audience == "" {
		return errors.New("config: auth.oidc.audience is required")
	}
	if o.GroupsClaim == "" {
		o.GroupsClaim = "groups"
	}
	switch {
	case o.MaxTokenLifetime == 0:
		o.MaxTokenLifetime = 15 * time.Minute
	case o.MaxTokenLifetime < 0:
		return errors.New("config: auth.oidc.maxTokenLifetime must be positive")
	}
	for i, d := range a.DeniedSubjects {
		human := d.Iss != "" || d.Sub != ""
		switch {
		case human && d.Identity != "":
			return fmt.Errorf("config: auth.deniedSubjects[%d] names both a human and a service identity", i)
		case human && (d.Iss == "" || d.Sub == ""):
			return fmt.Errorf("config: auth.deniedSubjects[%d]: a human needs both iss and sub", i)
		case !human && d.Identity == "":
			return fmt.Errorf("config: auth.deniedSubjects[%d] is empty", i)
		case !human:
			if err := id.MustHave(d.Identity, id.Principal); err != nil {
				return fmt.Errorf("config: auth.deniedSubjects[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// hostName is a DNS name: dot-separated labels of letters, digits and inner hyphens, each 1 to 63
// characters (RFC 1123 §2.1), at most 253 in all, so a mistyped entry fails at load rather than
// at discovery.
var hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// Transport reports whether o permits a request to u: https, or plain http to this host or to a
// host in PlainHTTPHosts, by exact name. The issuer URL, the key set URL it advertises and every
// redirect must pass (persistence-api.md §10.1).
func (o OIDC) Transport(u *url.URL) bool { return transport(u, o.PlainHTTPHosts) }

// transport is the plain-http rule auth.oidc and provider share: https, or http to this host or to
// a listed host by exact name.
func transport(u *url.URL, plainHTTPHosts []string) bool {
	switch u.Scheme {
	case "https":
		return true
	case "http":
		h := u.Hostname()
		return loopback(h) || slices.ContainsFunc(plainHTTPHosts, func(p string) bool { return strings.EqualFold(p, h) })
	}
	return false
}

// checkHosts refuses a plainHTTPHosts entry that is not a bare DNS name.
func checkHosts(field string, hosts []string) error {
	for i, h := range hosts {
		if len(h) > 253 || !hostName.MatchString(h) {
			return fmt.Errorf("config: %s[%d] %q is not a bare host name", field, i, h)
		}
	}
	return nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Package config reads the deployment settings held outside the database.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/id"
)

type Config struct {
	Listen   string   `yaml:"listen"`
	Database Database `yaml:"database"`
	Auth     Auth     `yaml:"auth"`
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
	return c, nil
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
	// Discovery and key fetches trust whatever the issuer URL answers. Plain http is for the
	// fixture issuer on this host only: elsewhere an on-path attacker could serve both.
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return fmt.Errorf("config: auth.oidc.issuer %q uses http on a host that is not loopback; use https", o.Issuer)
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

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

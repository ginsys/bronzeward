package client

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"
)

// Config is the contexts file (client-contracts.md §5 item 1): one current context and the
// contexts it may name. It holds no token, key or secret.
type Config struct {
	Current  string    `yaml:"current,omitempty" json:"current,omitempty"`
	Contexts []Context `yaml:"contexts" json:"contexts"`
}

// Context is one installation and how to reach and sign in to it (§5's table). The fields
// after Automation are written by the client from what the server last showed.
type Context struct {
	Name                 string      `yaml:"name" json:"name"`
	Server               string      `yaml:"server" json:"server"`
	CertificateAuthority string      `yaml:"certificateAuthority,omitempty" json:"certificateAuthority,omitempty"`
	Credential           string      `yaml:"credential" json:"credential"`
	Automation           *Automation `yaml:"automation,omitempty" json:"automation,omitempty"`
	OIDC                 *OIDC       `yaml:"oidc,omitempty" json:"oidc,omitempty"`

	LastEpoch string `yaml:"lastEpoch,omitempty" json:"lastEpoch,omitempty"`
	StartMode string `yaml:"startMode,omitempty" json:"startMode,omitempty"`
	// MaxTokenLifetimeSeconds is the maximum GET /server last served, for the lifetime pre-check
	// of client-contracts.md §9.6; 0 before the first read.
	MaxTokenLifetimeSeconds int `yaml:"maxTokenLifetimeSeconds,omitempty" json:"maxTokenLifetimeSeconds,omitempty"`
}

type Automation struct {
	TokenFile string `yaml:"tokenFile,omitempty" json:"tokenFile,omitempty"`
}

// OIDC is the identity provider and the public client registered for this CLI (§9.1). Audience
// is used only for the client's own pre-check (§9.6); TokenFile is the D2 (b) opt-in.
type OIDC struct {
	Issuer               string   `yaml:"issuer" json:"issuer"`
	ClientID             string   `yaml:"clientID" json:"clientID"`
	Scopes               []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	Audience             string   `yaml:"audience,omitempty" json:"audience,omitempty"`
	CertificateAuthority string   `yaml:"certificateAuthority,omitempty" json:"certificateAuthority,omitempty"`
	TokenFile            string   `yaml:"tokenFile,omitempty" json:"tokenFile,omitempty"`
}

// ConfigPath is the contexts file under the user's configuration directory
// ($XDG_CONFIG_HOME/bronzeward/config.yaml on Linux).
func ConfigPath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "bronzeward", "config.yaml"), nil
}

// LoadConfig reads the contexts file; a missing file is an empty configuration.
func LoadConfig() (*Config, error) {
	p, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	} else if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &c, nil
}

// UpdateConfig applies change to the contexts file under an exclusive lock held across the read,
// the change and the write, so concurrent commands never lose each other's updates. The file is
// written whole through an owner-only temporary file and a rename, so no reader sees half of it.
func UpdateConfig(change func(*Config) error) error {
	p, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	if err := change(c); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Find returns the context named name, or nil.
func (c *Config) Find(name string) *Context {
	if i := slices.IndexFunc(c.Contexts, func(x Context) bool { return x.Name == name }); i >= 0 {
		return &c.Contexts[i]
	}
	return nil
}

// Select applies §5 item 1's precedence: the --context flag, then BRONZEWARD_CONTEXT, then the
// current context.
func (c *Config) Select(flag string) (*Context, error) {
	name, from := flag, "--context"
	if name == "" {
		name, from = os.Getenv("BRONZEWARD_CONTEXT"), "BRONZEWARD_CONTEXT"
	}
	if name == "" {
		name, from = c.Current, "the current context"
	}
	if name == "" {
		return nil, errors.New("no context selected: add one with bronzewardctl context add")
	}
	if x := c.Find(name); x != nil {
		return x, nil
	}
	return nil, fmt.Errorf("context %q, named by %s, does not exist", name, from)
}

// Check refuses a context that cannot be used as written.
func (x *Context) Check() error {
	if x.Name == "" || strings.ContainsAny(x.Name, " \t\n/") {
		return fmt.Errorf("context name %q: give a name without spaces or slashes", x.Name)
	}
	if err := endpoint(x.Server, "server"); err != nil {
		return err
	}
	switch {
	case x.Credential == "automation":
		return nil
	case x.Credential != "oidc":
		return fmt.Errorf("credential %q: give automation or oidc", x.Credential)
	case x.OIDC == nil || x.OIDC.ClientID == "":
		return errors.New("an oidc context needs its issuer and client ID")
	}
	return endpoint(x.OIDC.Issuer, "oidc.issuer")
}

// endpoint parses an https URL, or an http one to a loopback address (§6 item 1).
func endpoint(raw, field string) error {
	if u, err := url.Parse(raw); err == nil && (u.RawQuery != "" || u.ForceQuery) {
		return fmt.Errorf("%s %q: give a URL with a host and no query, fragment or user", field, raw)
	}
	return providerEndpoint(raw, field)
}

// providerEndpoint is endpoint for a URL the identity provider names (its token and revocation
// endpoints, a redirect), which may carry a query, kept as it is. An empty fragment counts too:
// url.Parse drops it.
func providerEndpoint(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || strings.Contains(raw, "#") || u.User != nil {
		return fmt.Errorf("%s %q: give a URL with a host and no fragment or user", field, raw)
	}
	if u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s %q: must be https; plain http is accepted only to a loopback address", field, raw)
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

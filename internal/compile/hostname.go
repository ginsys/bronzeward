package compile

// HostnameSource is where a compiled machine's hostname comes from (compilation.md §6.1 item 2).
type HostnameSource string

const (
	HostnameMachineName   HostnameSource = "machine-name"
	HostnameConfiguration HostnameSource = "configuration"
	HostnameNone          HostnameSource = "none"
	HostnameLegacyStable  HostnameSource = "legacy-stable"
)

// Hostname is the compiler's decision about one machine's hostname. Name is set for machine-name only.
type Hostname struct {
	Source HostnameSource
	Name   string
}

// Hostname is the decision taken for this compilation.
func (c Compiled) Hostname() Hostname { return Hostname{} }

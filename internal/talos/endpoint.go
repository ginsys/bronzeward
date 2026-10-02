package talos

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

// defaultPort is the Talos API port an endpoint without one is dialled on.
const defaultPort = 50000

var errEndpoint = errors.New("talos: an endpoint is an IP literal (an IPv6 literal in brackets) with an optional port from 1 to 65535")

// ParseEndpoint checks a machine's Talos endpoint and returns it as host:port, the port 50000
// when absent (persistence-api §3.3). It accepts an IPv4 literal or a bracketed IPv6 literal
// only: a DNS name can resolve to another node between two connections, and a scheme, path, user
// part, zone or whitespace has no meaning here. Its error never quotes the input.
func ParseEndpoint(s string) (string, error) {
	host, port := s, ""
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", errEndpoint
		}
		host, port = s[1:end], s[end+1:]
		if port != "" {
			if port[0] != ':' {
				return "", errEndpoint
			}
			port = port[1:]
			if port == "" {
				return "", errEndpoint
			}
		}
	} else if i := strings.IndexByte(s, ':'); i >= 0 {
		host, port = s[:i], s[i+1:]
		if port == "" {
			return "", errEndpoint
		}
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" || addr.Is4() == strings.HasPrefix(s, "[") {
		return "", errEndpoint
	}
	p := defaultPort
	if port != "" {
		if port[0] < '1' || port[0] > '9' {
			return "", errEndpoint
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errEndpoint
		}
		p = n
	}
	return netip.AddrPortFrom(addr, uint16(p)).String(), nil
}

package discover

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// lookup resolves a DNS name; a test replaces it.
var lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// The two places Discover takes an endpoint from, as a refusal names them.
const (
	sourceFlag = "--endpoint"
	sourceFile = "the talosconfig's first endpoint"
)

// resolveEndpoint is the address to dial for s, an endpoint that is an IP literal or a DNS name, with
// an optional port (defaultPort when none). A name is resolved here, once, and the first address it
// gives that a node can have is the one dialled: the connection is made to that address and to no
// name, so no later lookup can send a request to another node. name is s's host when it was resolved,
// and "" for an IP literal. source says where s came from. The refusals name it and quote neither
// s nor the lookup's own text.
func resolveEndpoint(ctx context.Context, s, source string) (addr, name string, err error) {
	if ep, err := parseEndpoint(s); err == nil {
		return ep, "", nil
	}
	host, port, ok := splitName(s)
	if !ok {
		return "", "", errEndpoint
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return "", "", fmt.Errorf("discover: the DNS name from %s does not resolve", source)
	}
	for _, a := range ips {
		if a = a.Unmap(); a.IsValid() && a.Zone() == "" && !a.IsUnspecified() && !a.IsMulticast() {
			return netip.AddrPortFrom(a, port).String(), host, nil
		}
	}
	return "", "", fmt.Errorf("discover: the DNS name from %s resolves to no address a node can have", source)
}

// splitName is s as a DNS name and a port: a host of letters, digits, hyphens and underscores in
// dot-separated labels of 1 to 63 characters (one trailing dot allowed, 253 characters at most), whose
// last label is not all digits (so that no malformed IPv4 address is looked up), and the port grammar of
// parseEndpoint.
func splitName(s string) (host string, port uint16, ok bool) {
	h, p := s, defaultPort
	if strings.Count(s, ":") > 1 {
		return "", 0, false
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		h, p = s[:i], s[i+1:]
	}
	n, err := strconv.Atoi(p)
	if err != nil || p[0] < '1' || p[0] > '9' || n > 65535 || !validName(h) {
		return "", 0, false
	}
	return h, uint16(n), true
}

func validName(h string) bool {
	trimmed := strings.TrimSuffix(h, ".")
	if trimmed == "" || len(trimmed) > 253 {
		return false
	}
	labels := strings.Split(trimmed, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}

package talos

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// TestServerName: the TLS server name is the address dialled unless the caller gives a name, and a
// name is checked against the node's certificate with the dial still on the address. The trust anchor
// (the talosconfig's CA) is the same in every case.
func TestServerName(t *testing.T) {
	const name = "k8s.example.net"
	loop := []net.IP{net.ParseIP("127.0.0.1")}
	tests := []struct {
		name  string
		dns   []string
		ips   []net.IP
		opts  []DialOption
		reads bool
	}{
		{name: "the certificate lists the name only, the name is given", dns: []string{name}, opts: []DialOption{ServerName(name)}, reads: true},
		{name: "the certificate lists the name only, no name is given", dns: []string{name}},
		{name: "the certificate lists neither the name nor the address", dns: []string{"other.example.net"}, ips: []net.IP{net.ParseIP("127.0.0.9")}, opts: []DialOption{ServerName(name)}},
		{name: "the certificate lists the address only, a name is given", ips: loop, opts: []DialOption{ServerName(name)}},
		{name: "the certificate lists both, a name is given", dns: []string{name}, ips: loop, opts: []DialOption{ServerName(name)}, reads: true},
		{name: "a typed address against a certificate that lists it", ips: loop, reads: true},
		{name: "an empty name adds no check", ips: loop, opts: []DialOption{ServerName("")}, reads: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := talostest.NewPKIFor(t, tc.dns, tc.ips)
			n := talostest.Serve(t, p)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			r, err := Dial(ctx, p.Talosconfig("", "", ""), n.Endpoint, tc.opts...)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			_, err = r.Version(ctx)
			if (err == nil) != tc.reads {
				t.Fatalf("Version err = %v, reads = %v", err, tc.reads)
			}
		})
	}
}

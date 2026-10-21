// A disposable-cluster probe. It reads only the explicitly named synthetic config.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

func emit(kind string, values map[string]any) {
	values["event"], values["utc"] = kind, time.Now().UTC().Format(time.RFC3339Nano)
	if err := json.NewEncoder(os.Stdout).Encode(values); err != nil {
		panic("cannot write probe result")
	}
}

func certMetadata(raw []byte) (*x509.Certificate, map[string]any) {
	b, _ := pem.Decode(raw)
	if b == nil {
		panic("invalid synthetic certificate PEM")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		panic("invalid synthetic certificate DER")
	}
	hash := sha256.Sum256(c.Raw)
	return c, map[string]any{"sha256": hex.EncodeToString(hash[:]), "roles": c.Subject.Organization,
		"not_before": c.NotBefore.UTC(), "not_after": c.NotAfter.UTC()}
}

func main() {
	path := flag.String("config", "", "explicit disposable talosconfig")
	endpoint := flag.String("endpoint", "", "explicit disposable Talos IP:port")
	mode := flag.String("mode", "version", "inspect, version, mint or lifetime")
	roles := flag.String("roles", "os:reader", "minted roles")
	ttl := flag.Duration("ttl", 45*time.Second, "minted lifetime")
	out := flag.String("output", "", "new private JSON file for a minted credential")
	flag.Parse()
	if *path == "" || *endpoint == "" {
		panic("explicit config and endpoint required")
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		panic("cannot read synthetic config")
	}
	cfg, err := config.FromBytes(raw)
	if err != nil || cfg.Contexts[cfg.Context] == nil {
		panic("cannot parse synthetic config")
	}
	crt, err := base64.StdEncoding.DecodeString(cfg.Contexts[cfg.Context].Crt)
	if err != nil {
		panic("invalid synthetic certificate encoding")
	}
	cert, meta := certMetadata(crt)
	emit("credential", meta)
	if *mode == "inspect" {
		return
	}
	var dials atomic.Int64
	open := func() *client.Client {
		c, e := client.New(context.Background(), client.WithConfig(cfg), client.WithEndpoints(*endpoint),
			client.WithGRPCDialOptions(grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, "tcp", address)
			})))
		if e != nil {
			panic("cannot construct synthetic Talos client")
		}
		return c
	}
	c := open()
	defer c.Close() //nolint:errcheck
	version := func(c *client.Client, event string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v, e := c.Version(ctx)
		data := map[string]any{"code": status.Code(e).String(), "tcp_dials": dials.Load()}
		if e == nil {
			var tags []string
			for _, m := range v.Messages {
				tags = append(tags, m.GetVersion().GetTag())
			}
			data["versions"] = tags
		}
		emit(event, data)
		return e == nil
	}
	switch *mode {
	case "version":
		if !version(c, "version") {
			os.Exit(1)
		}
	case "mint":
		if *out == "" || *ttl <= 0 || *ttl > 24*time.Hour {
			panic("new output path and bounded positive TTL required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, e := c.GenerateClientConfiguration(ctx, &machine.GenerateClientConfigurationRequest{
			Roles: strings.Split(*roles, ","), CrtTtl: durationpb.New(*ttl)})
		emit("mint", map[string]any{"code": status.Code(e).String(), "requested_roles": *roles, "requested_seconds": ttl.Seconds()})
		if e != nil {
			os.Exit(1)
		}
		if len(r.Messages) != 1 {
			panic("expected exactly one issuing node")
		}
		m := r.Messages[0]
		_, issued := certMetadata(m.Crt)
		emit("issued", issued)
		f, e := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if e != nil {
			panic("cannot create new private credential file")
		}
		e = json.NewEncoder(f).Encode(map[string]string{"ca": string(m.Ca), "cert": string(m.Crt), "key": string(m.Key)})
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			panic("cannot save private fixture credential")
		}
	case "lifetime":
		wait := time.Until(cert.NotAfter.Add(2 * time.Second))
		if wait < 2*time.Second || wait > 3*time.Minute || !version(c, "before_expiry") {
			panic("lifetime probe requires a valid short certificate")
		}
		before := dials.Load()
		time.Sleep(wait)
		version(c, "held_after_expiry")
		emit("connection", map[string]any{"dials_before": before, "dials_after": dials.Load(), "past_not_after": time.Now().After(cert.NotAfter)})
		fresh := open()
		defer fresh.Close() //nolint:errcheck
		version(fresh, "new_after_expiry")
	default:
		panic(fmt.Sprintf("unknown probe mode %q", *mode))
	}
}

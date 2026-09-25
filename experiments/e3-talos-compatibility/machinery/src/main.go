// Command e3m does, through the Talos Go machinery, what the E3 harness also asks of talosctl as a
// subprocess: generate a configuration for a contract, validate one, and run the PoC RPC set
// against a live node. One source file is built against each pinned machinery version (one
// module per version, each linking this file), so that every matrix cell is measured through both
// implementations.
//
// Phase-0 evidence for the Talos compatibility experiment (ginsys/bronzeward issue 5). Not v1
// tooling.
//
// Exit status: 0 the operation succeeded, 1 it was refused or failed (the reason is printed as
// `error=`), 2 a usage error.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/bundle"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	mtype "github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/validation"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	cfgres "github.com/siderolabs/talos/pkg/machinery/resources/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "info":
		err = info()
	case "gen":
		err = gen(os.Args[2:])
	case "validate":
		err = validate(os.Args[2:])
	case "policy":
		err = policy()
	case "paths":
		err = paths(os.Args[2:])
	case "version", "read", "apply":
		err = rpc(os.Args[1], os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		// A node's refusal of a change it cannot apply in the requested mode carries the
		// configuration diff, which can hold secrets: it is withheld, with a count of its lines.
		kept, withheld := withholdDiff(err.Error())
		fmt.Printf("error=%q withheld_lines=%d\n", kept, withheld)
		os.Exit(1)
	}
}

// withholdDiff returns s up to its first diff line (one starting `diff:`, `Config diff:` or
// `--- `, as the harness's step_quiet recognises talosctl's), and the number of non-empty lines
// from there on.
func withholdDiff(s string) (string, int) {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "diff:") || strings.HasPrefix(l, "Config diff:") || strings.HasPrefix(l, "--- ") {
			withheld := 0
			for _, r := range lines[i:] {
				if r != "" {
					withheld++
				}
			}
			return strings.Join(lines[:i], "\n"), withheld
		}
	}
	return s, 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: e3m info | policy | gen | validate | paths | version | read | apply [flags]")
	os.Exit(2)
}

// info prints the machinery version this binary was built against, and its default Kubernetes
// version.
func info() error {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("no build information")
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/siderolabs/talos/pkg/machinery" {
			fmt.Printf("machinery=%s kubernetes_default=%s go=%s\n", d.Version, constants.DefaultKubernetesVersion, bi.GoVersion)
			return nil
		}
	}
	return errors.New("machinery is not a dependency of this build")
}

// gen mirrors `talosctl gen config <cluster> <endpoint> --with-secrets <f> --talos-version <t>
// --kubernetes-version <k> --install-disk <d> --install-image <i> --with-docs=false
// --with-examples=false`, writing controlplane.yaml and worker.yaml to -out. It goes through
// bundle.NewBundle, as talosctl's GenerateConfigBundle does.
func gen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	contract := fs.String("talos-version", "", "target contract, as talosctl's --talos-version; empty for the machinery's current")
	k8s := fs.String("kubernetes-version", constants.DefaultKubernetesVersion, "Kubernetes version")
	secretsFile := fs.String("with-secrets", "", "secrets bundle file")
	disk := fs.String("install-disk", "/dev/sda", "install disk")
	image := fs.String("install-image", "", "installer image")
	out := fs.String("out", "", "output directory")
	_ = fs.Parse(args)
	if fs.NArg() != 2 || *secretsFile == "" || *out == "" || *image == "" {
		return errors.New("gen needs <cluster> <endpoint>, -with-secrets, -install-image and -out")
	}
	var opts []generate.Option
	var vc *config.VersionContract // nil: the machinery's current contract
	if *contract != "" {
		var err error
		if vc, err = config.ParseContractFromVersion(*contract); err != nil {
			return fmt.Errorf("invalid talos-version: %w", err)
		}
		opts = append(opts, generate.WithVersionContract(vc))
	}
	sb, err := secrets.LoadBundle(*secretsFile)
	if err != nil {
		return fmt.Errorf("failed to load secrets bundle: %w", err)
	}
	if err := validateBundle(sb, vc); err != nil {
		return fmt.Errorf("failed to validate secrets bundle: %w", err)
	}
	opts = append(opts,
		generate.WithSecretsBundle(sb),
		generate.WithInstallDisk(*disk),
		generate.WithInstallImage(*image),
		generate.WithAdditionalSubjectAltNames([]string{}),
		generate.WithDNSDomain("cluster.local"),
		generate.WithClusterDiscovery(true),
	)
	b, err := bundle.NewBundle(bundle.WithInputOptions(&bundle.InputOptions{
		ClusterName: fs.Arg(0),
		Endpoint:    fs.Arg(1),
		KubeVersion: strings.TrimPrefix(*k8s, "v"),
		GenOptions:  opts,
	}))
	if err != nil {
		return err
	}
	for name, t := range map[string]mtype.Type{"controlplane.yaml": mtype.TypeControlPlane, "worker.yaml": mtype.TypeWorker} {
		data, err := b.Serialize(encoder.CommentsDisabled, t)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*out, name), data, 0o600); err != nil {
			return err
		}
	}
	fmt.Println("result=generated")
	return nil
}

// mode is the machinery's validation.RuntimeMode. talosctl validate parses its --mode with a
// type from the talos module's internal/ tree, which a machinery user cannot import; this is the
// same three-valued behaviour for the modes the matrix uses.
type mode string

func (m mode) String() string        { return string(m) }
func (m mode) RequiresInstall() bool { return m == "metal" }
func (m mode) InContainer() bool     { return m == "container" }

// validate mirrors `talosctl validate --config <f> --mode <m> [--strict]`.
func validate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	file := fs.String("config", "", "configuration file")
	m := fs.String("mode", "", "container, metal or cloud")
	strict := fs.Bool("strict", false, "treat warnings as errors")
	_ = fs.Parse(args)
	switch *m {
	case "container", "metal", "cloud":
	default:
		return fmt.Errorf("unknown runtime mode: %q", *m)
	}
	cfg, err := configloader.NewFromFile(*file)
	if err != nil {
		return err
	}
	opts := []validation.Option{validation.WithLocal()}
	if *strict {
		opts = append(opts, validation.WithStrict())
	}
	warnings, err := cfg.Validate(mode(*m), opts...)
	for _, w := range warnings {
		fmt.Printf("warning=%q\n", w)
	}
	if err != nil {
		return err
	}
	fmt.Println("result=valid")
	return nil
}

// digest is the experiment's normalization: the SHA-256 of the configuration with its trailing
// newlines replaced by exactly one, as the harness computes it for talosctl's read-back.
func digest(b []byte) string {
	sum := sha256.Sum256(append(bytes.TrimRight(b, "\n"), '\n'))
	return hex.EncodeToString(sum[:])
}

// rpc runs one PoC RPC against E3M_NODE through E3M_ENDPOINT, with the talosconfig in
// E3M_TALOSCONFIG. The environment, not argv, carries the endpoints and the credentials' path.
func rpc(op string, args []string) error {
	fs := flag.NewFlagSet(op, flag.ExitOnError)
	file := fs.String("file", "", "configuration to apply")
	dryRun := fs.Bool("dry-run", false, "apply: validate on the node and report, without applying")
	timeout := fs.Duration("timeout", 30*time.Second, "deadline for the call")
	_ = fs.Parse(args)
	endpoint, node, tc := os.Getenv("E3M_ENDPOINT"), os.Getenv("E3M_NODE"), os.Getenv("E3M_TALOSCONFIG")
	if endpoint == "" || node == "" || tc == "" {
		return errors.New("E3M_ENDPOINT, E3M_NODE and E3M_TALOSCONFIG must be set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	c, err := client.New(ctx, client.WithConfigFromFile(tc), client.WithEndpoints(endpoint))
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck
	ctx = client.WithNode(ctx, node)
	switch op {
	case "version":
		resp, err := c.Version(ctx)
		if err != nil {
			return err
		}
		for _, m := range resp.GetMessages() {
			fmt.Printf("result=version node_tag=%s\n", m.GetVersion().GetTag())
		}
	case "read":
		mc, err := safe.StateGetByID[*cfgres.MachineConfig](ctx, c.COSI, cfgres.ActiveID)
		if err != nil {
			return err
		}
		b, err := mc.Provider().Bytes()
		if err != nil {
			return err
		}
		fmt.Printf("result=read version=%s digest=%s\n", mc.Metadata().Version(), digest(b))
	case "apply":
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		resp, err := c.ApplyConfiguration(ctx, &machine.ApplyConfigurationRequest{
			Data:   data,
			Mode:   machine.ApplyConfigurationRequest_NO_REBOOT,
			DryRun: *dryRun,
		})
		if err != nil {
			return err
		}
		for _, m := range resp.GetMessages() {
			// The mode details of a dry run carry the configuration diff, which can hold secrets:
			// only its first line is printed.
			details, _, _ := strings.Cut(m.GetModeDetails(), "\n")
			fmt.Printf("result=applied dry_run=%t mode=%s details=%q\n", *dryRun, m.GetMode(), details)
			// The node's warnings, which talosctl also prints.
			for _, w := range m.GetWarnings() {
				fmt.Printf("warning=%q\n", w)
			}
		}
	}
	return nil
}

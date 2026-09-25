package main

import (
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/compatibility"
)

// policy prints the support policy this machinery version encodes, which is one of the matrix's
// four columns: for each Talos target, which Kubernetes versions it accepts
// (KubernetesVersion.SupportedWith) and which host versions may upgrade to it
// (TalosVersion.UpgradeableFrom). A target the machinery predates is reported as it answers.
func policy() error {
	targets := []string{"v1.10.0", "v1.11.0", "v1.12.0", "v1.13.0", "v1.14.0", "v1.15.0"}
	k8s := []string{"1.29.0", "1.30.0", "1.31.0", "1.32.0", "1.33.0", "1.34.0", "1.35.0", "1.36.0", "1.36.2", "1.37.0"}
	for _, t := range targets {
		tv, err := compatibility.ParseTalosVersion(&machine.VersionInfo{Tag: t})
		if err != nil {
			return err
		}
		for _, k := range k8s {
			kv, err := compatibility.ParseKubernetesVersion(k)
			if err != nil {
				return err
			}
			fmt.Printf("kubernetes\t%s\t%s\t%s\n", t, k, verdict(kv.SupportedWith(tv)))
		}
		for _, h := range targets {
			hv, err := compatibility.ParseTalosVersion(&machine.VersionInfo{Tag: h})
			if err != nil {
				return err
			}
			fmt.Printf("upgrade\t%s\tfrom %s\t%s\n", t, h, verdict(tv.UpgradeableFrom(hv)))
		}
	}
	return nil
}

func verdict(err error) string {
	if err != nil {
		return fmt.Sprintf("refused: %s", err)
	}
	return "supported"
}

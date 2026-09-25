package main

import (
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// validateBundle is secrets.Bundle.Validate as machinery v1.14 and later define it: it takes the
// target contract, as talosctl v1.14's gen config passes it. Linked into those modules only.
func validateBundle(sb *secrets.Bundle, vc *config.VersionContract) error { return sb.Validate(vc) }

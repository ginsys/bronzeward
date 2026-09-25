package main

import (
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
)

// validateBundle is secrets.Bundle.Validate as machinery v1.12 and v1.13 define it: it takes no
// contract. Linked into the modules for those versions only.
func validateBundle(sb *secrets.Bundle, _ *config.VersionContract) error { return sb.Validate() }

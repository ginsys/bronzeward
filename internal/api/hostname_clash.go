package api

import (
	"fmt"
	"net/http"

	"github.com/ginsys/bronzeward/internal/compile"
)

// ruleHostname is the rule of the `422 validation-failed` a hostname clash is refused with
// (compilation.md §6.1 item 6, persistence-api.md §9.2).
const ruleHostname = "hostname"

// nodeClaim is one covered machine's Kubernetes node name as the clash check reads it: what the
// compiler derived from the machine's composed hostname (compile.Compiled.NodeName), only for a machine
// whose derived name may be compared, with whether the hostname is filled from the machine's name.
type nodeClaim struct {
	machine string // the identifier
	name    string // the stored name, "" for none
	node    string
	filled  bool
}

// label names the machine in words: by name where it has one, by identifier only where it has none.
func (c nodeClaim) label() string {
	if c.name != "" {
		return c.name
	}
	return c.machine
}

// claimOf is the claim a compiled machine makes, and whether it makes one: a machine whose node name is
// not compared (no hostname, one held by a secret reference, an empty derived name) makes none.
func claimOf(m snapshotMachine, c compile.Compiled) (nodeClaim, bool, error) {
	node, known, err := c.NodeName()
	if err != nil || !known {
		return nodeClaim{}, false, err
	}
	return nodeClaim{machine: m.machine, name: m.name, node: node, filled: c.Hostname().Source == compile.HostnameMachineName}, true, nil
}

// hostnameClash refuses the first pair of machines of one release that would register one Kubernetes
// node name where at least one hostname is filled from a name: two explicit hostnames that were already
// equal are not new and are not refused here. A filled machine is named first, since renaming it helps.
// The text names both machines and prints no configured value: an explicit hostname may hold a secret
// reference, and a filled one is the machine's name.
func hostnameClash(claims []nodeClaim) *refusal {
	for i, a := range claims {
		for _, b := range claims[i+1:] {
			if a.node != b.node || !(a.filled || b.filled) {
				continue
			}
			if !a.filled {
				a, b = b, a
			}
			var detail string
			if b.filled {
				detail = fmt.Sprintf("Machine %s and machine %s would both register the Kubernetes node name %s. "+
					"Rename one of them, or set `registerWithFQDN` on both.", a.label(), b.label(), a.node)
			} else {
				detail = fmt.Sprintf("Machine %s would take the hostname %s from its name, but the hostname configured on machine %s "+
					"gives the same Kubernetes node name. Rename machine %s, or change the hostname in the configuration of machine %s.",
					a.label(), a.name, b.label(), a.label(), b.label())
			}
			return refuse(http.StatusUnprocessableEntity, "validation-failed", detail).
				with("rule", ruleHostname).with("machine", a.machine).with("other", b.machine)
		}
	}
	return nil
}

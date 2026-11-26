Changed
- An import or drift adoption of a control plane configuration now verifies `machine.ca.key` against
  the cluster's stored Talos CA key and stores the typed reference instead of a copy of the key. A
  different key is refused `ca-key-mismatch`; a cluster with no readable stored key answers
  `talos-access-unavailable`, so supply a first key before the first import. A server configured
  without `signerTokenFile` can no longer import any control plane configuration.
  Refs ginsys/bronzeward#1191.

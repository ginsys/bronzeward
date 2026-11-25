Changed
- The specification states that importing a control plane configuration keeps no stored copy of the cluster's Talos CA key: the import stores a typed reference to the signer's key after the signer confirms the imported value matches it, and the compiler gets a written read grant on the signer's path to render the key. Refs ginsys/bronzeward#889.

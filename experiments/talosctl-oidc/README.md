# talosctl-oidc credential research fixture

Work in progress for [#162](https://github.com/ginsys/bronzeward/issues/162).
No local execution result is claimed yet.

The original `rpc.go` probe uses an explicitly supplied disposable talosconfig
and endpoint. It reports certificate metadata and gRPC status without printing
private keys. `mint` writes the returned synthetic credential to a new mode-0600
file; that file is private run state and must never enter retained evidence.
`lifetime` compares calls on one held connection with a new connection after
the certificate's actual expiry, counting TCP dials to detect reconnection.

This fixture is separate from the accepted PoC and its running demo. It does not
change Bronzeward authentication, approval or provider policy. Reproduction,
complete scenario results and limitations will be added after execution.

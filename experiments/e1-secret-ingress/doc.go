// Package e1 is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// The experiment tests design §7.1's requirement that known or operator-marked secrets are
// extracted before any ordinary plaintext persistence, and that an observed configuration is
// never retained in a plaintext draft to be redacted afterwards.
package e1

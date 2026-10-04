// Package compile composes one machine's configuration from resolved inputs with the pinned
// machinery and validates it (compilation.md §6 steps 5, 7 and 8). Resolution (step 4) is
// ingest's; the inputs arrive as ingest.Resolved and the result is a Materialized, plaintext that
// renders nothing.
//
// Choices made here, not in the specification:
//
//   - Composition is the machinery's configpatcher, applied as talosctl's machineconfig patch
//     applies it, one fragment at a time so that a rejection names its input. Whether this path
//     reaches SR's verdicts is §10.1's condition, measured by the machinery gate.
//   - The result is always loaded and re-encoded without comments, also without fragments: SR
//     normalized every output so, and the published configuration is one encoding.
//   - Validation is talosctl validate --strict's: local, warnings as errors. A warning strict mode
//     leaves a warning (a document other than v1alpha1's) is not reported, as talosctl passes it.
//   - The reserved-text output check names documents only. A path inside the output can hold a
//     resolved mapping key, and composition does not yet know which tokens are resolved values;
//     paths with redaction come with provenance (§8).
//   - An Error never carries the machinery's message, which quotes values; how such a message
//     may be shown is §8.3's.
package compile

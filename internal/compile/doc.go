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
//   - An Error from Compose or Validate never carries the machinery's message, which quotes
//     values. Compile's carries it redacted by the trace pass's message of the same step, or a
//     fixed withheld notice (§8.3); the plaintext message is never kept beside the error, so
//     nothing unwraps or reflects to it.
//   - A template must split the real message in one way only: a lazy and a greedy match of the
//     text between quotes must agree, or the message is withheld.
//   - A step whose input holds a boolean or a mapping reference withholds its message whether
//     the template reports it verbatim or redacted: a boolean's stand-in is its value and a
//     mapping's keys are not traced, so nothing marks a quote of either.
//   - Compiled.Redacted expands every alias into a copy of its anchor before rewriting, and
//     refuses when an attributed leaf was not met where attribution found it.
//   - Compiled.Redacted applies the value means to every whole scalar and key equal to a value
//     form of any length, beyond the six-byte floor for contained copies, and refuses a rendering
//     that still holds a form of six bytes or more.
package compile

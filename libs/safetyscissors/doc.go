// Package safetyscissors is the harness layer magus is built on, as a library a Go
// program imports with no magus binary, no magusfile, no workspace and no version
// control: the tool still works, and whoever holds it cannot hurt themselves with it.
//
// This package holds only this doc. Each subpackage has one job:
//
//   - budget: the slot limiter, machine-wide memory and slot claims, and the verdicts
//     an arbiter answers them with.
//   - step: one thing a program is about to do, as data, and the spec that declares
//     each kind of step.
//   - tier: the model tier set a caller declares, the assignment a job records, the
//     reference spawn ceiling, and a price table built from rows the caller supplies.
//   - verdict: what a rule answers about a call, the stricter-of merge, and the
//     judgment a caller reads.
//
// Nothing in the module reads the environment, writes to stdout or stderr, or keeps
// process-wide state; each store opens on a directory its caller names. The module
// imports the standard library, golang.org/x/sync, golang.org/x/sys,
// github.com/bmatcuk/doublestar/v4 and the repository's own libs/* modules, and never
// magus. It ships no agent loop, no model client and no router.
//
// The design is ADR 0050 in magus's docs/decisions.
package safetyscissors

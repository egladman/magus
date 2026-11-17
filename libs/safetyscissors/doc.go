// Package safetyscissors is the harness layer magus is built on, as a library a Go
// program imports with no magus binary, no magusfile, no workspace and no version
// control: the tool still works, and whoever holds it cannot hurt themselves with it.
//
// This package holds only this doc. Each subpackage has one job:
//
//   - budget: the slot limiter, machine-wide memory and slot claims, and the verdicts
//     an arbiter answers them with.
//   - job: the job record a caller declares and the row a store keeps, its write paths,
//     goals, states, results, grants and attempts, the claim grammar, the origin every
//     record stamps, and the faults a record shows on its own. The store opens on a
//     directory the caller names and grades a check goal from a run on one of three
//     bases: witnessed, a command the caller runs that the store watched; attested, a
//     statement the caller's verifier accepts the signature of; and reported, the
//     caller's word, which grades only a goal that accepts it.
//   - sandbox: read, write and exec confinement narrowed to a lease, landlock on Linux and
//     sandbox-exec on macOS, refused on Windows. Confinement is required by default, so a
//     host that cannot confine a child refuses to start it.
//   - session: the append-only record of what invocations did, its fold, and the
//     pruning that bounds it, opened on a directory the caller names.
//   - step: one thing a program is about to do, as data, and the spec that declares
//     each kind of step.
//   - tier: the model tier set a caller declares, the assignment a job records, the
//     reference spawn ceiling, and a price table built from rows the caller supplies.
//   - trail: the append-only activity events and the guard trail's records, opened on a
//     directory the caller names. It exports reading only.
//   - verdict: what a rule answers about a call, the stricter-of merge, and the
//     judgment a caller reads.
//
// Nothing in the module reads the environment, writes to stdout or stderr, or keeps
// process-wide state; each store opens on a directory its caller names. The module
// imports the standard library, golang.org/x/sync, golang.org/x/sys,
// github.com/bmatcuk/doublestar/v4, libs/codec for every JSON value and JSON Lines store,
// and the repository's other libs/* modules, and never magus. It ships no agent loop, no model client and no router.
//
// The design is ADR 0050 in magus's docs/decisions.
package safetyscissors

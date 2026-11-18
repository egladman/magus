// Package safetyscissors is the harness layer magus is built on, as a library a Go
// program imports with no magus binary, no magusfile, no workspace and no version
// control: the tool still works, and whoever holds it cannot hurt themselves with it.
//
// [New] opens the harness on one directory and returns a [*SafetyScissors]: the job
// store and this invocation's session writer laid out under it, a sandbox that confines
// a child to a lease's write paths, and a spawn judged against its parent's model tier.
// A part New was not given is absent, and its accessor names the option that supplies
// it. The subpackages stay importable on their own; a program that needs only verdict
// imports only verdict. Each has one job:
//
//   - budget: the slot limiter, machine-wide memory and slot claims, and their verdicts.
//   - job: the job record a caller declares, the row a store keeps, and the store, which
//     grades a check postcondition from a run that is witnessed, attested or reported.
//   - sandbox: read, write and exec confinement narrowed to a lease, required by default.
//   - session: the append-only record of what invocations did, its fold and its pruning.
//   - step: one thing a program is about to do, as data, and the spec of each kind.
//   - tier: the model tier set, a job's assignment, the spawn ceiling and a price table.
//   - verdict: what a rule answers about a call, and the stricter-of merge.
//
// Nothing in the module reads the environment, writes to stdout or stderr, or keeps
// process-wide state; each store opens on a directory its caller names. The module
// imports the standard library, golang.org/x/sync, golang.org/x/sys,
// github.com/bmatcuk/doublestar/v4, libs/codec for every JSON value and JSON Lines store,
// and the repository's other libs/* modules, and never magus. It ships no agent loop, no model client and no router.
//
// The design is ADR 0050 in magus's docs/decisions.
package safetyscissors

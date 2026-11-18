// Package safetyscissors is the records and refusals around a unit of work, as a Go
// library: who holds it, what was declared, whether it is done and what it cost. A
// program imports it with no magus binary, no magusfile, no workspace and no version
// control.
//
// [New] opens the job store and this invocation's session writer under one directory
// and returns a [*SafetyScissors]. It requires [WithInvocation], [WithActor],
// [WithNotices] and [WithCheckSpec]. The slot limiter, the machine budget, the tier set
// and the sandbox are absent until their options are passed, and each accessor names
// the option that supplies its part. The subpackages stay importable on their own; a
// program that needs only verdict imports only verdict. Each has one job:
//
//   - budget: the slot limiter, machine-wide memory and slot claims, and the price types
//     a caller's [budget.Pricer] fills. The module ships no rates.
//   - job: the row a caller declares, with its [job.Check] and [job.Postcondition]s,
//     the store that keeps rows and attempts and reads each check through the caller's
//     [job.CheckSpec], and grading from a run that is witnessed, attested or reported.
//   - journal: the activity log of calls through the module, the records of one
//     invocation, and in journal/stream the events a program follows.
//   - sandbox: read, write and exec confinement narrowed to a lease, required by default.
//   - session: the append-only record of what invocations did, its fold and its pruning.
//   - step: one thing a program is about to do, as data, and the spec of each kind.
//   - tier: the model tier set a caller declares, a job's assignment and the spawn
//     ceiling.
//   - verdict: what a rule answers about a call, none of it a yes, and the stricter-of
//     merge.
//
// Nothing in the module reads the environment, writes to stdout or stderr, or keeps
// process-wide state; each store opens on a directory its caller names. The module
// imports the standard library, golang.org/x/sync, golang.org/x/sys,
// github.com/bmatcuk/doublestar/v4, libs/codec for every JSON value and JSON Lines store,
// and the repository's other libs/* modules, and never magus.
//
// It ships no driver: no agent loop, no model client, no router and no scheduler. It
// ships no prices and no catalog of models, tiers or rules; the caller declares them.
//
// The design is ADR 0050 in magus's docs/decisions.
package safetyscissors

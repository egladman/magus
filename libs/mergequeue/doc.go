// Package mergequeue is a speculative, partitioned merge queue.
//
// A queue run has three steps, each with the rights it needs and no more:
//
//   - A [Planner] reads the changes carrying merge intent (a [Changes] document, from a
//     [Provider] or from anywhere else), checks each one's approval at its head, finds
//     which changes are stacked on which from their ancestry, drops the ones that
//     conflict with the base branch on their own, and splits the rest into partitions
//     whose affected sets are pairwise disjoint. It writes a [Plan].
//   - A [Validator] executes the changes' code and needs read access only. Per partition
//     it builds candidates base+A, base+A+B, base+A+B+C onto each other, regenerates
//     generated files on each, and runs a gate on up to Depth of them at once. It records
//     one [Verdict] per change the moment that change is decided.
//   - An [Applier] holds the write credential and executes no change's code. It merges
//     each green change through the provider, with the change's own merge method, as soon
//     as every change beneath it in its partition has merged, and stops when the base
//     branch does not carry the tree validation predicted.
//
// The vocabulary: base is the branch the queue merges into, and the base commit is its
// tip when the plan was made. A candidate is a speculative merge commit, built onto
// either the base commit or the candidate beneath it. The tip is the base branch's
// commit at apply, and a head is a change's own commit. A change is stacked on another
// when it carries that one's head and the base does not; the stack base is that head,
// and the change's own delta is measured from it.
//
// The queue imports nothing of its host. It asks the version control system through
// [VCS], the build tool through [BuildFacts], the provider (GitHub and the like) through
// [Provider] and the CI system through [RunReader]; the gate and the regeneration are
// hooks run on each candidate ([CommandGate], [CommandRegenerate]). Every merge, check and push is composed
// here from the VCS's facts, so which merge base a prediction takes, which conflicts are
// the author's and which differences a review need not see are decided once, in Go. The
// client package implements VCS and BuildFacts with magus, and [CommandFacts] asks any
// other build tool through a command line.
//
// Go holds what the invariants are proven over and what needs the machine: admission,
// partitioning, candidate order, the landing checks, version control, processes, files
// and concurrency. Buzz holds what talks to a system outside the repository, the
// provider and the CI system, as a pure function of that system's answers and the record it
// was handed: it supplies facts and performs writes, and decides nothing (see the
// provider package). A fact the queue acts on is re-checked in Go before it is trusted:
// a change record passes [Change.Check], an approval must name a head, a base and a
// merge method, a declared stack parent must match ancestry, and a stale head is
// unproven. A knob is a fact, not a script: what the schedule computes over comes from
// the build tool and the provider, and how it computes is fixed here.
//
// Every document it reads or writes is JSON carrying a "schema" name with a version,
// and everything it reports is JSONL [Event] records.
package mergequeue

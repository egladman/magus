// Package mergequeue is a speculative, partitioned merge queue.
//
// A queue run has three steps, each with the rights it needs and no more:
//
//   - A [Planner] reads the changes carrying merge intent (a [Changes] document, from a
//     [Provider] or from anywhere else), checks each one's approval at its head, drops
//     the ones that conflict with the base branch on their own, and splits the rest into
//     partitions whose affected sets are pairwise disjoint. It writes a [Plan].
//   - A [Validator] executes the changes' code and needs read access only. Per partition
//     it stages base+A, base+A+B, base+A+B+C onto each other, regenerates derived files
//     on each staging commit, and runs a gate on up to Depth of them at once. It records
//     one [Verdict] per change the moment that change is decided.
//   - An [Applier] holds the write credential and executes no change's code. It merges
//     each green change as its own commit through the provider as soon as every change
//     beneath it in its partition has merged, and stops when the base branch does not
//     carry the tree validation predicted.
//
// The vocabulary: base is the branch the queue merges into, and the base commit is its
// tip when the plan was made. A stage is a staging commit, built onto either the base
// commit or the stage beneath it. The tip is the base branch's commit at apply, and a
// head is a change's own commit.
//
// The queue imports nothing of its host. It asks the version control system through
// [StagingRepo] and [MergingRepo], the build tool through [BuildFacts], and the forge
// through [Provider]; the gate and the regeneration are hooks run on each stage
// ([CommandGate], [CommandRegenerate]). The client package implements the first two
// with magus, and [CommandFacts] asks any other build tool through a command line.
//
// Go holds what the invariants are proven over and what needs the machine: admission,
// partitioning, stage order, the landing checks, version control, processes, files and
// concurrency. Buzz holds what talks to a system outside the repository, the forge and
// the CI system, as a pure function of that system's answers and the record it was
// handed: it supplies facts and performs writes, and decides nothing (see the provider
// package). A fact the queue acts on is re-checked in Go before it is trusted: a change
// record passes [Change.Check], an approval must name a head, and a stale head is
// unproven. A knob is a fact, not a script: what the schedule computes over comes from
// the build tool and the forge, and how it computes is fixed here.
//
// Every document it reads or writes is JSON carrying a "schema" name with a version,
// and everything it reports is JSONL [Event] records.
package mergequeue

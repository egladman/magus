// Package mergequeue is a speculative, partitioned merge queue for git repositories.
//
// A queue run has three steps, each with the rights it needs and no more:
//
//   - [Planner] reads the changes carrying merge intent (a [Changes] document, from a
//     [Provider] or from anywhere else), checks each one's approval at its head, drops
//     the ones that conflict with the base branch on their own, and splits the rest into
//     partitions whose affected sets are pairwise disjoint. It writes a [Plan].
//   - [Validation] executes the changes' code and needs read access only. Per partition
//     it stages base+A, base+A+B, base+A+B+C on top of each other, regenerates derived
//     files on each staging commit, and runs a gate command on up to Depth of them at
//     once. It writes one [StageResult] per change the moment that change is decided.
//   - [Landing] holds the write credential and executes no change's code. It lands each
//     green change as its own commit through the provider as soon as every change
//     beneath it in its partition has landed, and stops when the base branch does not
//     carry the tree validation predicted.
//
// The queue knows nothing about the build tool: affected sets arrive in the input or
// from an affected command hook, and validate and regenerate are command hooks run on
// each staging commit. It knows nothing about the forge either: a [Provider] is a Buzz
// script (see the provider package) that lists changes, reports approval at a commit,
// posts a commit status, merges and kicks back.
//
// Every document it reads or writes is JSON carrying a "schema" name with a version,
// and everything it reports is JSONL [Event] records.
package mergequeue

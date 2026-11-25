---
title: "ADR 0059: the internal/job file layout"
order: 59
description: internal/job names each file for one meaning. "Merge" means a job's work reached main and changing a row's fields is an update, misfiled code moves to the file its name says, and whether work reached main is decided by content rather than commit ancestry.
tags: [adr, decision, jobs, layout, naming]
status: accepted
date: 2026-11-24
---

# ADR 0059: the internal/job file layout

## Context

internal/job holds 63 non-test Go files. Two forces split it that far, and neither
follows what the code does.

The 1500-line `file-lines` conformance ceiling cut code where a file grew long, not
where a concept ended, so one idea spans `attempt.go`, `attempts.go` and
`attempt_profile.go`. That ceiling is now advice only, and `func-lines` stays a hard
check, so a fold such as `lazy_workspace.go` into its home is legal again.

"Merge" carries two meanings. `merge.go` holds the field merging behind `job apply`
and `job fork`, a record update, while reaching main lives in `landings.go` under a
word the repository no longer uses. A reader looking for either finds the wrong one
first.

The reached-main test reads commit ancestry. A squash merge or a rebase merge rewrites
a branch's commits, so the test misses work that merged that way.

## Decision

| behavior                                                                                                                                           | enforcement            | State   |
| -------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- | ------- |
| A file in internal/job is named for one meaning, and the pieces of one idea share a file.                                                          | review                 | planned |
| "Merge" means a job's work reached main. `landings.go` becomes `merge.go`, with its identifiers renamed to match.                                  | review                 | planned |
| Changing a row's fields is an update: today's `merge.go` becomes `update.go`, `PutMerge` becomes `PutUpdate` and `ForkMerge` becomes `ForkUpdate`. | review                 | planned |
| The probe store `landing-probes.jsonl` becomes `merge-probes.jsonl`, with no reader for the old name.                                              | the changelog          | planned |
| A job's work reached main when none of its paths differ between its branch and main. The commit-ancestry test is dropped.                          | a test in internal/job | planned |
| Misfiled code moves where its name says: `Workspace` to `workspace.go`, and `catalog.go` to `internal/maintenance`.                                | review                 | planned |
| A helper copied from `libs/safetyscissors/job` is exported there and the internal/job copy is deleted.                                             | review                 | planned |
| Each move is its own green commit.                                                                                                                 | review                 | planned |

The content test survives every way a branch reaches main, because a squash or rebase
rewrites commits and leaves the content alone. The rename of the probe store is a
stored-format break. An old store is left unread, and the changelog records it.

This layout supersedes the earlier plan that moved the field merging to `put.go`.

### Principles checked

- [Delete before you add](../design/principles/delete-before-you-add.md): a dead file
  goes, and the copies of safetyscissors helpers go rather than staying two.
- [A copy can be the right answer](../design/principles/a-copy-can-be-the-right-answer.md):
  the internal/job copies share semantics and bugs with the safetyscissors originals,
  so they merge.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the probe store's rename carries no reader for the old file.
- [A measure is not the goal](../design/principles/a-measure-is-not-the-goal.md): the
  file count is a result, not a target. A fold is made only where one idea spans files.

## Alternatives

Keep the ancestry test and add a squash detector beside it. Two tests for one
question disagree on a rebase merge, and the content test answers every case alone.

Keep "merge" for the field update and name reaching main something else. The field
update is the narrower idea and already has a plain word, update, while "merged" is
the state a pull request reaches in every forge.

Split files to stay under the 1500-line ceiling. The ceiling is advice now, and
splitting by length is what scattered one idea across three files.

## Consequences

A reader finds reaching main in `merge.go` and row updates in `update.go`.

A job whose branch merged by squash or rebase reads as merged.

An old `landing-probes.jsonl` is left unread.

`libs/safetyscissors/job` exports names it kept private, and each becomes part of the
SDK.

## Open questions

The exported names in `libs/safetyscissors/job` are picked when that commit is
written, since each becomes part of the SDK.

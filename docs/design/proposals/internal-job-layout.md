---
title: The internal/job file layout
description: Adopted 2026-11-24, not built. internal/job goes from 63 non-test files to about 40 in eight green commits, files are named for one meaning each, and "reached main" is decided by content rather than commit ancestry.
tags: [jobs, layout, refactor, naming, proposal]
---

# The internal/job file layout

State: adopted 2026-11-24, not built. Nothing below has merged yet.

## The idea

internal/job holds 63 non-test Go files. Two forces split it that far, and neither is
about what the code does:

- The 1500-line `file-lines` conformance ceiling. Code was cut where a file grew long,
  not where a concept ended, so one idea spans `attempt.go`, `attempts.go` and
  `attempt_profile.go`. That ceiling is now advice only; `func-lines` stays a hard
  check. A fold such as `lazy_workspace.go` into its home is legal again.
- Two meanings of "merge". `merge.go` holds the field merging behind `job apply` and
  `job fork` (a record update), while reaching main has its own file under a word the
  repository no longer uses. A reader looking for either finds the wrong one first.

The layout names each file for one meaning and puts misfiled code where its name
says.

## The commits

Eight commits, in this order, each green on its own:

1. Delete `isolation.go`, dead since pull request 613.
2. Fold the pieces of one idea into one file: the `attempt*` files; `model_tier*`;
   `check_line.go` and `lease.go` into `check.go`; `check_semantics.go` and the
   narrowed-test code into `test_scope.go`; `check_grader.go` into `grade.go`;
   `apart.go` and `contention.go` into `overlap.go`; `authority.go` with `pin.go`;
   `store.go` with `phase.go` and `retain.go`; the `render.go` and `row_notes.go`
   folds; `symbol_delta.go` into `verify.go`; `decode.go` with `problems.go`.
3. Move misfiled code. `Workspace` goes to `workspace.go`, and `plan.go` dissolves into
   `apply.go`, `render.go` and `regenerated.go`.
4. Today's `merge.go`, which updates a row, becomes `update.go`, and its functions say
   so: `PutMerge` becomes `PutUpdate`, `ForkMerge` becomes `ForkUpdate`, and the rest
   follow. `DecodePut` moves to `decode.go`.
5. `landings.go` becomes `merge.go`, the file about reaching main, with its
   identifiers renamed to match. Its store, `landing-probes.jsonl`, becomes
   `merge-probes.jsonl`: a stored-format break, recorded in the changelog with no
   reader for the old name. The functions about a branch's own commits
   (`landedSince` and its siblings) become `committed*`, and the bootstrap
   placement becomes `PlacedBootstrap`.
6. Split and rename by role: `lifecycle.go` becomes `exit.go` and `sweep.go`;
   `checkout.go` becomes `binding.go`; `worktree.go`, `provision.go` and `end.go` fold,
   with `binary.go` split out; `graph.go` becomes `diagram.go`.
7. `catalog.go` moves to `internal/maintenance`.
8. Export `samePath`, `globMeta`, the store file names and `errNoSuchJob` from
   `libs/safetyscissors/job`, and delete the copies in internal/job.

Steps 4 and 5 run together with the repository-wide ban on the word for reaching
main, and replace that ban's own rename step
for internal/job. This layout also supersedes the earlier plan that moved the field
merging to `put.go`.

## Adopted rulings

- Whether a job's work reached main is decided by content: none of its paths differ
  between the job's branch and main. A squash merge or a rebase merge rewrites the
  commits and leaves the content, so the content test survives both. The
  commit-ancestry test is dropped.
- A file is named for one meaning. "Merge" means reaching main; changing a row's
  fields is an update.
- Each step is its own green commit, so a reviewer reads one move at a time.

## Still open

- The exported names in step 8. Each is picked when that commit is written, since a
  name in `libs/safetyscissors/job` becomes part of the SDK.
- The final count. About 40 files is the estimate from the map the layout was drawn
  from; the commits decide the number.

## Checked against

- [Delete before you add](../principles/delete-before-you-add.md): step 1 deletes a dead
  file, and step 8 deletes copies rather than keeping two.
- [A copy can be the right answer](../principles/a-copy-can-be-the-right-answer.md):
  the copies in step 8 share semantics and bugs with the safetyscissors originals, so
  they merge.
- [A deliberate break never merges red](../principles/a-deliberate-break-never-merges-red.md):
  the probe store's rename carries no reader for the old file.
- [A measure is not the goal](../principles/a-measure-is-not-the-goal.md): the file
  count is a result, not a target; a fold is made only where one idea spans files.

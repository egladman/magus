---
title: "ADR 0010: jobs share a file by declaration claims"
order: 10
description: Two jobs that change different declarations of one file should run at once. A fork refused for a shared file prints the claim pair that would let both run and writes nothing; a claim must name one declaration; the append-only tables that no claim could split are split; and two jobs that passed with disjoint claims can be settled at integration when a project opts in with merge_low_risk.
tags: [adr, decision, jobs, claims, leases, merge, vcs, guard]
status: accepted (in progress)
date: 2026-10-13
---

# ADR 0010: jobs share a file by declaration claims

Each item below carries its own state: _done_ is in the tree, _planned_ is decided and
queued, _proposed_ is an idea not yet decided, and _not built_ was weighed and declined.

## Context

A job claims a declaration with `<file>#<declaration>` in its write paths. The guard denies
an edit inside a declaration another live lease claims, and `magus job wait` fails a job
whose footprint leaves its claims. A fork is refused with MGS3032 when two live jobs in
different checkouts hold the same driver-covered file and nothing orders them.

Claims were barely used. Measured over the job store and 2,320 transcripts since
2026-09-15:

- 25 of 664 writing jobs, 4 percent, carried a claim.
- 59 forks were refused with MGS3032 on 46 files. The root claimed in 1. It added
  `--depends-on` in 10, dropped the file from the row in 28, and forked the whole file
  later in 18.
- No real `claimed-declaration` denial and no claim violation at wait were recorded, so the
  checks never got in the way. Placement was right for 110 of 111 held-out hunks.
- 88 percent of the shared-file pairs landed on Go, Buzz, Markdown or TypeScript, and all
  four have a diff driver. A missing driver was not the reason.

Two things held claims back. The refusal printed `<declaration>` where the root needed the
name, so serializing was cheaper than working it out. And the files edited most often were
tables: one `const (` block held 74 percent of `types/diagnostic.go`, and one `var` held 56
percent of the guard's rule catalog. A claim cannot split a table, because the whole table
is one declaration.

## Decision

### A refused fork prints the claim pair and writes nothing

_Done._ MGS3032 computes the claims from what the holder has touched so far and from the
symbols the new job's goals name, and says whether the two are disjoint. When they are, the
message is a one-line re-fork. It never writes a claim into either row, because the rows are
the declaration of whoever forks them: the person or the orchestrator re-forks with the
pair.

### A claim names exactly one declaration

_Done._ A claim word that names more than one declaration of its file, such as `ctx` or
`String`, is refused with MGS3031, and the message prints words that name one each. A word
that names none is accepted, because the declaration may not be written yet. The fork says
so, so a typo shows. A claim on a glob stays refused.

### Claims are the default move

_Done._ The `magus-multi-agent` skill recommends a claim when two jobs touch different
declarations of a driver-covered file, and keeps `--depends-on` for two jobs on the same
declaration. [Jobs](../guides/jobs.md#splitting-a-file-by-declaration) documents it.

### The guard grades a whole-file write by declaration

_Done._ A whole-file `Write` in a claimed file used to stand down to the path verdict and
fail open. The guard now diffs the old and new content and grades each changed region against
the live claims. Imports and the rest of the preamble stay exempt, because a missed import
fails the build and the check says so.

### Tables that a claim cannot split are split

_Done_ for the two with the most conflicts. Each diagnostic code in `types/diagnostic.go` is
its own top-level constant, and each rule's documentation in `internal/guard/catalog.go` is
its own `ruleDoc` variable filled into a registry. The Go driver then splits both by entry.

| table                                           | share of its file | State    |
| ----------------------------------------------- | ----------------- | -------- |
| diagnostic codes, `types/diagnostic.go`         | 74 percent        | done     |
| rule documentation, `internal/guard/catalog.go` | 56 percent        | done     |
| members of `var Magus`, `std/magus.go`          | 46 percent        | proposed |
| `var boundaryTypes`, `cmd/magus-utils`          | 56 percent        | proposed |

### Two passed jobs with disjoint claims settle at integration

_Done._ `magus vcs resolve --against <branch>` settles a file that git reports as
conflicting when every one of these holds:

- a project's `merge_low_risk` covers the file, and a diff driver places its declarations;
- each side carries the work of a job that writes the file;
- `magus job wait` recorded every such job as `pass`;
- each job writes the file by declaration claims only, and no claim of one side names a
  declaration a claim of the other side names.

Each side's edits then apply where that side made them, import lines merge as a set, and
anything else that overlaps stays a conflict. The opt-in is the existing `merge_low_risk`
key, so no project gets it unasked. `hack/dev/merge-job-branches.buzz` lists each settle
under the layer it merged and runs every merged job's checks once a file settled this way.
The settle reads the merge base from the VCS, so on Mercurial, Sapling and jj a claimed file
says so and stays conflicted.

| item                                                          | State     |
| ------------------------------------------------------------- | --------- |
| the claim pair in the MGS3032 refusal                         | done      |
| one declaration per claim word, MGS3031                       | done      |
| claims as the default in the multi-agent skill                | done      |
| a whole-file write graded by declaration                      | done      |
| the settle in `vcs resolve --against` behind `merge_low_risk` | done      |
| the fork notes printed by `job fork` and its JSON output      | planned   |
| the same settle in the merge queue                            | not built |
| claim drivers for JSON, YAML and TOML                         | not built |

### Doctrine checked

[Doctrine](../doctrine.md) holds each of these up against this decision.

- [Agents propose, humans dispose](../doctrine.md#agents-propose-humans-dispose): relied on
  for the claim pair. The fork prints it and the person or orchestrator that declares the
  rows decides. The integration settle bends it, since a file lands merged without a person
  reading the hunk. The bend is bounded: a project opts in with `merge_low_risk`, every job
  passed, the claims are disjoint, and the merged jobs' checks run once afterward.
- [Automation you can interrogate](../doctrine.md#automation-you-can-interrogate): relied on.
  The `vcs resolve` line names each claim and job behind a settle, and the reason a file
  did not settle.
- [Misconfiguration is an error, never a warning](../doctrine.md#misconfiguration-is-an-error-never-a-warning):
  relied on. A claim word that names several declarations is refused, not guessed at.
- [Friction that carries meaning](../doctrine.md#friction-that-carries-meaning): relied on.
  MGS3032 used to say `<declaration>`; it now carries the one move that clears it.

## Alternatives

- **Serialize with `--depends-on`.** It is right for two jobs on one declaration and was
  the root's answer to 38 of 59 refusals, which cost wall-clock time. Reopen if the claim
  pair turns out to be wrong often enough that roots stop trusting it.
- **Have the fork write the claims.** The rows are the root's declaration, and a claim a
  tool wrote reads as a boundary the root chose. Reopen if rows gain a field that marks a
  claim as computed.
- **Settle every both-added insert at integration.** It is a general fallback for any
  adjacent edits, with no check that the work was claim-disjoint. It stays opt-in and
  limited to jobs that passed with disjoint claims, so a settle always has evidence.
  Reopen with a measurement of settles the claim check would have refused that a check
  later caught.
- **Claim drivers for JSON, YAML and TOML.** These make up 1.7 percent of shared pairs, a
  nested key cannot be named by a line pattern, and placing one would need a parser where
  git has a regular expression. `go.mod`, lockfiles and `magus.yaml` keep one owner, and
  MGS3031 already refuses a claim on them. Reopen if their share of shared pairs passes
  several percent.
- **Settle in the merge queue as well.** The queue already settles low-risk classes. A
  claim settle needs the jobs' records, which the queue does not read. Reopen if the
  integration step moves into the queue.

## Consequences

- A fork that used to end in a wait or a dropped file can end in a re-fork with a
  printed pair, and the pair is only as good as the holder's touched declarations so far.
- A table split is churn once, and a new entry is its own declaration afterward. Generated
  outputs that read the table, such as the rule pages, regenerate in the same commit.
- Two same-declaration edits still need one owner. The guard denies them and this decision
  does not loosen it.
- A rename that moves a declaration changes the line a claim matches, so the renaming job
  claims both names.
- A project that sets `merge_low_risk` accepts that a claim-disjoint settle lands without a
  person reading the merged hunk. The merged jobs' checks run once after it.

## Open questions

- Whether `std/magus.go` members and the boundary types are worth splitting, once the two
  done splits show their effect on conflict lines.
- This page took 0010 on the branch that wrote it. Take the number again at merge time if
  another decision landed first, and renumber the file, its title, its `order` and the
  links to it.

---
title: "ADR 0048: a local ci repairs a declared writer"
order: 48
description: A ci run always verifies with rw and update withheld, so its verdict is the verdict of the bytes as they stand and its cache keys are the ones GitHub's run mints. A run that carried rw goes one step further when a target fails. If the innermost target that failed itself declares a write and reads rw, ci runs it under rw and verifies again. A run that changed the working tree then fails, names the files and the repaired targets, and is recorded as a failure, because the commit is the person's. A run without rw, a dry run, a tree with no VCS and a sized gate verify only.
tags: [adr, decision, ci, charms, rw, verdicts, cache]
status: proposed
date: 2026-11-13
---

# ADR 0048: a local ci repairs a declared writer

## Context

`default_charms: [rw]` makes `magus run format` write locally, and `magus run ci` then
stripped rw (`CharmsForCI`, `run.go`) and failed on the same format check the person
had just asked magus to fix for them. The failure named its remedy, `format:rw`, and
the person ran it by hand and ran ci again. Three earlier designs tried to remove that
round trip, and a review of a fourth found, with citations, why each one broke:

- **A write pass, then a verify pass.** The push check matches a verdict on `Commit`
  (`internal/guard/push.go:202`), so a pass recorded after a write pass let HEAD
  through while its committed bytes failed GitHub. Most verifier chains reach a
  `format` or `generate`, so they keyed under rw in the write pass: root ci is 721 s
  at p75, and the local entries stopped being shared with verify-only runs.
- **A per-target charm.** It had to be applied in at least seven places: the handler,
  the spell-filtered handler, the settle pass, both audits, outcome recording and
  install keying.
- **Both CLI callers stripped rw before `RunCI`** (`cmd/magus/run.go`,
  `cmd/magus/affected.go`), so a design inside `RunCI` alone was inert.

This one keeps every verify pass byte-for-byte today's run, so none of those apply.

What the code could not say before this change: which target failed inside a
composer. `cache.NeedMember` held no project, `WrapNeedFailure` wrapped only a member
with a persisted ref on the uncached-composer path, and an inline member reported
nothing (`internal/cache/inline.go`). In the projects whose ci is cacheable the
failure was just `<project>:ci`.

Checked against the code at `bf4460606` before building:

- One `ctx.needs(a, b, c)` dispatches every member at once and returns
  `errors.Join("a: …", "b: …")` in argument order (`libs/gopherbuzz/pool.go`,
  `dispatchInner`). One error can name several members, so the lookup returns the
  innermost member on each branch, not one.
- A dirty path holding `[`, `{`, `*` or `?` is a pattern to `diff.HashContent`, and
  doublestar has no quoting (`internal/file/diff/diff.go`, `OutputGlobs`). The
  snapshot reads each path literally instead.
- The lib `generate` targets do not declare their `go.mod` update themselves: the
  `ctx.writesFiles` sits in `spells/golang/gomod.buzz`, and the static read follows
  only same-file helpers (`internal/describe/extract.go`). A lib's go.mod replace drift
  (MGS1016) is therefore not repaired.
- `Step.Revision` is never hashed (`internal/cache/cache.go`, `Step`), so the run after
  the commit replays the entries the last verify pass stored, and records.
- `RunAll` reports a result only for a step that ran; a dependent its upstream stopped
  reports none (`internal/cache/cache.go`, `RunAll`).

## Decision

1. A ci run verifies with rw and `update` withheld. Its verdict is always the verdict
   of that run.
2. A run that carried rw repairs. When a verify pass fails, ci takes the innermost
   failing target on each failure. When that target itself declares a write and reads
   rw, ci runs it under rw once, then verifies again. A stale output, which the verify
   pass has already rewritten, is retried once with no repair.
3. A run that changed a version-controlled file fails with MGS3057, names the files and
   the repaired targets, and is recorded as a failure. The commit is the person's.
4. A target that reads rw and declares no write is never the target of a repair. It
   sees rw only as a member of a declared writer being repaired.
5. A sized ci run, a run without rw, a dry run and a tree with no VCS verify only.
6. The direction of needs most projects follow is a mined precedent. This repository
   refuses a departure in its lint; magus ships the rows, not the refusal.

### 1. What a declared writer is

A target is a declared writer when both hold for the target itself, with no walk of
its chain: it has an in-place update or an output (`p.TargetUpdates`,
`p.TargetOutputs`; `declaresOwnWrite`), and it reads rw (`readsCharm`). Core names no
target. In this repository the 19 `format` targets and the docs, agents and proto
generators qualify; root `generate`, which composes `*-generate` and declares nothing
of its own, does not (see Open questions).

### 2. The loop

`RunCI` keeps the charms its caller carried and verifies under `CharmsForCI`. With rw
carried and a VCS present, it snapshots the working tree's changed paths by content,
then loops: verify, collecting the pass's results; on a pass, stop. Each failed result
is decided:

- a `types.OutputDriftError` on a step not yet retried is retried;
- otherwise `cache.FailedNeeds` names the innermost members the error passed through,
  or the step itself when none; each declared writer not yet repaired is repaired;
- a target that is not a declared writer, or one that fails again after its repair,
  ends the loop with its error.

Each target is repaired at most once and each step retried at most once, so the loop
ends. A repair is `runResolved` of that one target under the carried charms less
`update`, with the cache skipped, since a replay of an in-place update writes nothing.
A failing repair ends the loop with its error. After the loop the snapshot is taken
again; any change joins MGS3057 to the run's error, after a remaining failure's own.
`WithTargetResults` hears the last verify pass only.

### 3. The member a failure names

`cache.NeedMember` carries the project. `WrapNeedFailure` wraps whenever the member is
named, and keeps every member the failure passed through. The inline interceptor wraps
a failed member with its project and target, which covers cross-project members too.
`cache.FailedNeeds` returns the innermost member on each branch of a joined error. The
output descriptor still names the deepest member with a persisted log, so `magus query
output` follows the same hops as before.

### 4. The CLI

`magus run ci`, `magus affected ci` and `magus x ci` pass the carried charms to
`RunCI` and print the stripped set in the header, since that is what every pass
verifies under. A sized `magus affected ci` gets the stripped set and says `this sized
ci run verifies only` when rw was carried.

### 5. The needs-direction precedent

`needs-direction` mines one row per ordered pair of target names from the graph alone:
target nodes and the `depends_on` edges between targets of one project. The cohort is
the projects that define both targets, the projects whose first target reaches the
second through its needs follow, and the rest are departures. A cross-project need
orders projects, not one project's targets, so it is not followed. A pair no project
follows makes no row. The gates are the other families' (`min_cohort` 5, `min_share`
0.8). No target name is written anywhere: the pairs are whatever the workspace's own
projects define.

No rule drops a departure that follows from another. A project whose `format` does
not reach `generate` also departs from `ci` reaching `generate`, and both are listed:
fixing the cause clears them together. `hack/lint/code-follows-mined-precedents.buzz`
judges the family once, with no language, and a sanction names the node and the pair.
It is not a shipped doctor check: the doctor runner has no graph, and the known false
firings (a test fixture, a target that does a different job under a common name, a
need made by a direct call the graph does not see) are a workspace's to sanction.

### State

| item                                                                                    | State                                 |
| --------------------------------------------------------------------------------------- | ------------------------------------- |
| `declaresOwnWrite` and the declared-writer test                                         | done                                  |
| `NeedMember.Project`, wrapping every named member, the inline wrap, `cache.FailedNeeds` | done                                  |
| the repair loop in `RunCI`, its retry and its stops                                     | done                                  |
| the content snapshot of the working tree's changed paths                                | done                                  |
| MGS3057 `UncommittedRepair`                                                             | done                                  |
| the MGS3057 reference page                                                              | done                                  |
| `magus run ci`, `magus affected ci` and `magus x ci` carry the charms                   | done                                  |
| the sized-run line                                                                      | done                                  |
| the guard's `affected` case                                                             | not built (see Alternatives)          |
| the needs-direction precedent family and its lint branch                                | done                                  |
| the sentences that said ci never writes                                                 | done                                  |

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): the person must
  commit, so the repair ends in a returned error, and a run that carried rw but cannot
  repair says which reason stopped it.
- [Verdicts are provenance-blind](../design/principles/verdicts-are-provenance-blind.md):
  the verdict is always a verify pass over the bytes as they stand, under the verify
  charms, wherever the run started.
- [Compute once](../design/principles/compute-once.md): every verify pass keys as
  GitHub's run does, so local entries are shared with every verify-only run.
- [Human in the loop](../design/principles/human-in-the-loop.md): magus writes the
  repair and stops; the commit stays the person's.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the changed behaviour of a local `ci` under rw is recorded in
  `changes/unreleased/local-ci-repairs-a-declared-writer.md`.
- [refusals.md](../design/refusals.md): the mode follows the charm, never where magus
  runs. A CI runner gets verify-only by passing `--no-default-charms`, not by being
  detected.

## Alternatives

### A `ci.write_first` key (attempt 1)

Lost because it was a second way to say what the rw charm already says, and a key read
per workspace cannot differ between a laptop and a runner without detecting the
runner. Reopened by a workspace that needs repair off while keeping `default_charms:
[rw]` for every other target.

### Canonical target names (attempt 2)

Lost because core would name `format` and `generate`, which a workspace may spell
otherwise or not have. The declared-writer test reads only declared data. Reopened by
nothing; it is the constraint this record keeps.

### A three-clause closure at `targetHandler` (attempt 3)

Lost because the handler is one of at least seven sites that read a step's charms
(the spell-filtered handler, the settle pass, both audits, outcome recording and
install keying), and missing one keys or audits a step under the wrong set. Reopened by
a single place that owns a step's charms.

### A write pass before the verify pass

Lost on the four findings in Context: a verdict recorded after a write pass passes
HEAD's committed bytes at the push check, and the verifiers keyed under rw. Reopened by
a push check that matches on the tree rather than the commit, and a measured
key-sharing rate that shows the write pass costs nothing.

### A chain-level writer test

Lost because walking the chain made root `golangci_lint` a writer through
`install_linter`, and `release_index` one although it throws under rw. Reopened by a
chain walk that can tell a member's write from the composer's.

### Scoping rw per member during a repair

Lost because seven sites read the charm set, and scoping it per member means changing
all of them. A repair therefore runs its target with rw's existing reach: a member sees
its caller's charms (`types/charm.go`). Reopened by a case where a writer's member
rewrites files under rw that the person did not expect.

### Exiting zero after a repair

Closed. With no verdict record the push check reads the run log's finished status
(`internal/guard/push.go`), and a zero exit reads as a pass at HEAD, whose committed
bytes failed.

### Counting default rw for the guard's `affected` verb

Not built. The design widened `rewrites` in `internal/guard/lease.go` to `affected`,
but its four callers all sit behind a `verb == "run"` test: `vetsWritePaths` and
`readsOwnProjects` test it first, and `rowJudge.verdict` returns for any other verb
before its two calls. Widening the predicate alone changes no decision. Holding an
`affected ci` to the write paths means judging `affected` in `verdict`, a guard change
of its own. Reopened by a leased checkout holder whose repair writes outside its
lease.

## Consequences

- `magus run ci` with `default_charms: [rw]` can change the tree. Repaired files mix
  with the person's work in progress, and the error names every file that moved.
- `magus run ci --no-default-charms`, the form a CI runner uses, is today's run. So are
  a sized `magus affected ci` and a dry run.
- `magus x ci` applies `default_charms`, as `magus x <target>` already did.
- The snapshot reads the VCS's changed paths. A file inside an untracked directory and
  an ignored file are not seen, and a cache directory the workspace does not ignore
  reads as a change when it appears during the run.
- Drift that is only warned about (another change's drift, or `drift: warn`) still
  passes, as before.
- A lib `generate` whose `go.mod` drifts is not repaired: the write is declared in a
  spell helper the static read does not follow. Its error still names `generate:rw`.
- The CLI's closing list of failed targets reads the invocation journal, not
  `WithTargetResults`, so after a repair it still lists the failures of the first pass.
  They did fail at HEAD.
- The repair is not judged separately by the guard; the ci invocation was.

## Open questions

- **Root `generate` is not repaired** (Eli's call). Its non-rw arm restores the
  regenerated files and throws (`magusfile.buzz`), and it declares no write of its own,
  so it is never a declared writer; its message already says `magus run generate:rw`.
  Either it stays a manual step, or its save and restore are deleted so the engine's
  drift check does the work and the verify pass regenerates in place.
- **rw's reach during a repair** (Eli's call). A repair runs its target with rw's
  existing reach, so every member it composes sees rw too. Root `format`'s repair
  reaches root `generate`, its generators and `replaceWorkspaceModules`; a lib
  `format`'s repair reaches its `generate`. The alternative is scoping rw per member,
  above.
- **The mined family's share threshold.** "3 of 5" is 0.6, under the 0.8 the other
  families use; whether needs-direction keeps 0.8 is undecided.

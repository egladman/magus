---
title: "ADR 0027: shared prerequisites run once in stage zero"
order: 27
description: >-
  A CI shard plan packed units into flat shards, and a computing prerequisite two units shared joined them into one shard, so shared work fanned in and parallelism was lost. The plan now has a stage zero: the computing prerequisites two or more shards would otherwise both compute run once, ahead of every shard, and each shard replays them from a cache archive stage zero exports. A plan with no shared work is unchanged byte for byte. The GitHub half lives only in ci.yaml. The hand-wired docs figures job is folded into the plan as ordinary units.
tags: [adr, decision, ci, sharding, cache, plan]
status: accepted
date: 2026-11-06
---

# ADR 0027: shared prerequisites run once in stage zero

## Context

`magus affected ci --plan` cuts the affected units into shards. The path is
`Magus.Plan` (plan.go), then `ci.Build` (internal/ci/ci.go), then
`forecast.Model.Plan` (internal/ci/forecast/forecast.go). A unit is one target the
anchor asks for through `ctx.needs`, and its closure is every target it needs.

A prerequisite either replays from the cache, because no changed path reaches it, or
computes. `forecast.Model.Repeatable` names the first kind: a shard may repeat one,
since a repeat is a replay. The second kind may not repeat, so the plan never
computes it twice. `weighing.closures` enforces that with a union-find. Two units
whose closures share a computing prerequisite become one item, and one item goes to
one shard.

That is correct and it costs parallelism. Take two 200 s targets that both need a
10 s build, with 1 s of runner setup and one target at a time, the model
`TestStageZeroSuite` in internal/ci/ci_test.go plans. They pack onto one shard of
about 411 s. The build on its own shard, then one shard each, takes about 212 s. The more a change reaches a shared prerequisite (a root library, a code
generator), the more units fan in to one shard, until one runner does the work the
cap allowed eight to share. Eli's ask, 2026-11-06: CI computes each unit once, fans
in under the budget as well as out, and invalidates only readers. "It needs to be an
actual graph."

Pull request runs set `MAGUS_CACHE_REMOTE_WRITE_ENABLED=false`, so today the shards
of one run share nothing. Each shard's gate runs under
`queue gate --sandbox=required`, which scrubs a child's environment to an
allowlist. Commit 715017fac made a saved plan of more than one shard refuse a run
with no `--shard` (MGS3029).

## Decision

| Item                                                                                       | State |
| ------------------------------------------------------------------------------------------ | ----- |
| `forecast.Model.PlanStages` splits a plan into stage zero and stage one                    | done  |
| `ci.Plan.StageZero` and `types.ShardPlan.StageZero`; shard IDs are stage then letters: `0a`, `1a` | done  |
| The `--plan` document's `stage_zero` key, absent when stage zero is empty                  | done  |
| `magus run --stdin --shard 0a` runs one stage-zero shard                                   | done  |
| ci.yaml: a `stage-0` job, its cache archive and sha256, imported by every `stage-1` shard  | done  |
| docs `ci` asks for diagrams-generate and glossary-usage-generate under gha; no `figures` job | done  |
| The symbol index's pack path names no checkout, so a cached index replays anywhere        | done (62823db95, #625) |
| Each project's index op as a planned unit, beside the root's symbol digest                 | not built |
| A measured makespan on this repository's CI, staged against unstaged                       | planned |

### The plan

`PlanStages` finds each computing prerequisite that two or more units' closures hold.
It packs the units again with those counted as repeatable, so their dependents may
split under the existing rule. A shard opens only when it shortens the longest one
by more than setup plus the prerequisites it repeats. A shared prerequisite's own
duration is charged before a split happens, and that is the time stage zero adds to
the critical path.

Each shared prerequisite the split leaves on two or more shards goes to stage zero,
found with the existing `forecast.Model.Repeats`. Stage zero packs with the same
model, so it honors `MAGUS_CI_MAX_SHARDS` and the memory budget like any stage, and
its own shared prerequisites join as they always did. A unit that another unit needs
and that went to stage zero leaves stage one. Stage one is checked against
`forecast.Model.Replaying(stageZero)`, so its shards may repeat what stage zero
computed and nothing else. Stage zero carries no duties: `placeDuties` runs on stage
one alone.

When the split shares nothing, stage one is `forecast.Model.Plan` as before and stage
zero is empty. The ID, digest, duties and JSON of every shard are then unchanged.
`stage_zero` stays out of the plan's job `outputs` for the same reason, and the
workflow reads it from the plan file.

`planReach.needs` now holds each prerequisite's closure as well as each unit's, so
stage zero packs a prerequisite with its own closure.

### Selection

A stage-zero shard is selected on argv by its ID, as a stage-one shard is.
`--shard all` runs stage zero first, then stage one. An absent `--shard` still
refuses any plan of more than one shard, counting both stages. `--n-shards` must
match the size of the named shard's stage.

### The provider

Only `.github/workflows/ci.yaml` knows GitHub. The plan job reads `stage_zero` with
`jq` into `stage_zero_count` and `stage_zero_matrix`. The `stage-zero` job runs each
shard as a `ci` shard runs, then `magus config cache export --to` writes the local
tier, and the job uploads the archive with its sha256 as a run-scoped artifact. Each
`ci` shard downloads every archive, checks each digest, and runs
`magus config cache import` before `run --stdin`. A missing archive, a digest
mismatch or a failed import is a `::warning` that names it, and the shard computes
the work itself. The `ci` job runs after a red stage zero, so every shard reports.
The `ci gate` job fails on it.

### The figures

ci.yaml ran `docs figures` as a job outside the matrix, and docs `ci` skipped
diagrams-generate and glossary-usage-generate under gha. Its comment gave three
reasons. Each is the planner gap above or no longer holds:

1. The figures need the whole-tree symbol index, which no shard built. Both targets
   `ctx.needs` the root's `symbol-digest-generate`, so as units they share a
   computing prerequisite, which stage zero now computes once.
2. They were kept off the docs shard by hand, so site-generate and diagrams-generate
   would not share one 16 GB runner. The packer already checks each shard against
   the memory budget `ci.machine` sets (`MAGUS_CI_MACHINE_MEMORY_MB=16384` in
   ci.yaml) through `forecast.Model.fitMemory`, and stage zero packs the same way.
   No second memory check is added.
3. The index was said to be keyed by the checkout's absolute path.
   `symbols.IndexPath` (internal/symbols/index.go) has hashed the workspace-relative
   project path since 62823db95. On 2026-11-06 a lint run in this checkout replayed
   `.:scip-buzz`, `console:scip` and 15 more index runs recorded in another checkout
   (MGS8005, each naming 0 paths under it). The comment was stale.

So docs `ci` asks for both targets on every CI, and the job and its case in `ci gate`
are deleted. `magus\graph()` still freshens each project's index inside
`symbol-digest-generate` rather than through `ctx.needs`. The plan therefore sees
the digest as one unit, not each project's index op. Making each op a unit means the
root magusfile's `symbol-digest-generate` needs every project's index op. That is
not built here.

### Principles checked

- [A cache is an accelerator, never a dependency](../design/principles/a-cache-is-an-accelerator.md):
  the archive only shortens stage one. Without it each shard computes the same
  units, and the verdict is the same.
- [Every answer has an address](../design/principles/every-answer-has-an-address.md):
  stage one replays stage zero by cache key, so a replay is as honest as the
  declarations; stage zero runs in the same box and `--cache .magus` as stage one.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a failed import
  warns and names the archive. The workflow reads an absent `stage_zero` key as an
  empty stage, which is the key's documented encoding, not a fallback.
- [Provider I/O is Buzz](../design/judgment/provider-io-is-buzz.md): Go names no
  provider. The artifact upload, download and digest are workflow steps.
- [Told, never guessed](../design/judgment/told-never-guessed.md): a shard runs the
  stage-zero shard argv names; nothing reads where it runs to choose a stage.
- [Built for the largest monorepos](../design/principles/built-for-the-largest-monorepos.md):
  the more units a change reaches through one prerequisite, the more the flat plan
  lost.

## Alternatives

### Let every shard compute the shared prerequisite

Counting every prerequisite repeatable splits the units with no stage zero. It lost
because each shard computes the work again, which is what the union-find exists to
prevent, and on a pull request no remote tier carries one shard's result to another.
Reopen it if pull requests write a cache tier every shard of a run reads.

### A general stage graph

`ShardPlan` could hold any number of stages, one per depth of the needs graph. It
lost on scope: one stage of shared work already lets the dependents split, and no
plan of this repository has shown a second level worth a runner's setup. Reopen it
when a measured plan puts a stage-zero shard's own shared work on its critical path.

### Stage zero in the plan's job outputs

`planOutputs` could publish `stage_zero_matrix` and `stage_zero_count`. It lost
because every plan, staged or not, would then render different outputs, which
breaks byte identity, and `TestPlanOutputsCoverWorkflowKeys` would pin a key that an
unstaged plan must still emit. Reopen it if the plan's outputs gain a version.

### The digest as a job output

setup-magus passes its digest as a job output so the artifact does not vouch for
itself. A matrix leg has no output of its own, since the legs overwrite one name. The
digest rides in the artifact beside the archive. It catches a truncated or corrupt
transfer, not a forged one; both ends are jobs of one run with the same token. Reopen
it if stage zero writes the remote tier, which a pull request does not.

## Consequences

- A change whose shards share a computing prerequisite gains a serial stage: one
  runner's setup plus the shared work, ahead of every shard. The split rule charges
  that work before it opens a shard, so a plan stages only when splitting pays by the
  forecast's own measure.
- The JSON plan gains `stage_zero` only on plans that use it. A consumer of the
  matrix that ignores the key still runs every shard, and each computes what stage
  zero would have.
- Stage zero survives `--only`: the filtered shards may still share its work, and a
  stage-zero shard nobody needs costs one runner.
- Inherited and trivial verdicts empty stage zero as they empty the matrix.

- No history holds a shard peak for diagrams-generate, since it ran outside the
  matrix. The first run counts its peak as zero and packs it as if it fits. The plan
  says so in its Fallbacks line ("the history holds no peak memory for ..."). That
  run shows whether its shard fits 16 GB, and the shard's history records the peak
  for every later plan.
- Shards build the whole-tree index inside the queue gate's box. The deleted job
  built it outside the box with `magus graph build`. PR CI shows whether the box
  has every indexer it needs.

## Open questions

- The makespan this buys on this repository is not measured yet. The first pull
  request run with a staged plan answers it, from the history the shards upload.
- Stage one weighs a stage-zero unit at its full duration, though it replays. The
  plan is pessimistic about stage one, and so splits less than it could.

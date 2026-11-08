---
title: A reading budget for mandated jobs
description: Built 2026-11-07. Under a mandate, a settled job whose scoped or full lines pass the review budget is held for the person, and the hold names the split-into-branches command that would split it. magus never splits the job.
tags: [mandate, risk, review, jobs, proposal]
---

# A reading budget for mandated jobs

State: built 2026-11-07 ([ADR 0019](../../decisions/0019-a-mandate-carries-out-decisions-already-made.md)
section 4). This page records why, and what it reuses.

## The idea

Karpathy, at YC AI Startup School (June 2025): "A 1,000-line diff isn't useful to me". The
person verifying is the slow step. Under a mandate, the person first meets a job's work when
they review it, so a job too large to read in one sitting is held while it is still one row,
before it becomes a share of a large pull request.

## Where it applies

`decideHold` (hack/policy/mandate.buzz) holds an unsettled job above the tolerance, and the
tolerance cannot pass `mechanical`. Only paths proven above `mechanical` count, so an
unsettled job inside the tolerance never has lines to count. The budget matters for settled
jobs, which were integrated unattended at any tier. A settled job past the budget is now
held the same way.

## What it reuses

The repository already had a review budget: `hack/dev/split-into-branches.buzz --by symbols`
packs a change into a stack of branches of at most `--budget` changed lines, 400 by default,
from the SmartBear study. That constant had one reader. It moved to
`hack/magusfile/branch-stack.buzz` as `reviewBudget`, which both scripts already share, so
the split and the hold agree on one number.

- Counting: `vcs\diffStat` from the job's checkpoint in its checkout, added plus deleted
  lines, for each path whose `TierEvidence` is `scoped` or `full`. No Go change and no new
  field: `assess` takes the count the way it takes `measure`, so a test injects both.
- Splitting: the hold names `split-into-branches.buzz -- --by symbols --base <checkpoint>`
  for the person to run in the job's checkout. That script already prints a stack and never
  pushes. The hold adds no split logic of its own.
- Setting: `--review-budget` on `hack/dev/issue-mandate.buzz issue`, stored on the mandate
  line. It defaults to `reviewBudget`, `issue` prints the budget in force, and a value above
  it is refused. A mandate line written before the field reads as `reviewBudget`.
- Unknown: when the lines cannot be counted, the job is held and the hold says so.

## The refusal

```text
ledger-report is held for review: settled, with 1180 lines in scoped or full paths, above the mandate's budget of 400; fork the next row
ADR 0019 section 4. To split it, run in /w/ledger-report: `magus buzz hack/dev/split-into-branches.buzz -- --by symbols --base abc123 --budget 400`. Evidence:
internal/ledger/report.go: full (code: ...), 640 lines
...
```

The root moves on to the next row, as it does for every hold. The person splits the row,
integrates it as it is, or drops it. ADR 0019 says a mandate makes none of the decisions,
and how to split a job is one.

## Not built

- No hold outside a mandate. With the person present they read at their own pace, and "the
  enforcement lives in the explanation rather than in a veto" (I think our tools are the
  problem, 2026-08-18).
- No budget on a pull request as a whole.
- No new counter, constant, store, config key or Go field.

## Cleanup this leaves

Three things count changed lines and could share one reader: `split-into-branches.buzz`
(per declaration, through `vcs\regions`), the pull request word budget in
`hack/policy/pull_requests.buzz` (changed lines outside generated files), and this hold
(per file, through `vcs\diffStat`). They count different units for different readers, so
this change leaves them apart. Folding them is its own change.

## Checked against

- Enforce only what is costly and mechanically checkable: lines and tiers are both
  mechanical, and the cost is the person's reading time.
- [Friction that carries meaning](../principles/friction-that-carries-meaning.md): it fires
  only on a large settled job under a mandate, never on every push.
- [A measure is not the goal](../principles/a-measure-is-not-the-goal.md): staying under the
  budget certifies nothing, and a file is only `mechanical` by proof.
- [Delete before you add](../principles/delete-before-you-add.md) and
  [a copy can be the right answer](../principles/a-copy-can-be-the-right-answer.md): the
  constant moved instead of being copied; the counters stay apart for the reason above.
- [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md): the hold
  leaves the split to the person.
- [Refusals](../refusals.md): no override flag. The person's own integration is the
  override.

---
title: A reading budget for mandated jobs
description: Proposal, not built. Under a mandate, a settled job whose lines above mechanical exceed a budget is held for the person, and the hold says how those lines divide by package. magus never splits the job.
tags: [mandate, risk, review, jobs, proposal]
---

# A reading budget for mandated jobs

State: proposed, 2026-11-07. Nothing is built. Eli decides the three questions at the end.

## The idea

Karpathy, at YC AI Startup School (June 2025): "A 1,000-line diff isn't useful to me". The
person verifying is the slow step, so a model should be kept on a leash. Under a mandate
([ADR 0019](../../decisions/0019-a-mandate-carries-out-decisions-already-made.md)), the person
first meets a job's work when they review it. This proposal holds a job whose reviewable lines
pass a budget, so the person meets it while it is still one row and not a share of a large
pull request.

## Where it applies

`decideHold` (hack/policy/mandate.buzz:506) allows any settled job, whatever its tier. An
unsettled job is held when its tier is above the mandate's tolerance, and the tolerance cannot
be set above `mechanical` (`ceiling`, mandate.buzz:22).

Count only lines in paths not proven `trivial` or `mechanical`. Then an unsettled job inside the
tolerance has no countable lines at all, and a budget there would never fire. The budget only
matters for settled jobs, which today go into the branch unattended at any tier. A settled
`full` job of 1,200 lines is merged into the branch while nobody watches, and the person first
meets it in the pull request.

The rule: in `decideHold`, after `settled(row)`, a job whose reviewable lines exceed the
mandate's budget is held, the same way a job above tolerance is.

## Counting

- Paths: the job's `TierEvidence`, from the `assess(id)` call `decideHold` already makes
  (measured from the row's checkpoint). A path counts when its tier is `scoped` or `full`.
- Lines: lines added plus lines removed in those paths, from the checkpoint to the job's head.
  `netLines` (internal/job/verify.go:627) already sums changed lines, for observation. The count
  reuses it, filtered to the countable paths.
- `TierEvidence` has no line count. Proposed: add `Lines int`, filled in where the risk code
  already reads the diff, so Buzz never diffs a second time. The alternative is a numstat call
  from Buzz.
- Not counted: generated output, prose, comment-only edits, and Go files proven equivalent. Each
  is `trivial` or `mechanical` by proof, not by guess. Tests are counted, because a person reads
  them.
- When `assess` fails, the tier is already `full`. When the lines cannot be counted, the job is
  held, and the hold says it could not count.

## Where the budget is set

At issue time, the way the tolerance is: `hack/dev/issue-mandate.buzz issue --review-budget 400`,
stored on the `Mandate` record in `magus/mandate.jsonl`. A repository constant beside `ceiling`,
`budgetCeiling`, caps it, and issuing above the cap is refused. Issuing without the flag is
refused too, and the refusal names the flag: there is no silent default. ADR 0019 rejected a
magus.yaml key for the tolerance (its alternatives section), and the same reasons apply to the
budget.

The number is Eli's. 400 is the usual upper figure for one sitting from code-inspection studies
such as SmartBear's at Cisco. It is input for the choice, not the reason for it.

## The refusal

It has the same shape as today's tolerance hold (mandate.buzz:516):

```text
ledger-report is held for review: settled with 1,180 reviewable lines (scoped or full), above the mandate's budget of 400; fork the next row
ADR 0019 section 4. Evidence:
  internal/ledger/report.go: full, 640 lines (code: ...)
  internal/report/render.go: scoped, 410 lines (code: Go package .../internal/report: ...)
  cmd/app/main.go: scoped, 130 lines (...)
by package: internal/ledger 640 (over 400 alone), internal/report 410 (over 400 alone), cmd/app 130
```

The `by package` line comes from `TierEvidence.Project` and `Packages`. It states facts: each
package's count, and whether that package alone is over the budget. It proposes no split.
`splitAdvice` (internal/job/claim.go:443) is the precedent for a refusal that carries facts, and
this follows its wording.

The root moves on to the next row, as it does for every hold. The held row waits for the person,
who can fork child rows along the packages, integrate it as it is, or drop it. ADR 0019 says a
mandate "makes none" of the decisions, and how to split a job is one. magus never forks, splits
or reruns anything because of this rule.

## Not built

- No hold outside a mandate. With the person present, they read at their own pace, and "the
  enforcement lives in the explanation rather than in a veto" (I think our tools are the
  problem, 2026-08-18). Printing the count in `magus job wait` is open as a separate choice.
- No budget on a pull request as a whole.
- No split engine, no suggestion ranking, no model.

## Checked against

- Enforce only what is costly and mechanically checkable: lines and tiers are both mechanical,
  and the cost is the person's reading time, which is the scarcest input.
- [Friction that carries meaning](../principles/friction-that-carries-meaning.md): it fires only
  on a large settled job under a mandate, never on every push.
- [A measure is not the goal](../principles/a-measure-is-not-the-goal.md): staying under the
  budget certifies nothing. Splitting the work across rows to get under it is the outcome the
  rule wants. The tier cannot be gamed, because a file is only `mechanical` by proof.
- [The loop keeps you able to notice](../judgment/the-loop-keeps-you-able-to-notice.md): the
  person gets the deviation, a held row, not every row.
- [Human in the loop](../principles/human-in-the-loop.md) and
  [agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md): the hold leaves
  the decision with the person.
- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md): it names the
  count, the budget, each path and the next step.
- The blog's counterweight, "Tighten past what the job can bear and you do not get compliance,
  you get workarounds" (2026-08-18): the hold binds only the unattended root. The person
  integrates a held row with the usual command, so there is nothing to work around.
- [Refusals](../refusals.md), the row against an exception form for change risk past 10
  (ADR 0023): likewise, there is no override flag here. The person's own integration is the
  override.
- [Delete before you add](../principles/delete-before-you-add.md): no new store, config key or
  command. The rule adds one `Mandate` field, one constant, one branch in `decideHold`, and
  possibly one `TierEvidence` field.

## Eli decides

1. Hold a settled job over the budget under a mandate: yes or no. The rest depends on it.
2. The number and its ceiling.
3. `Lines` on `TierEvidence` (proposed), or a numstat call from Buzz.

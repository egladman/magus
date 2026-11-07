---
title: Review in tier order
description: Proposal, not built. magus proves a tier for every changed file; the review reads it, so the riskiest files come first and every file says why it has its tier. Nothing folds that does not fold today.
tags: [review, risk, diff, console, buzzed, proposal]
---

# Review in tier order

State: proposed, 2026-11-07. Nothing is built. Eli decides the two questions at the end.

## The idea

In his YC AI Startup School talk (June 2025), Karpathy argues that the person verifies what a
model writes, and that a GUI speeds up the verifying. magus already knows which parts of a change
need it. `magus affected ci` gives every changed file the lowest tier it can prove (`trivial`,
`mechanical`, `scoped`, `full`), with one sentence saying why
([Gate sizing](../../concepts/ci/risk.md)). The review never sees that sentence. `magus diff`, the
console's Diff page and its focus mode put hunks in [reading order](../../concepts/review.md#the-reading-order),
definition before use, and they know nothing about tier.

Proposal: the diff carries each file's tier evidence; the reading order takes tier as the
first key between groups; and every place a file is shown prints the evidence sentence.

## magus computes

- Reuse `AssessChange` (risk.go:55) on the diff's own base and head. It returns a
  `types.TierReport`, and each `types.TierEvidence` (types/risk.go:28) already holds the path,
  class, tier and why. No new classifier.
- Add one field to `types.DiffFile`: `Risk *TierEvidence`, json `risk`. It is nil when nothing
  computed it, and `Diff.Notes` says why. `Diff.Order` keeps the same contract today.
- `OrderHunks` (internal/changeset/order.go:133) sorts connected groups by size and then by
  reach. Tier goes in front of both: a group takes the highest tier of its files, and `full`
  comes before `scoped`, which comes before `mechanical`. Inside a group nothing moves, so a
  definition still comes before its uses. `Diff.SortForReading` (types/diff.go:1217), the
  fallback when there is no symbol index, takes the same first key, so the two orders cannot
  disagree.
- A tier belongs to a file, and a hunk takes its file's tier. No prover works per hunk, and this
  proposal adds none. A per-hunk guess of "comment-only" is the kind of guess risk.md refuses.

## The console, `magus diff` and buzzed display

- Console Diff page: each step header shows the tier and the why sentence, in the same words
  `magus affected ci` prints. `riskChips()` (console/src/apps/diff/order.ts:199) is named for
  risk but draws read state. The tier chip goes into it, so the function draws what its name
  says. Focus mode's progress line adds the tier of the step on screen. The `risk` field joins
  `role` in the wire types (console/src/apps/diff/session.ts).
- `magus diff` text report: the tier and the why sentence after each file in the summary.
  `-o json` carries them as `files[].risk`.
- buzzed: the Changes view (internal/editor/changes.go) runs `magus diff -o json` and draws the
  same header. buzzed imports no magus package, and app/core_test.go:27 keeps it that way. The
  Review buffer cannot show a tier: a model's proposal is not in the working tree yet, and magus
  proves tiers for files, not proposals. buzzed's planned skim, read and read-again levels
  (docs/research/review.md, Order item 6) stay the person's choice. The tier sits beside them,
  and the editor never picks a level for the person. The console and buzzed may each draw this
  their own way; they share the JSON, not the code.

## Nothing new folds

Generated output already folds (`--generated` on the CLI, `showGenerated` in the console), and
settled files already hide (`showSettled`). A `trivial` or `mechanical` file stays open, in its
place at the end of the order. Eli wrote "I read every line before it goes out" (Where AI earns
its keep). The order says where to spend attention first. It does not decide what gets read.

## Checked against

- [Human in the loop](../principles/human-in-the-loop.md) and
  [manual on purpose](../judgment/manual-on-purpose.md): magus orders and explains. Nothing
  marks a hunk read or approves a file because its tier is low.
- [Friction that carries meaning](../principles/friction-that-carries-meaning.md): the time
  spent reading moves to where the tier says the risk is, instead of being spread evenly.
- [Told, never guessed](../judgment/told-never-guessed.md) and
  [no silent defaults](../judgment/no-silent-defaults.md): a file magus cannot bound is `full`,
  and the page says so. An unindexed project still gets no order and the note it gets today.
- [Verdicts are provenance-blind](../principles/verdicts-are-provenance-blind.md): the tier
  ignores who wrote the file, a model or a person.
- [One source, every door](../principles/one-source-every-door.md): one `TierEvidence` is printed
  by `affected ci`, `diff`, the console and buzzed.
- [Delete before you add](../principles/delete-before-you-add.md): one field and one sort key
  are added. The misnamed `riskChips` is reused instead of a second chip row.
- The blog post "I think our tools are the problem" (2026-08-18): "Inform them instead", then
  "let them choose".
- [Refusals](../refusals.md): no row refuses this.
- magus computes and refuses, and it does not act. It never skips, approves or marks anything
  for the reader.

## Eli decides

1. Tier as the first key between groups (proposed), or tier only as a label on today's order.
2. Nothing new folds (proposed), or `trivial` groups fold the way generated ones do.

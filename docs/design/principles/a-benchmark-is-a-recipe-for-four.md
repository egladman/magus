---
title: A benchmark is a recipe for four
order: 40
description: A benchmark measures the benchmark; the gap between it and real work is never zero and is unknown until it is measured, so a result is claimed at the size it was run and a change tuned only to a benchmark needs a witness from real use.
tags: [design, principle, benchmarks, measurement, evidence]
---

# A benchmark is a recipe for four

A recipe that serves four does not serve twenty when every quantity is
multiplied by five. The salt is too much, the pan is too small, the water
boils off at a different rate, and the cooking time was never a quantity at
all. The recipe was right. What it left out was its own tolerance: the range
of kitchens, pans and head counts it was tried in.

A benchmark is that recipe. It was run on one tree, on one machine, on one
set of tasks, at one size, and its number is true there. Between it and the
work people do with the tool there is a gap that is never zero and is unknown
until someone measures it. Three habits follow from treating that gap as real.

- **A result is claimed at the size it was run.** A number from a tree of ten
  thousand files says nothing certain about a million. Run a benchmark at two
  sizes or more and publish how the number moves between them. A figure for a
  size nobody ran is an extrapolation, and the sentence that states it says
  so.
- **A benchmark tuned against is spent.** Once a change is shaped by a
  benchmark's number, that number has stopped measuring the tool and started
  measuring the fit. Keep one benchmark nobody tunes against, or count the
  gain only where real runs show it too.
- **A benchmark win needs a witness from real use.** The run journals already
  record what the cache saved and what an agent spent on real work. A change
  justified by a benchmark alone is labelled as that: better on the
  benchmark, not yet seen in use. When the benchmark keeps improving and the
  journals do not move, stop: the remaining work is on the recipe, not the
  dinner.

This page does not argue against measuring.
[Measured, not asserted](measured-not-asserted.md) still holds, and a claim
with no artifact is still only experience. It sets the weight a measurement
carries: an artificial result is evidence about an artificial workload, and
it earns the word "faster" or "cheaper" for real work only when real work
says the same.

## Enforced by

- [Benchmarks](../../benchmarks.md) publishes every run beside the tree, the
  machine, the date and the versions that produced it, so a number is read
  with its kitchen.
- An unreleased change note that claims a measured gain links the run behind
  it (`hack/lint/measured-claims-cite-their-run.buzz`), so the size and the
  workload of a claim can be checked.

Nothing yet refuses a gain claimed from a benchmark alone, or a figure quoted
at a size that was never run. Both are debts in
[Where this is strained](../strained.md).

## Good

```text
affected on the 10,000-project tree: 410 ms; on the 100,000-project tree: 6.2 s.
Fifteen times the time for ten times the tree. Not run past 100,000.
Journals from three real workspaces, two weeks: median affected 38% lower.
```

Two sizes, the slope between them, where the measuring stopped, and a witness
from real runs.

## Bad

```text
The new index is 4x faster on the benchmark, so affected is 4x faster.
```

One size, one tree, and a claim about every tree. If the index was tuned
until the benchmark said 4x, the benchmark can no longer say whether anything
else improved.

## See also

- [Measured, not asserted](measured-not-asserted.md)
- [A measure is not the goal](a-measure-is-not-the-goal.md)
- [Built for the largest monorepos](built-for-the-largest-monorepos.md)
- [Benchmarks](../../benchmarks.md)

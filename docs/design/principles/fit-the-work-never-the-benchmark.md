---
title: Fit the work, never the benchmark
order: 40
description: Between an artificial result and real work there is a gap that is never zero and is unknown until it is measured; a change tuned to a benchmark fits the benchmark, so a gain counts where real use shows it, at the size it was run.
tags: [design, principle, benchmarks, measurement, evidence]
---

# Fit the work, never the benchmark

A dish written for four does not serve twenty when every quantity is
multiplied by five. The salt is too much, the pan is too small, the water
boils off at a different rate, and the cooking time was never a quantity at
all. The instructions were right. What they left out was their own
tolerance: the range of kitchens, pans and head counts they were tried in.

A benchmark is those instructions. It was run on one tree, on one machine, on one
set of tasks, at one size, and its number is true there. Between that
artificial result and the work people do with the tool there is a gap. It is
never zero, and it is unknown until someone measures it. A benchmark cannot
measure it, because the gap is the part the benchmark left out.

Chasing the number makes the gap worse. Each change shaped by a benchmark's
result fits the tool a little more closely to the benchmark, and nothing says
it fits the work any better. Past some point the number is measuring the fit.
The work is still what the tool is for, so three habits follow.

- **A benchmark win needs a witness from real use.** The run journals already
  record what the cache saved and what an agent spent on real work. A change
  justified by a benchmark alone is labeled as that: better on the
  benchmark, not yet seen in use. When the benchmark keeps improving and the
  journals do not move, stop: the remaining work is on the instructions, not
  the dinner.
- **A benchmark tuned against is spent.** Once a change is shaped by a
  benchmark's number, that number has stopped measuring the tool. Keep one
  benchmark nobody tunes against, or count the gain only where real runs show
  it too.
- **A result is claimed at the size it was run.** A number from a tree of ten
  thousand files says nothing certain about a million. Run a benchmark at two
  sizes or more and publish how the number moves between them. A figure for a
  size nobody ran is an extrapolation, and the sentence that states it says
  so.

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

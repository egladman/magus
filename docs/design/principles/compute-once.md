---
title: Compute once, reuse everywhere
order: 38
description: A result is computed once, by the stage that owns it; every later stage reads that result, and recomputes only on a real cache miss.
tags: [design, principle, ci, cache, performance]
---

# Compute once, reuse everywhere

A pipeline is a chain of readers. CI validates a tree, a later job ships it,
another renders what it measured. Each of them could compute the result again
from source, and none of them should. The stage that owns a result computes
it once. Every stage after it reads that result: a cache hit, or an artifact
the owner uploaded.

Recomputing looks safe and is not. It spends the work twice, and at the scale
of [the largest monorepos](built-for-the-largest-monorepos.md) twice is what
a budget cannot afford. Worse, the second run answers a different question. A
flaky test that passed in CI can fail after the merge, so the shipped tree is
judged by a run that never gated it.

The rule is sharper than
[A cache is an accelerator](a-cache-is-an-accelerator.md), and the two do not
conflict. A cache is optional: a miss computes, and the answer is the same. A
result a later stage is declared to read is an input: when it is missing, the
reader fails and names the stage that owed it. Falling back to computing it
would hide a broken producer behind a slower success
([No silent defaults](../judgment/no-silent-defaults.md)).

## Enforced by

- CI's stage zero runs a prerequisite two shards share once, ahead of every
  shard, and each shard replays it from the archive stage zero exports
  ([ADR 0027](../../decisions/0027-shared-prerequisites-run-once-in-stage-zero.md)).
- Units that share a prerequisite go to one shard, and a prerequisite the
  change does not reach replays from the cache
  ([CI](../../concepts/ci.md)).
- CD's site job renders the coverage badges from the profiles CI's main run
  uploaded for its commit. `coverage-render` needs no suite, and a missing
  artifact fails the job naming the CI run (`.github/workflows/cd.yaml`).
- Nothing yet refuses a workflow step that reruns a target CI already ran for
  the same commit. That is a debt.

## Good

```sh
# CI, on main: each suite replays the entry a shard just stored
magus run coverage-measure .
# CD: draws the badges from the profiles that run uploaded
magus run coverage-render .
```

The suites run once per tree, in the run that gates it, and CD reads their
profiles.

## Bad

```sh
# CD, after the merge CI already validated
magus run test console
magus run coverage-render .   # needed every Go suite and buzz-test
```

CD reran every Go module's race suite and the Buzz tests after the merge,
only to draw three badges. It doubled the work, took a 60-minute timeout, and
measured a run that gated nothing.

## See also

- [ADR 0027: shared prerequisites run once in stage zero](../../decisions/0027-shared-prerequisites-run-once-in-stage-zero.md)
- [CI](../../concepts/ci.md)
- [A cache is an accelerator, never a dependency](a-cache-is-an-accelerator.md)
- [Built for the largest monorepos](built-for-the-largest-monorepos.md)

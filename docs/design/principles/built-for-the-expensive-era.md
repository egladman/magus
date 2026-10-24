---
title: Built for the expensive era
order: 10
description: Move what a model would have to work out into deterministic, checked answers, so a smaller model can do the work.
tags: [design, principle, agents, cost]
---

# Built for the expensive era

Model time is subsidized today, the way early ride-hailing was: prices are
set to win adoption, not to cover cost. The pattern that follows is known, not
predicted. Once adoption is won, prices rise and each token buys less;
enterprise contracts already show it. magus is built for that era, not this
one.

The answer is to move what a model would otherwise have to work out (the
graph, declared outputs, what a change can break, which conventions hold) into
deterministic, checked answers. A smaller model can then do work that today
takes a frontier one. A feature that only works with a frontier model is not
finished. This prevents a tool that looks capable only while someone else
pays for the reasoning.

## Enforced by

- The model-tier lens: the economy tier's pass rate should rise over time, read
  beside the null-control rows in [Benchmarks](../../benchmarks.md), which are the
  false-pass check: a control that changes nothing must fail.
- The static tier table in [ADR 0009](../../decisions/0009-cost-profiles-for-agent-sessions.md):
  a person picks the model for a tier, and nothing routes per call.

## Good

One deterministic answer replaces a model reading files to rediscover it:

```sh
magus describe file docs/MAGUS.md
magus refs decodeFile --occurrences
```

## Bad

```text
Read every magusfile under libs/ and tell me which targets write docs/MAGUS.md.
```

The model spends tokens deriving a fact the graph already holds, and the
answer is a guess a cheaper model gets wrong.

## See also

- [Determinism at chosen points](../judgment/determinism-at-chosen-points.md)
- [Optimize the loop that verifies, never the loop that generates](optimize-the-loop-that-verifies.md)

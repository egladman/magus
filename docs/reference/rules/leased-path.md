---
title: "leased-path: a write into paths a running lease owns, by a caller that names no lease"
description: "An advisory: it explains, and blocks nothing, on a write into paths a running lease owns, by a caller that names no lease."
tags: [guard, rules, leased-path, advise]
---

# leased-path

An advisory: it explains, and blocks nothing, on a write into paths a running lease owns, by a caller that names no lease.

## What it catches

A write into paths a running lease owns, by a caller that names no lease.

## Why

The writer is either that lease, not saying so, or a second agent about to collide with it; magus cannot tell which, so it advises rather than refuses. It speaks once per session per lease. Every write used to repeat it: 8,419 servings in one audit, 52% of every advisory the guard served, for a fact the writer had after the first.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [leased-path]: ...
```

`magus describe rule leased-path` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

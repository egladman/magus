---
title: "hook-wiring: a write to the host wiring that decides whether these rules run at all"
description: "An advisory: it explains, and blocks nothing, on a write to the host wiring that decides whether these rules run at all."
tags: [guard, rules, hook-wiring, advise]
---

# hook-wiring

An advisory: it explains, and blocks nothing, on a write to the host wiring that decides whether these rules run at all.

## What it catches

A write to the host wiring that decides whether these rules run at all.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [hook-wiring]: ...
```

`magus describe rule hook-wiring` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

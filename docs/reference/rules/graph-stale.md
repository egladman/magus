---
title: "graph-stale: a graph read while the index is older than the sources it describes"
description: "An advisory: it explains, and blocks nothing, on a graph read while the index is older than the sources it describes."
tags: [guard, rules, graph-stale, advise]
---

# graph-stale

An advisory: it explains, and blocks nothing, on a graph read while the index is older than the sources it describes.

## What it catches

A graph read while the index is older than the sources it describes.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [graph-stale]: ...
```

`magus describe rule graph-stale` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

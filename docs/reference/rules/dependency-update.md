---
title: "dependency-update: a raw dependency update outside a target's update charm"
description: "An advisory: it explains, and blocks nothing, on a raw dependency update outside a target's update charm."
tags: [guard, rules, dependency-update, advise]
---

# dependency-update

An advisory: it explains, and blocks nothing, on a raw dependency update outside a target's update charm.

## What it catches

A raw dependency update outside a target's update charm.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [dependency-update]: ...
```

`magus describe rule dependency-update` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

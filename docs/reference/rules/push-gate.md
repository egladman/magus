---
title: "push-gate: a push the run log does not prove ungated, which names the gate and lets it through"
description: "An advisory: it explains, and blocks nothing, on a push the run log does not prove ungated, which names the gate and lets it through."
tags: [guard, rules, push-gate, advise]
---

# push-gate

An advisory: it explains, and blocks nothing, on a push the run log does not prove ungated, which names the gate and lets it through.

## What it catches

A push the run log does not prove ungated, which names the gate and lets it through.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [push-gate]: ...
```

`magus describe rule push-gate` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

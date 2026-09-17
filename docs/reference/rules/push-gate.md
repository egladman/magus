---
title: "push-gate: a push with no gate run since the last change"
description: "An advisory: it explains, and blocks nothing, on a push with no gate run since the last change."
tags: [guard, rules, push-gate, advise]
---

# push-gate

An advisory: it explains, and blocks nothing, on a push with no gate run since the last change.

## What it catches

A push with no gate run since the last change.

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

---
title: "unleased-write: a write magus cannot attribute while a fleet is running"
description: "An advisory: it explains, and blocks nothing, on a write magus cannot attribute while a fleet is running."
tags: [guard, rules, unleased-write, advise]
---

# unleased-write

An advisory: it explains, and blocks nothing, on a write magus cannot attribute while a fleet is running.

## What it catches

A write magus cannot attribute while a fleet is running.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [unleased-write]: ...
```

`magus describe rule unleased-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

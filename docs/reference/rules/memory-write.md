---
title: "memory-write: a write to a memory file, where the memory surface is the way in"
description: "An advisory: it explains, and blocks nothing, on a write to a memory file, where the memory surface is the way in."
tags: [guard, rules, memory-write, advise]
---

# memory-write

An advisory: it explains, and blocks nothing, on a write to a memory file, where the memory surface is the way in.

## What it catches

A write to a memory file, where the memory surface is the way in.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [memory-write]: ...
```

`magus describe rule memory-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

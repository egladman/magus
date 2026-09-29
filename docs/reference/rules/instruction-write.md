---
title: "instruction-write: a write to a cross-host instruction file, which every session loads whole"
description: "An advisory: it explains, and blocks nothing, on a write to a cross-host instruction file, which every session loads whole."
tags: [guard, rules, instruction-write, advise]
---

# instruction-write

An advisory: it explains, and blocks nothing, on a write to a cross-host instruction file, which every session loads whole.

## What it catches

A write to a cross-host instruction file, which every session loads whole.

## Why

A cross-host instruction file is read in full at the start of every session on every host, so a sentence there costs context forever. A rule the guard already refuses or doctor already reports is restated context: delete it the moment the tool starts saying it.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [instruction-write]: ...
```

`magus describe rule instruction-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

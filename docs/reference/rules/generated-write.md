---
title: "generated-write: a hand edit to a declared output, which the next run overwrites"
description: "An advisory: it explains, and blocks nothing, on a hand edit to a declared output, which the next run overwrites."
tags: [guard, rules, generated-write, advise]
---

# generated-write

An advisory: it explains, and blocks nothing, on a hand edit to a declared output, which the next run overwrites.

## What it catches

A hand edit to a declared output, which the next run overwrites.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [generated-write]: ...
```

`magus describe rule generated-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

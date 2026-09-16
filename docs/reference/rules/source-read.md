---
title: "source-read: an unbounded source read the symbol index has already answered"
description: "An advisory: it explains, and blocks nothing, on an unbounded source read the symbol index has already answered."
tags: [guard, rules, source-read, advise]
---

# source-read

An advisory: it explains, and blocks nothing, on an unbounded source read the symbol index has already answered.

## What it catches

An unbounded source read the symbol index has already answered.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [source-read]: ...
```

`magus describe rule source-read` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

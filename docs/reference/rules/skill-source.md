---
title: "skill-source: a write to an installed skill copy rather than to its source"
description: "An advisory: it explains, and blocks nothing, on a write to an installed skill copy rather than to its source."
tags: [guard, rules, skill-source, advise]
---

# skill-source

An advisory: it explains, and blocks nothing, on a write to an installed skill copy rather than to its source.

## What it catches

A write to an installed skill copy rather than to its source.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [skill-source]: ...
```

`magus describe rule skill-source` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

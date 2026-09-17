---
title: "installed-skill: a write to an installed skill copy, which re-installing discards"
description: "An advisory: it explains, and blocks nothing, on a write to an installed skill copy, which re-installing discards."
tags: [guard, rules, installed-skill, advise]
---

# installed-skill

An advisory: it explains, and blocks nothing, on a write to an installed skill copy, which re-installing discards.

## What it catches

A write to an installed skill copy, which re-installing discards.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [installed-skill]: ...
```

`magus describe rule installed-skill` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

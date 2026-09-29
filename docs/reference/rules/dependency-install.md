---
title: "dependency-install: a raw package install that the cached install target already runs"
description: "An advisory: it explains, and blocks nothing, on a raw package install that the cached install target already runs."
tags: [guard, rules, dependency-install, advise]
---

# dependency-install

An advisory: it explains, and blocks nothing, on a raw package install that the cached install target already runs.

## What it catches

A raw package install that the cached install target already runs.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [dependency-install]: ...
```

`magus describe rule dependency-install` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

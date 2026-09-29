---
title: "lease-state: a leased write while its row reports a diverged base, a re-entered path, or a bad pattern"
description: "An advisory: it explains, and blocks nothing, on a leased write while its row reports a diverged base, a re-entered path, or a bad pattern."
tags: [guard, rules, lease-state, advise]
---

# lease-state

An advisory: it explains, and blocks nothing, on a leased write while its row reports a diverged base, a re-entered path, or a bad pattern.

## What it catches

A leased write while its row reports a diverged base, a re-entered path, or a bad pattern.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [lease-state]: ...
```

`magus describe rule lease-state` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

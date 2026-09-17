---
title: "lease-terminal: a call naming a lease whose row has already finished"
description: "An advisory: it explains, and blocks nothing, on a call naming a lease whose row has already finished."
tags: [guard, rules, lease-terminal, advise]
---

# lease-terminal

An advisory: it explains, and blocks nothing, on a call naming a lease whose row has already finished.

## What it catches

A call naming a lease whose row has already finished.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [lease-terminal]: ...
```

`magus describe rule lease-terminal` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

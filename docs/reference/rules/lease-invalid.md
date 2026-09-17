---
title: "lease-invalid: a call naming a lease this workspace's job store does not declare"
description: "An advisory: it explains, and blocks nothing, on a call naming a lease this workspace's job store does not declare."
tags: [guard, rules, lease-invalid, advise]
---

# lease-invalid

An advisory: it explains, and blocks nothing, on a call naming a lease this workspace's job store does not declare.

## What it catches

A call naming a lease this workspace's job store does not declare.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [lease-invalid]: ...
```

`magus describe rule lease-invalid` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

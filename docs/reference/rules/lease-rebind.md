---
title: "lease-rebind: a leased worker rewriting who it is or what its own job row says"
description: "A deny rule: it refuses a leased worker rewriting who it is or what its own job row says, and names what to run instead."
tags: [guard, rules, lease-rebind, deny]
---

# lease-rebind

A deny rule: it refuses a leased worker rewriting who it is or what its own job row says, and names what to run instead.

## What it catches

A leased worker rewriting who it is or what its own job row says.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-rebind]: ...
```

`magus describe rule lease-rebind` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

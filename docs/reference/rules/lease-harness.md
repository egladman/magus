---
title: "lease-harness: a leased worker rewriting the harness skill trees that steer it"
description: "A deny rule: it refuses a leased worker rewriting the harness skill trees that steer it, and names what to run instead."
tags: [guard, rules, lease-harness, deny]
---

# lease-harness

A deny rule: it refuses a leased worker rewriting the harness skill trees that steer it, and names what to run instead.

## What it catches

A leased worker rewriting the harness skill trees that steer it.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-harness]: ...
```

`magus describe rule lease-harness` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

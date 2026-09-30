---
title: "lease-undeclared: a call graded under a lease id the job store has no row for, or a binding it tombstoned"
description: "A deny rule: it refuses a call graded under a lease id the job store has no row for, or a binding it tombstoned, and names what to run instead."
tags: [guard, rules, lease-undeclared, deny]
---

# lease-undeclared

A deny rule: it refuses a call graded under a lease id the job store has no row for, or a binding it tombstoned, and names what to run instead.

## What it catches

A call graded under a lease id the job store has no row for, or a binding it tombstoned.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-undeclared]: ...
```

`magus describe rule lease-undeclared` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

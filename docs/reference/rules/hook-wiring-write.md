---
title: "hook-wiring-write: a leased or agent-attributed write to the hook wiring the guard is installed by"
description: "A deny rule: it refuses a leased or agent-attributed write to the hook wiring the guard is installed by, and names what to run instead."
tags: [guard, rules, hook-wiring-write, deny]
---

# hook-wiring-write

A deny rule: it refuses a leased or agent-attributed write to the hook wiring the guard is installed by, and names what to run instead.

## What it catches

A leased or agent-attributed write to the hook wiring the guard is installed by.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [hook-wiring-write]: ...
```

`magus describe rule hook-wiring-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "focus-read: a read outside the paths a focus lease was given"
description: "A deny rule: it refuses a read outside the paths a focus lease was given, and names what to run instead."
tags: [guard, rules, focus-read, deny]
---

# focus-read

A deny rule: it refuses a read outside the paths a focus lease was given, and names what to run instead.

## What it catches

A read outside the paths a focus lease was given.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [focus-read]: ...
```

`magus describe rule focus-read` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

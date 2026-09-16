---
title: "interpreter-rewrite: an inline interpreter rewriting a file this tree already carries"
description: "A deny rule: it refuses an inline interpreter rewriting a file this tree already carries, and names what to run instead."
tags: [guard, rules, interpreter-rewrite, deny]
---

# interpreter-rewrite

A deny rule: it refuses an inline interpreter rewriting a file this tree already carries, and names what to run instead.

## What it catches

An inline interpreter rewriting a file this tree already carries.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [interpreter-rewrite]: ...
```

`magus describe rule interpreter-rewrite` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

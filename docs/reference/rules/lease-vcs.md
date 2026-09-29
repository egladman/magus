---
title: "lease-vcs: a worker lease committing, pushing, stashing or reverting the tree it is landed from"
description: "A deny rule: it refuses a worker lease committing, pushing, stashing or reverting the tree it is landed from, and names what to run instead."
tags: [guard, rules, lease-vcs, deny]
---

# lease-vcs

A deny rule: it refuses a worker lease committing, pushing, stashing or reverting the tree it is landed from, and names what to run instead.

## What it catches

A worker lease committing, pushing, stashing or reverting the tree it is landed from.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-vcs]: ...
```

`magus describe rule lease-vcs` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "shared-stash: a bare stash push or pop, on a stack every worktree shares"
description: "A deny rule: it refuses a bare stash push or pop, on a stack every worktree shares, and names what to run instead."
tags: [guard, rules, shared-stash, deny]
---

# shared-stash

A deny rule: it refuses a bare stash push or pop, on a stack every worktree shares, and names what to run instead.

## What it catches

A bare stash push or pop, on a stack every worktree shares.

## Why

The stash stack is shared across every worktree of a repository, so a bare `pop` can take an entry another session pushed, and a bare `push` can bury one. Prefer a temporary commit to set work aside. If you must stash, push with a unique `-m` tag, capture your entry's SHA, and restore with `apply <sha>` rather than `pop`.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [shared-stash]: ...
```

`magus describe rule shared-stash` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "worktree-remove: removing a worktree, which may hold another session's uncommitted work"
description: "A deny rule: it refuses removing a worktree, which may hold another session's uncommitted work, and names what to run instead."
tags: [guard, rules, worktree-remove, deny]
---

# worktree-remove

A deny rule: it refuses removing a worktree, which may hold another session's uncommitted work, and names what to run instead.

## What it catches

Removing a worktree, which may hold another session's uncommitted work.

## Why

A worktree is where another session may be working right now, and its uncommitted changes live nowhere else. Check it is clean first with `git -C <path> status`, and remove it only once you know what it holds. `git worktree remove --help` and `-h`, alone on the line, print usage and pass.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [worktree-remove]: ...
```

`magus describe rule worktree-remove` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

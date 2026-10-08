---
title: "shared-stash: a bare stash push or pop, on a stack every worktree shares"
description: "A deny rule by default: it refuses a bare stash push or pop, on a stack every worktree shares, and names what to run instead."
tags: [guard, rules, shared-stash, deny]
---

# shared-stash

A deny rule by default: it refuses a bare stash push or pop, on a stack every worktree shares, and names what to run instead.

## What it catches

A bare stash push or pop, on a stack every worktree shares.

## Why

The stash stack is shared across every worktree of a repository, so a bare `pop` can take an entry another session pushed, and a bare `push` can bury one. Prefer a temporary commit to set work aside. If you must stash, push with a unique `-m` tag, capture your entry's SHA, and restore with `apply <sha>` rather than `pop`.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"shared-stash": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

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
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

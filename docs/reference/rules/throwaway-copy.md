---
title: "throwaway-copy: a run inside a temp or scratchpad copy, which leaves the real tree unverified"
description: "A deny rule: it refuses a run inside a temp or scratchpad copy, which leaves the real tree unverified, and names what to run instead."
tags: [guard, rules, throwaway-copy, deny]
---

# throwaway-copy

A deny rule: it refuses a run inside a temp or scratchpad copy, which leaves the real tree unverified, and names what to run instead.

## What it catches

A run inside a temp or scratchpad copy, which leaves the real tree unverified.

## Why

A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits. No magus run needs a clean tree; run from the workspace and name the project. If you genuinely need a pristine tree, use a throwaway `git worktree add`, not a copy.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [throwaway-copy]: ...
```

`magus describe rule throwaway-copy` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

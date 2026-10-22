---
title: "throwaway-copy: a run inside a temp or scratchpad copy, which leaves the real tree unverified"
description: "A deny rule by default: it refuses a run inside a temp or scratchpad copy, which leaves the real tree unverified, and names what to run instead."
tags: [guard, rules, throwaway-copy, deny]
---

# throwaway-copy

A deny rule by default: it refuses a run inside a temp or scratchpad copy, which leaves the real tree unverified, and names what to run instead.

## What it catches

A run inside a temp or scratchpad copy, which leaves the real tree unverified.

## Why

A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits. No magus run needs a clean tree; run from the workspace and name the project. If you genuinely need a pristine tree, use a throwaway `git worktree add`, not a copy.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"throwaway-copy": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [throwaway-copy]: ...
```

A workspace that sets this rule to `advise` sees `advise [throwaway-copy]: ...` instead.

`magus describe rule throwaway-copy` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

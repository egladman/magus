---
title: "lease-vcs: a worker lease pushing, stashing or reverting, or committing outside its own branch and checkout"
description: "A deny rule by default: it refuses a worker lease pushing, stashing or reverting, or committing outside its own branch and checkout, and names what to run instead."
tags: [guard, rules, lease-vcs, deny]
---

# lease-vcs

A deny rule by default: it refuses a worker lease pushing, stashing or reverting, or committing outside its own branch and checkout, and names what to run instead.

## What it catches

A worker lease pushing, stashing or reverting, or committing outside its own branch and checkout.

## Why

The orchestrator lands every unit from the worker's tree, so a worker that pushes, stashes, reverts, resets, cleans, rebases, merges, cherry-picks or removes a worktree changes the state it is integrated from, and a whole-tree revert destroys a sibling's uncommitted work. A commit is the one exception: allowed only in the lease's checkout_root, when that is a secondary checkout on a named branch other than the base, with any backend. Every spelling is placed alike: `git -C <dir>`, `--git-dir`, `GIT_DIR=`, a `cd` before it, and vcs\cmd from a Buzz script under the lease. A commit whose checkout cannot be read is refused, since it cannot be shown to be the worker's own. A worker is a row with a parent, a lease a subagent holds, or a caller that names no session; only an identified root session holding a parentless row is the root.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-vcs": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

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
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

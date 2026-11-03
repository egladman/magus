---
title: "spawn-unisolated-row: an isolated spawn for a writing job bound to the spawner's own checkout"
description: "A deny rule by default: it refuses an isolated spawn for a writing job bound to the spawner's own checkout, and names what to run instead."
tags: [guard, rules, spawn-unisolated-row, deny]
---

# spawn-unisolated-row

A deny rule by default: it refuses an isolated spawn for a writing job bound to the spawner's own checkout, and names what to run instead.

## What it catches

An isolated spawn for a writing job bound to the spawner's own checkout.

## Why

`magus job exec` binds a job to the checkout it runs in, and a job forked from the root's checkout without --checkout binds there. The host's worktree isolation cannot move that binding, so the worker edits the spawner's tree beside its other workers. Read-only jobs, jobs with a checkout of their own and spawns without isolation pass.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-unisolated-row": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-unisolated-row]: ...
```

A workspace that sets this rule to `advise` sees `advise [spawn-unisolated-row]: ...` instead.

`magus describe rule spawn-unisolated-row` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

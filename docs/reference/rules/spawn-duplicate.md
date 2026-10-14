---
title: "spawn-duplicate: a spawn for a job that already has a worker"
description: "A deny rule by default: it refuses a spawn for a job that already has a worker, and names what to run instead."
tags: [guard, rules, spawn-duplicate, deny]
---

# spawn-duplicate

A deny rule by default: it refuses a spawn for a job that already has a worker, and names what to run instead.

## What it catches

A spawn for a job that already has a worker.

## Why

A spawn titled `<parent>/<role> <job>` hands that job to its child. When an earlier spawn in this session was already attributed to that job and its row is still declared or running, a second worker would write the same paths from a second context. Continue the worker with a message instead, or stop it first. A running row alone does not fire it, since a row stays running after its worker dies; another session's worker is not this rule's to see. It cannot tell this session's worker that died from one still alive.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-duplicate": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-duplicate]: ...
advise [spawn-duplicate]: ...
```

`magus describe rule spawn-duplicate` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

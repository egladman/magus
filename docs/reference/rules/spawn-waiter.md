---
title: "spawn-waiter: a spawn whose child exists to wait on, poll or watch other work"
description: "An advisory by default: it explains, and blocks nothing, on a spawn whose child exists to wait on, poll or watch other work."
tags: [guard, rules, spawn-waiter, advise]
---

# spawn-waiter

An advisory by default: it explains, and blocks nothing, on a spawn whose child exists to wait on, poll or watch other work.

## What it catches

A spawn whose child exists to wait on, poll or watch other work.

## Why

A host tells a parent when each child ends, and a root waits on a job with `magus job wait`, so an agent spawned to wait spends a context on a notice that already comes. It reads only the title and the opening of the brief: one that opens on wait, poll, monitor, watch or babysit, or says to wait for, on or until an agent, worker or job. It cannot tell a brief opening on a feature named for watching ("Monitor endpoint: add a route") from one whose child only waits.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-waiter": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [spawn-waiter]: ...
```

A workspace that sets this rule to `deny` sees `deny [spawn-waiter]: ...` instead.

`magus describe rule spawn-waiter` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

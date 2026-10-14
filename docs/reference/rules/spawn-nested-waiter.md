---
title: "spawn-nested-waiter: a subagent spawning a helper whose title or brief opening speaks of waiting or watching"
description: "An advisory by default: it explains, and blocks nothing, on a subagent spawning a helper whose title or brief opening speaks of waiting or watching."
tags: [guard, rules, spawn-nested-waiter, advise]
---

# spawn-nested-waiter

An advisory by default: it explains, and blocks nothing, on a subagent spawning a helper whose title or brief opening speaks of waiting or watching.

## What it catches

A subagent spawning a helper whose title or brief opening speaks of waiting or watching.

## Why

Waiting belongs to whoever spawned the work: the host tells a parent when its child ends. A worker handing that to a helper of its own stacks a third context on a notice two already get. It advises, once a session, because a worker may also spawn a child to build a watch feature, which the words alone cannot tell apart.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-nested-waiter": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-nested-waiter]: ...
advise [spawn-nested-waiter]: ...
```

`magus describe rule spawn-nested-waiter` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

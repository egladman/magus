---
title: "spawn-nested-waiter: a subagent spawning a helper whose title or brief opening speaks of waiting or watching"
description: "A deny rule by default: it refuses a subagent spawning a helper whose title or brief opening speaks of waiting or watching, and names what to run instead."
tags: [guard, rules, spawn-nested-waiter, deny]
---

# spawn-nested-waiter

A deny rule by default: it refuses a subagent spawning a helper whose title or brief opening speaks of waiting or watching, and names what to run instead.

## What it catches

A subagent spawning a helper whose title or brief opening speaks of waiting or watching.

## Why

Waiting belongs to whoever spawned the work: the host tells a parent when its child ends. A worker handing that to a helper of its own stacks a third context on a notice two already get. A worker spawning a child to build a watch feature reads the same and rewords its title; a workspace where that is common sets the rule to advise, which says it once a session.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-nested-waiter": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-nested-waiter]: ...
```

A workspace that sets this rule to `advise` sees `advise [spawn-nested-waiter]: ...` instead.

`magus describe rule spawn-nested-waiter` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

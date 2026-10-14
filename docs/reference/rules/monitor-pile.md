---
title: "monitor-pile: an agent arming a waiter while several of its own are already live"
description: "An advisory by default: it explains, and blocks nothing, on an agent arming a waiter while several of its own are already live."
tags: [guard, rules, monitor-pile, advise]
---

# monitor-pile

An advisory by default: it explains, and blocks nothing, on an agent arming a waiter while several of its own are already live.

## What it catches

An agent arming a waiter while several of its own are already live.

## Why

Every live waiter, a watcher, a backgrounded sleep or an endless backgrounded watch, wakes its agent on its own schedule, and the ones still armed after the work ends wake a finished agent until they expire. The third live waiter of one agent is advised against and the fifth refused. A waiter counts from when it is armed until its own timeout, so it cannot tell a watcher whose command already exited from one still running.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"monitor-pile": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [monitor-pile]: ...
advise [monitor-pile]: ...
```

`magus describe rule monitor-pile` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

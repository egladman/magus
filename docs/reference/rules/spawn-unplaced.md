---
title: "spawn-unplaced: a subagent's first call that no waiting spawn names, which then runs under no lease"
description: "An advisory by default: it explains, and blocks nothing, on a subagent's first call that no waiting spawn names, which then runs under no lease."
tags: [guard, rules, spawn-unplaced, advise]
---

# spawn-unplaced

An advisory by default: it explains, and blocks nothing, on a subagent's first call that no waiting spawn names, which then runs under no lease.

## What it catches

A subagent's first call that no waiting spawn names, which then runs under no lease.

## Why

Advice, not a refusal: the caller may be no worker at all, and spawns wait for their child up to two minutes.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-unplaced": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [spawn-unplaced]: ...
```

A workspace that sets this rule to `deny` sees `deny [spawn-unplaced]: ...` instead.

`magus describe rule spawn-unplaced` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

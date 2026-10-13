---
title: "spawn-principal-for-economy: a read-only job's subagent spawned on one of the costliest models"
description: "An advisory by default: it explains, and blocks nothing, on a read-only job's subagent spawned on one of the costliest models."
tags: [guard, rules, spawn-principal-for-economy, off]
---

# spawn-principal-for-economy

An advisory by default: it explains, and blocks nothing, on a read-only job's subagent spawned on one of the costliest models.

## What it catches

A read-only job's subagent spawned on one of the costliest models.

## Why

A job marked read-only gathers evidence, which a cheaper model does as well. The spawn's model, or the session's own when it names none, counts as costly when its input price is above the median of the model table. At standard it advises once; at strict it also advises on a spawn of the host's read-only explore agent type. It keys on declared facts, never on the prompt, and never refuses, since a read-only design job can need the strongest model.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-principal-for-economy": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
off [spawn-principal-for-economy]: ...
```

`magus describe rule spawn-principal-for-economy` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

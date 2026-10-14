---
title: "spawn-above-model-tier: a subagent spawned on a model whose tier ranks above the tier its job was forked at"
description: "An advisory by default: it explains, and blocks nothing, on a subagent spawned on a model whose tier ranks above the tier its job was forked at."
tags: [guard, rules, spawn-above-model-tier, off]
---

# spawn-above-model-tier

An advisory by default: it explains, and blocks nothing, on a subagent spawned on a model whose tier ranks above the tier its job was forked at.

## What it catches

A subagent spawned on a model whose tier ranks above the tier its job was forked at.

## Why

A job row records the model tier it was forked at and why. A spawn of a model the declaration ranks above that tier spends more than the row chose, and the row no longer says why. It compares ranks in the workspace's declaration, never model names or prices, and says nothing for a model no tier maps. It never refuses: work can outgrow its row, and the remedy is re-forking the row at the tier it needs so the choice is recorded, which only the root can judge.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-above-model-tier": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [spawn-above-model-tier]: ...
```

`magus describe rule spawn-above-model-tier` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

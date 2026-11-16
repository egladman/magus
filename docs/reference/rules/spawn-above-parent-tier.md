---
title: "spawn-above-parent-tier: a subagent spawned on a model whose tier ranks above its spawner's"
description: "An advisory by default: it explains, and blocks nothing, on a subagent spawned on a model whose tier ranks above its spawner's."
tags: [guard, rules, spawn-above-parent-tier, advise]
---

# spawn-above-parent-tier

An advisory by default: it explains, and blocks nothing, on a subagent spawned on a model whose tier ranks above its spawner's.

## What it catches

A subagent spawned on a model whose tier ranks above its spawner's.

## Why

The root session runs on the model the person picked, and a child above that tier spends more than its parent chose. The way up is allowed: the spawn is advised once, naming both tiers and ranks, what each model costs per million input and output tokens by its mgs_listModels row (a price no row gives is said to be unknown), the alias that stays at the parent's tier, and that setting the rule off in magus\guard.builtins stops the advice. Both models resolve to tiers by an exact alias of the spawning host in the workspace's model-tier declaration, or by the exact id of a mgs_listModels row that names a tier, and compare by rank, never by name or price. A spawn naming no model is never judged. Any other model has an unknown tier: magus never infers one from a name, and advises once naming the model, the aliases the declaration maps and the mgs_listModels row to add. Set to deny, a spawn whose spawner's model is proven is refused and told to spawn at the parent's alias or lower. The model is proven when its host reported it: a main thread's from its transcript once the host has reported the thread, a subagent's from the model its spawn named. A subagent spawned on no model is taken to run on the session's, which is inferred and only advised. `magus job fork` follows the same setting for a child row above its parent's tier: advised by default, refused under deny, and silent when off.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-above-parent-tier": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [spawn-above-parent-tier]: ...
```

A workspace that sets this rule to `deny` sees `deny [spawn-above-parent-tier]: ...` instead.

`magus describe rule spawn-above-parent-tier` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

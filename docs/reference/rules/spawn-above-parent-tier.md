---
title: "spawn-above-parent-tier: a subagent spawned on a model whose tier ranks above its spawner's"
description: "A deny rule by default: it refuses a subagent spawned on a model whose tier ranks above its spawner's, and names what to run instead."
tags: [guard, rules, spawn-above-parent-tier, deny]
---

# spawn-above-parent-tier

A deny rule by default: it refuses a subagent spawned on a model whose tier ranks above its spawner's, and names what to run instead.

## What it catches

A subagent spawned on a model whose tier ranks above its spawner's.

## Why

A child never outranks the work that handed it out: the root session runs on the model the person picked, and every agent below it stays at that tier or lower. Both models resolve to tiers by an exact alias of the spawning host in the workspace's model-tier declaration, or by the exact id of a mgs_listModels row that names a tier, and compare by rank, never by name or price. A spawn naming no model is never judged. Any other model has an unknown tier: magus never infers one from a name, and advises once naming the model, the aliases the declaration maps and the mgs_listModels row to add. The spawner's model is proven when its host reported it: a main thread's from its transcript once the host has reported the thread, a subagent's from the model its spawn named. Proven, the spawn is refused; a subagent spawned on no model is taken to run on the session's, which is inferred and advised once. `magus job fork` holds a child row to its parent's tier the same way. The way up is a person's: they fork the higher-tier row and run it from a session at that tier. Set to advise, proof is advised too.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-above-parent-tier": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-above-parent-tier]: ...
```

A workspace that sets this rule to `advise` sees `advise [spawn-above-parent-tier]: ...` instead.

`magus describe rule spawn-above-parent-tier` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

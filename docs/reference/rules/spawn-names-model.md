---
title: "spawn-names-model: a main thread spawning a subagent without naming the model it runs on"
description: "An advisory by default: it explains, and blocks nothing, on a main thread spawning a subagent without naming the model it runs on."
tags: [guard, rules, spawn-names-model, off]
---

# spawn-names-model

An advisory by default: it explains, and blocks nothing, on a main thread spawning a subagent without naming the model it runs on.

## What it catches

A main thread spawning a subagent without naming the model it runs on.

## Why

A spawn that names no model runs the child on the session's own, usually the costliest it uses. At standard it advises once; at strict it refuses, and also refuses a spawn whose job row records a model tier when the spawn's model is not at that tier. The model resolves to a tier through the host's aliases in the workspace's model-tier declaration, and the two compare by rank; a host the declaration maps no model for is refused naming the hosts entry to add. The refusal names the row's tier, rank, alias, the source that chose it, its reason and the declaration's site. A spawn bound to no row, or to a row forked with no tiers declared, is judged on the missing model alone.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-names-model": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-names-model]: ...
advise [spawn-names-model]: ...
```

`magus describe rule spawn-names-model` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

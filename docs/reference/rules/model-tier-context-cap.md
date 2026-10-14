---
title: "model-tier-context-cap: a leased worker writing past the context its job's model tier caps"
description: "An advisory by default: it explains, and blocks nothing, on a leased worker writing past the context its job's model tier caps."
tags: [guard, rules, model-tier-context-cap, advise]
---

# model-tier-context-cap

An advisory by default: it explains, and blocks nothing, on a leased worker writing past the context its job's model tier caps.

## What it catches

A leased worker writing past the context its job's model tier caps.

## Why

A model tier's bounds in the workspace's declaration can cap the context a worker at that tier reaches (maxContext), because a smaller model past a large context does worse work and resends all of it every turn. When a leased worker's context, as its host last reported it, passes the cap of the tier its row was forked at, its next write is advised to report what it has and stop. Set to deny, every write under the lease is refused, as a lease past its deadline is. A tier with no cap, a row with no tier, or a host that reports no context is never judged.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"model-tier-context-cap": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [model-tier-context-cap]: ...
advise [model-tier-context-cap]: ...
```

`magus describe rule model-tier-context-cap` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

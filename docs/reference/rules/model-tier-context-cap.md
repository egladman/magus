---
title: "model-tier-context-cap: a leased worker writing past the context its job's model tier caps"
description: "A deny rule by default: it refuses a leased worker writing past the context its job's model tier caps, and names what to run instead."
tags: [guard, rules, model-tier-context-cap, deny]
---

# model-tier-context-cap

A deny rule by default: it refuses a leased worker writing past the context its job's model tier caps, and names what to run instead.

## What it catches

A leased worker writing past the context its job's model tier caps.

## Why

A model tier's bounds in the workspace's declaration can cap the context a worker at that tier reaches (maxContext), because a smaller model past a large context does worse work and resends all of it every turn. When a leased worker's context, as its host last reported it, passes the cap of the tier its row was forked at, every write under the lease is refused, as a lease past its deadline is, and the refusal says to report what it has and stop. Set to advise, the next write is told so once. A tier with no cap, a row with no tier, or a host that reports no context is never judged.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"model-tier-context-cap": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [model-tier-context-cap]: ...
```

A workspace that sets this rule to `advise` sees `advise [model-tier-context-cap]: ...` instead.

`magus describe rule model-tier-context-cap` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

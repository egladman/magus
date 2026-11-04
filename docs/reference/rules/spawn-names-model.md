---
title: "spawn-names-model: a spawn omitting its model, or (at deny) using a model outside its row's tier"
description: "Off by default: once a workspace turns it on, it reports a spawn omitting its model, or (at deny) using a model outside its row's tier."
tags: [guard, rules, spawn-names-model, off]
---

# spawn-names-model

Off by default: once a workspace turns it on, it reports a spawn omitting its model, or (at deny) using a model outside its row's tier.

## What it catches

A spawn omitting its model, or (at deny) using a model outside its row's tier.

## Why

Off by default; a workspace may set it to advise, deny or off. With advise, a missing model is advised once. With deny, a reported spawning thread is refused for a missing model, while an inferred caller is advised. At deny, a reported spawn bound to a tiered row is refused when its model does not resolve to that row's rank; an inferred mismatch is advised. A model recorded on the row remains accepted. Other models resolve through the host's alias for that tier or an exact mgs_listModels id or alias; a host without a tier alias is named for configuration. Without a bound tiered row, a named model is not compared.

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
advise [spawn-names-model]: ...
```

A workspace that sets this rule to `deny` sees `deny [spawn-names-model]: ...` instead.

`magus describe rule spawn-names-model` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

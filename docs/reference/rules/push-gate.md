---
title: "push-gate: a push the run log does not prove ungated, which names the gate and lets it through"
description: "An advisory by default: it explains, and blocks nothing, on a push the run log does not prove ungated, which names the gate and lets it through."
tags: [guard, rules, push-gate, advise]
---

# push-gate

An advisory by default: it explains, and blocks nothing, on a push the run log does not prove ungated, which names the gate and lets it through.

## What it catches

A push the run log does not prove ungated, which names the gate and lets it through.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"push-gate": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [push-gate]: ...
```

`magus describe rule push-gate` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

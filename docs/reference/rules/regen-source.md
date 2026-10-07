---
title: "regen-source: a hand edit to a file a target regenerates"
description: "An advisory by default: it explains, and blocks nothing, on a hand edit to a file a target regenerates."
tags: [guard, rules, regen-source, advise]
---

# regen-source

An advisory by default: it explains, and blocks nothing, on a hand edit to a file a target regenerates.

## What it catches

A hand edit to a file a target regenerates.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"regen-source": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [regen-source]: ...
```

`magus describe rule regen-source` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

---
title: "instruction-write: a write to a cross-host instruction file, which every session loads whole"
description: "An advisory by default: it explains, and blocks nothing, on a write to a cross-host instruction file, which every session loads whole."
tags: [guard, rules, instruction-write, advise]
---

# instruction-write

An advisory by default: it explains, and blocks nothing, on a write to a cross-host instruction file, which every session loads whole.

## What it catches

A write to a cross-host instruction file, which every session loads whole.

## Why

A cross-host instruction file is read in full at the start of every session on every host, so a sentence there costs context forever. A rule the guard already refuses or doctor already reports is restated context: delete it the moment the tool starts saying it.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"instruction-write": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [instruction-write]: ...
```

`magus describe rule instruction-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

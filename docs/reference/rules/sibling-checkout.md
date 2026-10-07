---
title: "sibling-checkout: a magus command relocated into another checkout, judging a tree nobody ships"
description: "An advisory by default: it explains, and blocks nothing, on a magus command relocated into another checkout, judging a tree nobody ships."
tags: [guard, rules, sibling-checkout, advise]
---

# sibling-checkout

An advisory by default: it explains, and blocks nothing, on a magus command relocated into another checkout, judging a tree nobody ships.

## What it catches

A magus command relocated into another checkout, judging a tree nobody ships.

## Why

A binary links the spell sources of the tree it was built from, so a verdict it reaches about a DIFFERENT checkout describes a tree that exists nowhere, and anything it regenerates lands there unmarked. Run magus from the workspace it belongs to and name the project as an argument; a different workspace is `--root <path>`.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"sibling-checkout": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [sibling-checkout]: ...
```

`magus describe rule sibling-checkout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

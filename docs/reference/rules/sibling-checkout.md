---
title: "sibling-checkout: a magus command relocated into another checkout, judging a tree nobody ships"
description: "A deny rule: it refuses a magus command relocated into another checkout, judging a tree nobody ships, and names what to run instead."
tags: [guard, rules, sibling-checkout, deny]
---

# sibling-checkout

A deny rule: it refuses a magus command relocated into another checkout, judging a tree nobody ships, and names what to run instead.

## What it catches

A magus command relocated into another checkout, judging a tree nobody ships.

## Why

A binary links the spell sources of the tree it was built from, so a verdict it reaches about a DIFFERENT checkout describes a tree that exists nowhere, and anything it regenerates lands there unmarked. Run magus from the workspace it belongs to and name the project as an argument; a different workspace is `--root <path>`.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [sibling-checkout]: ...
```

`magus describe rule sibling-checkout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

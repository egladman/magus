---
title: "graph-stale: a graph read, or a graph-backed deny, while the graph describes another tree"
description: "An advisory: it explains, and blocks nothing, on a graph read, or a graph-backed deny, while the graph describes another tree."
tags: [guard, rules, graph-stale, advise]
---

# graph-stale

An advisory: it explains, and blocks nothing, on a graph read, or a graph-backed deny, while the graph describes another tree.

## What it catches

A graph read, or a graph-backed deny, while the graph describes another tree.

## Why

symbol-search, grep-reader and search-translation deny in favor of a graph answer, so they first ask whether the graph describes the tree on disk: no merge, rebase, cherry-pick or revert underway, read through the workspace's version control, and a guard index built at the current revision. When either fails the search runs, and this names why and `magus graph build`, to run once the operation is finished: a rebuild mid-rebase would describe a tree about to change. An index that records another revision is stale for every kind, so after a history rewrite the next lookup waits for a rebuild rather than trusting it.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [graph-stale]: ...
```

`magus describe rule graph-stale` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

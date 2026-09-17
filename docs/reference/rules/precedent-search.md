---
title: "precedent-search: a hunt for one distinctive name, which refs answers with verified sites"
description: "An advisory: it explains, and blocks nothing, on a hunt for one distinctive name, which refs answers with verified sites."
tags: [guard, rules, precedent-search, advise]
---

# precedent-search

An advisory: it explains, and blocks nothing, on a hunt for one distinctive name, which refs answers with verified sites.

## What it catches

A hunt for one distinctive name, which refs answers with verified sites.

## Why

A precedent hunt is a search for one distinctive name, and it is the search the graph answers best: refs lists verified sites, so you land on working code instead of assembling it from grep hits. Measured over 1,499 sessions: 42% of new files were preceded by one of these, 71% in subagent sessions, where only 12.9% reached for a magus verb at all.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [precedent-search]: ...
```

`magus describe rule precedent-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

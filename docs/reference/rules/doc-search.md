---
title: "doc-search: a search through markdown, where headings are indexed as doc sections"
description: "An advisory: it explains, and blocks nothing, on a search through markdown, where headings are indexed as doc sections."
tags: [guard, rules, doc-search, advise]
---

# doc-search

An advisory: it explains, and blocks nothing, on a search through markdown, where headings are indexed as doc sections.

## What it catches

A search through markdown, where headings are indexed as doc sections.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [doc-search]: ...
```

`magus describe rule doc-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

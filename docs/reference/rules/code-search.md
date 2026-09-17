---
title: "code-search: a repo-wide text search that the symbol graph may answer better"
description: "An advisory: it explains, and blocks nothing, on a repo-wide text search that the symbol graph may answer better."
tags: [guard, rules, code-search, advise]
---

# code-search

An advisory: it explains, and blocks nothing, on a repo-wide text search that the symbol graph may answer better.

## What it catches

A repo-wide text search that the symbol graph may answer better.

## Why

A text match misses the generated, indirect and cross-language references the graph knows about, so the two agree only when the pattern is a real symbol. It ADVISES rather than refuses because that is exactly the case it cannot check in advance: an empty semantic result means the pattern was text, and grep was the right tool after all. Pick by the question: `magus refs <symbol>` for a code symbol, `magus query "<terms>"` for a domain entity, `magus refs --text <pattern>` for raw text.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [code-search]: ...
```

`magus describe rule code-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

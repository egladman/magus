---
title: "symbol-search: a recursive text search for a symbol the index defines and can enumerate"
description: "A deny rule: it refuses a recursive text search for a symbol the index defines and can enumerate, and names what to run instead."
tags: [guard, rules, symbol-search, deny]
---

# symbol-search

A deny rule: it refuses a recursive text search for a symbol the index defines and can enumerate, and names what to run instead.

## What it catches

A recursive text search for a symbol the index defines and can enumerate.

## Why

It fires only when the index can VOUCH for the name: the symbol is defined here and no project's index is older than its sources. On those terms `magus refs <symbol> --occurrences` knows every definition and reference, including the generated and cross-language ones a pattern misses. Searching raw TEXT is untouched and has its own answer: `magus refs --text <pattern> [<path>...]` is a literal substring search with grep's exit codes, scoped by the same trailing paths.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [symbol-search]: ...
```

`magus describe rule symbol-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

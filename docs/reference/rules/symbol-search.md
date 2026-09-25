---
title: "symbol-search: a recursive text search for names the graph answers exactly: symbols or diagnostic codes"
description: "A deny rule: it refuses a recursive text search for names the graph answers exactly: symbols or diagnostic codes, and names what to run instead."
tags: [guard, rules, symbol-search, deny]
---

# symbol-search

A deny rule: it refuses a recursive text search for names the graph answers exactly: symbols or diagnostic codes, and names what to run instead.

## What it catches

A recursive text search for names the graph answers exactly: symbols or diagnostic codes.

## Why

It fires only when the graph can VOUCH for every name the pattern looks for: each symbol is defined here and no project's index is older than its sources, and each diagnostic code is one the graph carries a node for. On those terms `magus refs <symbol> --occurrences` knows every definition and reference, including the generated and cross-language ones a pattern misses, and `magus explain diagnostic:<code>` knows the code's page and what documents and emits it. An alternation (`A\|B`, `-e A -e B`, `A|B` under -E) is answered with one command per name, and a definition lookup (`func X`, `func (r *T) X`, `type X`) with refs on X. A single name the index cannot vouch for, a BZZ code, a case-insensitive search, or a search of a tree outside the workspace stays advice or nothing. Searching raw TEXT is untouched and has its own answer: `magus refs --text <pattern> [<path>...]` is a literal substring search with grep's exit codes, scoped by the same trailing paths.

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

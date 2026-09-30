---
title: "symbol-search: a text search for symbols or diagnostic codes the graph answers exactly"
description: "A deny rule: it refuses a text search for symbols or diagnostic codes the graph answers exactly, and names what to run instead."
tags: [guard, rules, symbol-search, deny]
---

# symbol-search

A deny rule: it refuses a text search for symbols or diagnostic codes the graph answers exactly, and names what to run instead.

## What it catches

A text search for symbols or diagnostic codes the graph answers exactly.

## Why

It fires only when the graph can VOUCH for every name the pattern looks for: each symbol is defined here and no project's index is older than its sources, and each diagnostic code is one the graph carries a node for. On those terms `magus refs <symbol> --occurrences` knows every definition and reference, including the generated and cross-language ones a pattern misses, and `magus explain diagnostic:<code>` knows the code's page and what documents and emits it. An alternation (`A\|B`, `-e A -e B`, `A|B` under -E) is answered with one command per name, and a definition lookup (`func X`, `func (r *T) X`, `type X`) with `magus refs X --definition --source`, which prints the body in place of the grep-then-sed pair. A search naming Go files (`grep -n Foo file.go`, measured 2026-09-26 as the commonest symbol lookup) runs, with refs advised: a deny could only hand back the lines grep prints, so it would cost a turn and save nothing. A definition lookup carrying -A, -B or -C is a read of the body, and grep-reader refuses it. A search of the tree carries the index's own answer when it can give one within the hook's budget: every file under the searched paths with its occurrence count and lines, as `magus refs` prints them. It is refs' answer, not grep's: comments, strings and prose are not in it. A pipe after the search is not reproduced. The deny still carries the unfiltered answer and says so: a model of sort, sed or awk substituted for the real tool diverges from it, and the deny would then state the wrong output as fact. A stale index or a diagnostic code keeps the routing deny without the answer. A single name the index cannot vouch for, a BZZ code, a case-insensitive search, a Markdown or log operand, or a search of a tree outside the workspace stays advice or nothing. Searching raw TEXT is untouched and has its own answer: `magus refs --text <pattern> [<path>...]` is a literal substring search with grep's exit codes, scoped by the same trailing paths. Measured 2026-09-24 over 14,773 search patterns: 45% were alternations and 13% definition lookups, and the single-identifier form this rule started with fired 0 times.

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

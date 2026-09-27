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

It fires only when the graph can VOUCH for every name the pattern looks for: each symbol is defined here and no project's index is older than its sources, and each diagnostic code is one the graph carries a node for. On those terms `magus refs <symbol> --occurrences` knows every definition and reference, including the generated and cross-language ones a pattern misses, and `magus explain diagnostic:<code>` knows the code's page and what documents and emits it. An alternation (`A\|B`, `-e A -e B`, `A|B` under -E) is answered with one command per name, and a definition lookup (`func X`, `func (r *T) X`, `type X`) with `magus refs X --definition --source`, which prints the body in place of the grep-then-sed pair. A search naming Go files (`grep -n Foo file.go`, measured 2026-09-26 as the commonest symbol lookup) is denied with the lines it would have printed, so the line numbers a bounded read needs are still there. A search of the tree carries the index's own answer when it can give one within the hook's budget: every file under the searched paths with its occurrence count and lines, as `magus refs` prints them. A pipe after the search (`| head`, `| grep -v _test`, `| wc -l`, `| cut -d: -f1 | sort | uniq -c`) is run over the rows the search would print, so the deny answers the pipeline's question, and a projection onto files routes to `magus refs <symbol>`, the per-file view. A stale index, a site list the graph capped, or a pipe over rows the index does not hold keeps the routing deny without the answer, and a filter no row model reproduces (`| xargs`, `| awk` past a field print, `| while read`) is silent. A single name the index cannot vouch for, a BZZ code, a case-insensitive search, a Markdown or log operand, or a search of a tree outside the workspace stays advice or nothing. Searching raw TEXT is untouched and has its own answer: `magus refs --text <pattern> [<path>...]` is a literal substring search with grep's exit codes, scoped by the same trailing paths.

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

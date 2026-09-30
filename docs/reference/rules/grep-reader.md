---
title: "grep-reader: a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body"
description: "A deny rule: it refuses a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body, and names what to run instead."
tags: [guard, rules, grep-reader, deny]
---

# grep-reader

A deny rule: it refuses a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body, and names what to run instead.

## What it catches

A definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body.

## Why

A context count guesses at a declaration's length: too short cuts the body off and costs another call, too long spends lines on whatever follows. `magus refs X --definition --source` prints the declaration whole, numbered and checked against the index. Where the index cannot vouch for the name, the deny serves `sed -n <first>,<last>p <file>` instead, the declaration's own lines from a parse of the file the search reads (a named file or glob, or under a directory the files the index last saw name it). The single-file allowance symbol-search gives `grep -n 'func X' f.go` does not apply: with -A, -B or -C the search is the read. It fires only when every alternative is a definition lookup (`func X`, `func (r *T) X`, `type X`, `type X struct`) and a declaration of each name is found; a search for uses, a case-insensitive one, or one with any text alternative is left to the search rules. A pipe after it is named as not reproduced. Measured 2026-09-29 over the audit's Claude, Codex and Cursor transcripts: 1,456 context-flag definition lookups, 16 of them denied by any rule. A hand-read sample of 39 held 33 (85%) where the served command answered what the grep asked; the 6 misses filtered the body through a second grep for a few lines, which the served command answers at a higher cost.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [grep-reader]: ...
```

`magus describe rule grep-reader` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "grep-reader: a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body"
description: "An advisory by default: it explains, and blocks nothing, on a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body."
tags: [guard, rules, grep-reader, advise]
---

# grep-reader

An advisory by default: it explains, and blocks nothing, on a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body.

## What it catches

A definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body.

## Why

A context count guesses at a declaration's length: too short cuts the body off and costs another call, too long spends lines on whatever follows. `magus refs X --definition --source` prints the declaration whole, numbered and checked against the index. Where the index cannot vouch for the name, the deny serves `sed -n <first>,<last>p <file>` instead, the declaration's own lines from a parse of the file the search reads (a named file or glob, or under a directory the files the index last saw name it). The single-file allowance symbol-search gives `grep -n 'func X' f.go` does not apply: with -A, -B or -C the search is the read. A deny resting on the index advises instead while the graph describes another tree, as graph-stale; one parsed from a named file does not. It fires only when every alternative is a definition lookup (`func X`, `func (r *T) X`, `type X`, `type X struct`) and a declaration of each name is found; a search for uses, a case-insensitive one, or one with any text alternative is left to the search rules. A pipe after it is named as not reproduced.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"grep-reader": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [grep-reader]: ...
```

`magus describe rule grep-reader` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

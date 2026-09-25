---
title: "scripted-rewrite: a scripted substitute-and-write, which cannot tell your symbol from a dependency's"
description: "A deny rule: it refuses a scripted substitute-and-write, which cannot tell your symbol from a dependency's, and names what to run instead."
tags: [guard, rules, scripted-rewrite, deny]
---

# scripted-rewrite

A deny rule: it refuses a scripted substitute-and-write, which cannot tell your symbol from a dependency's, and names what to run instead.

## What it catches

A scripted substitute-and-write, which cannot tell your symbol from a dependency's.

## Why

A regex cannot tell YOUR symbol from a dependency's symbol of the same name. A `\.Sum\b` rewrite aimed at one proto field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`, and the damage is written before any diff is read. The graph knows which is which and a pattern never can: `magus refs <symbol> --occurrences` returns verified sites, per file, with columns. Run `magus graph build` first if refs reports a project not-indexed, because that verdict means unknown rather than absent, and taking it for "no matches" is how a rename misses half its sites. Rewriting raw TEXT (prose, a config value, a string literal) has no graph equivalent; say so and use an editor tool. A script file is judged by its program: `python3 p.py`, and a write of p.py, get the verdict the same program would get inline. A program whose every named path lies outside the workspace is untouched.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [scripted-rewrite]: ...
```

`magus describe rule scripted-rewrite` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

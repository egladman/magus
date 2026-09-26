---
title: "graph-pipe: a read-only graph verb piped into a text filter, when magus projects the record itself"
description: "An advisory: it explains, and blocks nothing, on a read-only graph verb piped into a text filter, when magus projects the record itself."
tags: [guard, rules, graph-pipe, advise]
---

# graph-pipe

An advisory: it explains, and blocks nothing, on a read-only graph verb piped into a text filter, when magus projects the record itself.

## What it catches

A read-only graph verb piped into a text filter, when magus projects the record itself.

## Why

The same answer output-pipe gives, offered rather than imposed: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field. It advises on `refs`, `query`, `explain` and `describe` because they change nothing and their pipe loses no failure. Measured 2026-09-26: 819 output-pipe denies landed on these verbs, and the refused agent went back to `grep -rn`, which answers with less than the graph read it was denied.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [graph-pipe]: ...
```

`magus describe rule graph-pipe` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "graph-pipe: a read-only graph verb piped into a text filter, when magus projects the record itself"
description: "An advisory by default: it explains, and blocks nothing, on a read-only graph verb piped into a text filter, when magus projects the record itself."
tags: [guard, rules, graph-pipe, advise]
---

# graph-pipe

An advisory by default: it explains, and blocks nothing, on a read-only graph verb piped into a text filter, when magus projects the record itself.

## What it catches

A read-only graph verb piped into a text filter, when magus projects the record itself.

## Why

The same answer output-pipe gives, offered rather than imposed: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field. It advises on `refs`, `query`, `explain` and `describe` because they change nothing and their pipe loses no failure. A reader refused on these verbs tends to go back to `grep -rn`, which answers with less than the graph read it was denied.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"graph-pipe": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

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
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

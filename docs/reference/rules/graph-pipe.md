---
title: "graph-pipe: a read-only graph verb piped into a text filter, when magus projects the record itself"
description: "A deny rule by default: it refuses a read-only graph verb piped into a text filter, when magus projects the record itself, and names what to run instead."
tags: [guard, rules, graph-pipe, deny]
---

# graph-pipe

A deny rule by default: it refuses a read-only graph verb piped into a text filter, when magus projects the record itself, and names what to run instead.

## What it catches

A read-only graph verb piped into a text filter, when magus projects the record itself.

## Why

The same answer output-pipe gives: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field. It is its own rule on `refs`, `query`, `explain` and `describe` because they change nothing and their pipe loses no failure, so a workspace whose readers go back to `grep -rn` when refused can set this one to advise and keep output-pipe denying.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"graph-pipe": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [graph-pipe]: ...
```

A workspace that sets this rule to `advise` sees `advise [graph-pipe]: ...` instead.

`magus describe rule graph-pipe` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

---
title: "output-pipe: magus output piped into a filter, when magus projects the record itself"
description: "An advisory by default: it explains, and blocks nothing, on magus output piped into a filter, when magus projects the record itself."
tags: [guard, rules, output-pipe, advise]
---

# output-pipe

An advisory by default: it explains, and blocks nothing, on magus output piped into a filter, when magus projects the record itself.

## What it catches

Magus output piped into a filter, when magus projects the record itself.

## Why

magus projects its own record, so the filter is answering a question the command takes a flag for: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field, `-s` to silence progress. The half a reader cannot discover by trying again is the exit status: a pipe takes it from the LAST stage, so a failing magus reads as exit 0 and nothing says so. It denies on `run`, `affected`, `x` and every verb that is not a graph read. A read-only graph verb (`refs`, `query`, `explain`, `describe`) gets the same answer as the graph-pipe advisory, and a help request (`--help`, `-h`) passes: neither loses a failure.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"output-pipe": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [output-pipe]: ...
```

`magus describe rule output-pipe` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

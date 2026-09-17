---
title: "output-pipe: magus output piped into a filter, when magus projects the record itself"
description: "A deny rule: it refuses magus output piped into a filter, when magus projects the record itself, and names what to run instead."
tags: [guard, rules, output-pipe, deny]
---

# output-pipe

A deny rule: it refuses magus output piped into a filter, when magus projects the record itself, and names what to run instead.

## What it catches

Magus output piped into a filter, when magus projects the record itself.

## Why

magus projects its own record, so the filter is answering a question the command takes a flag for: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field, `-s` to silence progress. The half a reader cannot discover by trying again is the exit status: a pipe takes it from the LAST stage, so a failing magus reads as exit 0 and nothing says so.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [output-pipe]: ...
```

`magus describe rule output-pipe` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

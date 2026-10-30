---
title: "deviation-author: an agent adding a line to a `*.deviations.jsonl` ledger, in any spelling and any role"
description: "A deny rule by default: it refuses an agent adding a line to a `*.deviations.jsonl` ledger, in any spelling and any role, and names what to run instead."
tags: [guard, rules, deviation-author, deny]
---

# deviation-author

A deny rule by default: it refuses an agent adding a line to a `*.deviations.jsonl` ledger, in any spelling and any role, and names what to run instead.

## What it catches

An agent adding a line to a `*.deviations.jsonl` ledger, in any spelling and any role.

## Why

A line says a person decided a break is worth shipping, and no checker can judge that from its bytes. The failing gate prints the line, and a person fills in the reason and adds it. It is refused whoever asks, the root included. Name the line in your result, and a person adds it. Deleting an unused line is not refused, since the gate asks for it and it permits nothing. This is a seatbelt for a harness that opted in, not a boundary, and a person's merge is the check. The gate's verdict reads only the file's contents, so CI sees the same bytes whoever wrote them.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"deviation-author": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [deviation-author]: ...
```

A workspace that sets this rule to `advise` sees `advise [deviation-author]: ...` instead.

`magus describe rule deviation-author` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

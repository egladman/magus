---
title: "read-ack: an agent stamping a read receipt, which records that a PERSON read a change"
description: "A deny rule: it refuses an agent stamping a read receipt, which records that a PERSON read a change, and names what to run instead."
tags: [guard, rules, read-ack, deny]
---

# read-ack

A deny rule: it refuses an agent stamping a read receipt, which records that a PERSON read a change, and names what to run instead.

## What it catches

An agent stamping a read receipt, which records that a PERSON read a change.

## Why

This is not a permission an agent is missing: there is no spelling of it an agent may use, because an agent stamping the changeset would make the measure mean nothing for everybody, including the human relying on it. Report what is unread instead: `magus diff --impact` names every changed file carrying no receipt, and `magus diff -o json` puts read_state on each file for a caller to branch on.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [read-ack]: ...
```

`magus describe rule read-ack` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

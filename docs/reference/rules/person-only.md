---
title: "person-only: an agent stamping a read receipt or closing an attention request, each of which records that a PERSON did it"
description: "A deny rule: it refuses an agent stamping a read receipt or closing an attention request, each of which records that a PERSON did it, and names what to run instead."
tags: [guard, rules, person-only, deny]
---

# person-only

A deny rule: it refuses an agent stamping a read receipt or closing an attention request, each of which records that a PERSON did it, and names what to run instead.

## What it catches

An agent stamping a read receipt or closing an attention request, each of which records that a PERSON did it.

## Why

This is not a permission an agent is missing: there is no spelling of either an agent may use, because an agent stamping the changeset or closing its own block would make the measure mean nothing for everybody, including the human relying on it. Report what is unread instead: `magus diff --impact` names every changed file carrying no receipt, and `magus diff -o json` puts read_state on each file for a caller to branch on. Waiting on a request instead: say you are waiting on its id and hand it back; `magus session dispose <id>` is a person's to run.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [person-only]: ...
```

`magus describe rule person-only` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

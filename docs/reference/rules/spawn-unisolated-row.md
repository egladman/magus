---
title: "spawn-unisolated-row: an isolated spawn for a writing job whose fork made its checkout"
description: "A deny rule by default: it refuses an isolated spawn for a writing job whose fork made its checkout, and names what to run instead."
tags: [guard, rules, spawn-unisolated-row, deny]
---

# spawn-unisolated-row

A deny rule by default: it refuses an isolated spawn for a writing job whose fork made its checkout, and names what to run instead.

## What it catches

An isolated spawn for a writing job whose fork made its checkout.

## Why

`magus job exec` takes a job whose fork made a checkout there and nowhere else, so a worker the host isolates in a fresh checkout cannot take it, and the host refuses its edits in the made checkout. Host isolation is how a writing job gets a checkout of its own: a job with none, or with one its fork adopted, adopts the host's checkout at exec, so its spawn passes. Read-only jobs and spawns without isolation pass.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-unisolated-row": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-unisolated-row]: ...
```

A workspace that sets this rule to `advise` sees `advise [spawn-unisolated-row]: ...` instead.

`magus describe rule spawn-unisolated-row` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

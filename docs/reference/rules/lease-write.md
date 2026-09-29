---
title: "lease-write: a leased write outside its write paths, or into a path it was denied or another lease owns"
description: "A deny rule: it refuses a leased write outside its write paths, or into a path it was denied or another lease owns, and names what to run instead."
tags: [guard, rules, lease-write, deny]
---

# lease-write

A deny rule: it refuses a leased write outside its write paths, or into a path it was denied or another lease owns, and names what to run instead.

## What it catches

A leased write outside its write paths, or into a path it was denied or another lease owns.

## Why

The boundary is the orchestrator's declaration in the job store; the guard reads it back on both surfaces, a file write and a shell line, in the same words. A leased write before the job has reported the base it landed on is refused under the same name, since nothing yet records which revision the work applies to.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-write]: ...
```

`magus describe rule lease-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

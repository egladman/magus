---
title: "capture-filter: a filter over a run capture or log, which cuts the failure block apart"
description: "A deny rule: it refuses a filter over a run capture or log, which cuts the failure block apart, and names what to run instead."
tags: [guard, rules, capture-filter, deny]
---

# capture-filter

A deny rule: it refuses a filter over a run capture or log, which cuts the failure block apart, and names what to run instead.

## What it catches

A filter over a run capture or log, which cuts the failure block apart.

## Why

A failure prints five lines together: the target, the cause, an output ref, the command that reads that ref, and the command to reproduce it. A filter keeps the one line it matched and drops the rest, so `grep 'cause:'` keeps the symptom and discards the ref that reads the whole log two lines below it. A range print (`sed -n '1,200p'`) is a filter too: it cuts by POSITION, and the block sits wherever the run left it. Read the file whole, or give the run an output contract up front with `-o jsonl --tee <file>` and query that. Reading the whole file is not a filter and stays allowed.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [capture-filter]: ...
```

`magus describe rule capture-filter` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

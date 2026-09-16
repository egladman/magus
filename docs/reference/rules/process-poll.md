---
title: "process-poll: a process table inspected to wait on magus work the lock already reports"
description: "A deny rule: it refuses a process table inspected to wait on magus work the lock already reports, and names what to run instead."
tags: [guard, rules, process-poll, deny]
---

# process-poll

A deny rule: it refuses a process table inspected to wait on magus work the lock already reports, and names what to run instead.

## What it catches

A process table inspected to wait on magus work the lock already reports.

## Why

A magus run holds a project lock and announces itself, and `magus status --watch=15s` reads that same lock state continuously: holder PID, command, age, waiters. `pgrep`, `pidof` and `ps` invent a second waiter that races the real one, has no bound of its own, and answers a question the lock message already answered.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [process-poll]: ...
```

`magus describe rule process-poll` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

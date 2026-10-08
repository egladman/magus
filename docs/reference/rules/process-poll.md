---
title: "process-poll: a process table inspected to wait on magus work the lock already reports"
description: "An advisory by default: it explains, and blocks nothing, on a process table inspected to wait on magus work the lock already reports."
tags: [guard, rules, process-poll, advise]
---

# process-poll

An advisory by default: it explains, and blocks nothing, on a process table inspected to wait on magus work the lock already reports.

## What it catches

A process table inspected to wait on magus work the lock already reports.

## Why

A magus run holds a project lock and announces itself, and `magus status --watch=15s` reads that same lock state continuously: holder PID, command, age. `pgrep`, `pidof` and `ps` invent a poll with no bound of its own that answers a question the lock message already answered.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"process-poll": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [process-poll]: ...
```

`magus describe rule process-poll` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

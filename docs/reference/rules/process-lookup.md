---
title: "process-lookup: pgrep or pidof used to look up a process by name"
description: "A deny rule by default: it refuses pgrep or pidof used to look up a process by name, and names what to run instead."
tags: [guard, rules, process-lookup, deny]
---

# process-lookup

A deny rule by default: it refuses pgrep or pidof used to look up a process by name, and names what to run instead.

## What it catches

Pgrep or pidof used to look up a process by name.

## Why

A name cannot tell your process from another session's, and the lookup fails on this host as operation not permitted. Wait on the host's background-task notification for work you started, `magus server status` for magus runs, or `magus job wait <job>` for a job.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"process-lookup": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [process-lookup]: ...
```

A workspace that sets this rule to `advise` sees `advise [process-lookup]: ...` instead.

`magus describe rule process-lookup` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

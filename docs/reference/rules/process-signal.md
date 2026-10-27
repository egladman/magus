---
title: "process-signal: kill, pkill or killall used to signal a process identified only by PID or name"
description: "A deny rule by default: it refuses kill, pkill or killall used to signal a process identified only by PID or name, and names what to run instead."
tags: [guard, rules, process-signal, deny]
---

# process-signal

A deny rule by default: it refuses kill, pkill or killall used to signal a process identified only by PID or name, and names what to run instead.

## What it catches

Kill, pkill or killall used to signal a process identified only by PID or name.

## Why

A PID or process-name pattern cannot establish ownership: a pkill of a slow fork or MCP process can stop another session's work or the host's connection. Cancel the shell task through the host that started it, or have the person stop it. `magus status` inspects the tracked work without signaling it. Signal-list requests pass; `kill -0` is a process-poll instead.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"process-signal": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [process-signal]: ...
```

A workspace that sets this rule to `advise` sees `advise [process-signal]: ...` instead.

`magus describe rule process-signal` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

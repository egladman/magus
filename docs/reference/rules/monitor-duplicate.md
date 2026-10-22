---
title: "monitor-duplicate: a second live watcher on one subject, or one on work the agent backgrounded itself"
description: "A deny rule by default: it refuses a second live watcher on one subject, or one on work the agent backgrounded itself, and names what to run instead."
tags: [guard, rules, monitor-duplicate, deny]
---

# monitor-duplicate

A deny rule by default: it refuses a second live watcher on one subject, or one on work the agent backgrounded itself, and names what to run instead.

## What it catches

A second live watcher on one subject, or one on work the agent backgrounded itself.

## Why

A watcher is a host tool that runs a command in the background and wakes the agent on each line it prints, and again when it expires. Its subject is what its command reads: a process pattern, a pid, a file, a job, the project lock, a CI run; a command naming none is its own subject. A second watcher while the first is live wakes the agent twice per event; one on a host task capture, or on the process of a command this agent backgrounded, repeats the notice the host already gives. A backgrounded call that watches without end (`magus status --watch`, `tail -f`, `watch`) is judged the same way, and a leased worker is refused one outright, since its waits are its own runs. Re-arming a watcher that expired passes. It cannot tell a process pattern that also matches someone else's run from one matching only the agent's own.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"monitor-duplicate": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [monitor-duplicate]: ...
```

A workspace that sets this rule to `advise` sees `advise [monitor-duplicate]: ...` instead.

`magus describe rule monitor-duplicate` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

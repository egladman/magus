---
title: "background-left: an agent finishing while shell commands it ran in the background are still running"
description: "A deny rule by default: it refuses an agent finishing while shell commands it ran in the background are still running, and names what to run instead."
tags: [guard, rules, background-left, deny]
---

# background-left

A deny rule by default: it refuses an agent finishing while shell commands it ran in the background are still running, and names what to run instead.

## What it catches

An agent finishing while shell commands it ran in the background are still running.

## Why

A host does not stop a finished subagent's background tasks, and once the subagent has reported nobody is told when one ends or that it is still running: a transcript search left behind by a scout ran for four hours. At a subagent's stop magus reads the subagent's own transcript for the shell commands it ran in the background (run_in_background, a foreground call the host moved to the background, a watcher's command) that no completion notice, stop call or watcher expiry has ended, and refuses the stop naming each by id, start time, age and command. A run under `magus job exec --timeout`, which check-timeout sends a worker to background and which stops at its deadline, and a commit the host moved to the background while its hooks run, end on their own: their completion notice is how the agent hears the result, so neither holds a stop. A host that resumes a stopped subagent when its background task completes declares the first version that does, and magus reads the version from the transcript, since the hook input carries none. At or past it the stop stands with advice naming each task, and the subagent keeps its job instead of ending it no_return. Below it, with no version recorded, or with none declared, the stop is refused and the refusal says why. At a root session's stop it advises instead, and only of tasks older than 30 minutes: a root wakes on a task's completion notice, so a young task is one it is waiting on, and the person is there to decide. Reads the host's transcript by shape, so a host whose transcript records no background task in that shape is never refused. Set to advise, a subagent's stop is advised too.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"background-left": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [background-left]: ...
```

A workspace that sets this rule to `advise` sees `advise [background-left]: ...` instead.

`magus describe rule background-left` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

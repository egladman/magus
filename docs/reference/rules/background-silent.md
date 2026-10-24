---
title: "background-silent: a background task of the session that has written nothing for advise_minutes (30 unset)"
description: "A deny rule by default: it refuses a background task of the session that has written nothing for advise_minutes (30 unset), and names what to run instead."
tags: [guard, rules, background-silent, deny]
---

# background-silent

A deny rule by default: it refuses a background task of the session that has written nothing for advise_minutes (30 unset), and names what to run instead.

## What it catches

A background task of the session that has written nothing for advise_minutes (30 unset).

## Why

A background task's output file and event notices are the only sign it is working, and one that writes nothing for half an hour is more likely waiting on a stdin nobody feeds, a lock or a prompt than doing slow work: a `python3 -` reading an unfed stdin sat for eight hours that way. On each shell call of a root session magus reads the session's transcript for the background tasks still running, as background-left does, and measures each one's silence from the latest of its start, its output file's last write and its newest event notice. It reads only the bytes written since its last call, resuming from a checkpoint in the workspace cache, so a transcript of hundreds of megabytes costs a hook call no more than a small one. Past 30 minutes of silence the call is advised, once per task per session; past 60 minutes every shell call that is not a read is refused, naming each task's id, age, silence, command, output file and the TaskStop call that ends it. magus never stops a task itself. The text says what a stop loses: only the process; the output file stays on disk, reads still pass, and a magus run keeps its output ref and history. It names how to pick the work back up, bounded at background-unbounded's ceiling. advise_minutes and deny_minutes set the two thresholds (30 and 60 unset); deny_minutes must be more than advise_minutes. A watcher with an expiry is not judged, since the host ends it. A subagent's tasks are background-left's at its stop. The host's hook input carries no list of background tasks, so the transcript is the record read.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"background-silent": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [background-silent]: ...
```

A workspace that sets this rule to `advise` sees `advise [background-silent]: ...` instead.

`magus describe rule background-silent` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

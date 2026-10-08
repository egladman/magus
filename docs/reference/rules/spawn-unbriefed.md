---
title: "spawn-unbriefed: a subagent spawned before the multi-agent skill loaded"
description: "An advisory by default: it explains, and blocks nothing, on a subagent spawned before the multi-agent skill loaded."
tags: [guard, rules, spawn-unbriefed, advise]
---

# spawn-unbriefed

An advisory by default: it explains, and blocks nothing, on a subagent spawned before the multi-agent skill loaded.

## What it catches

A subagent spawned before the multi-agent skill loaded.

## Why

Four decisions a spawn cannot be corrected for later are made before the child starts: which worktree it is cut from, which lease grades its writes, which model it runs, and that git stays with the orchestrator. The magus-multi-agent skill carries all four. A skill nobody is required to read is a skill nobody reads, so the spawn waits on the load instead of trusting it. It grades the session, never the prompt: it asks whether a marker file exists, so a handed-over prompt that merely mentions a denied command is untouched. Load Skill(magus-multi-agent) once and every later spawn in the session passes.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-unbriefed": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [spawn-unbriefed]: ...
```

`magus describe rule spawn-unbriefed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

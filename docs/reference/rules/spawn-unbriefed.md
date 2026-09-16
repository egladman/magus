---
title: "spawn-unbriefed: a subagent spawned before the multi-agent skill loaded"
description: "A deny rule: it refuses a subagent spawned before the multi-agent skill loaded, and names what to run instead."
tags: [guard, rules, spawn-unbriefed, deny]
---

# spawn-unbriefed

A deny rule: it refuses a subagent spawned before the multi-agent skill loaded, and names what to run instead.

## What it catches

A subagent spawned before the multi-agent skill loaded.

## Why

Four decisions a spawn cannot be corrected for later are made before the child starts: which worktree it is cut from, which lease grades its writes, which model it runs, and that git stays with the orchestrator. The magus-multi-agent skill carries all four. Measured across 2,147 session transcripts: zero loads, under every name and every variant, while the two skills a hook DEMANDS loaded 415 times. A skill nobody is required to read is a skill nobody reads, and this workspace had already written that down before measuring it again here. It grades the session, never the prompt: it asks whether a marker file exists, so a handed-over prompt that merely mentions a denied command is untouched. Load Skill(magus-multi-agent) once and every later spawn in the session passes.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-unbriefed]: ...
```

`magus describe rule spawn-unbriefed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

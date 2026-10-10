---
title: "architecture-unbriefed: an agent's next call after a structure question, or a new directory, before the architecture skill"
description: "An advisory by default: it explains, and blocks nothing, on an agent's next call after a structure question, or a new directory, before the architecture skill."
tags: [guard, rules, architecture-unbriefed, advise]
---

# architecture-unbriefed

An advisory by default: it explains, and blocks nothing, on an agent's next call after a structure question, or a new directory, before the architecture skill.

## What it catches

An agent's next call after a structure question, or a new directory, before the architecture skill.

## Why

A structure question is answered from the workspace's own edges: what imports what, where a cycle closes, which layer a package sits in, how far a change reaches. An agent reading files one at a time answers it from the files it happened to open, and proposes a boundary nobody needed. The magus-architecture-review skill is how the graph gets asked. Two acts arm it. A prompt submitted with structure vocabulary (architecture, boundaries, layering, coupling, imports, a new package, where something belongs, blast radius) marks the session, and the agent's next graded call (a shell line, a read, a search, an edit, a magus tool call) waits on the load. A write that creates a new directory waits on it too, since that write draws a boundary whatever was asked. A load counts for the agent that made it: a subagent in that session loads the skill itself, and a parent's load does not brief its child. Load Skill(magus-architecture-review), or its -full twin, once and the agent's later calls pass. It stands down on a host whose wiring does not report skill loads, where nothing could ever clear it; the new-directory advisory still speaks there.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"architecture-unbriefed": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [architecture-unbriefed]: ...
```

`magus describe rule architecture-unbriefed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

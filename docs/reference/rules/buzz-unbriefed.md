---
title: "buzz-unbriefed: the first write to a .buzz file in a session that has not read the Buzz skill"
description: "A deny rule: it refuses the first write to a .buzz file in a session that has not read the Buzz skill, and names what to run instead."
tags: [guard, rules, buzz-unbriefed, deny]
---

# buzz-unbriefed

A deny rule: it refuses the first write to a .buzz file in a session that has not read the Buzz skill, and names what to run instead.

## What it catches

The first write to a .buzz file in a session that has not read the Buzz skill.

## Why

Buzz is in no model's training data, so what gets written is Go or TypeScript with the serial numbers filed off, and enough of it parses to reach review. Six errors in one session, by an agent with this repository open throughout: fs\glob indexed as strings when it returns [Path]; .append on a list declared without mut; the ternary form, which upstream-strict parsing rejects outside --embedded; archive\extract, which does not exist; a missing `import "fs"`; and .sub sliced by character on BYTE-indexed strings. Reading first supplies every one of them. It grades the session, not the file: one Skill(magus-buzz-write) and every later Buzz write passes. Reads are never gated, since reading is how the language gets learned.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [buzz-unbriefed]: ...
```

`magus describe rule buzz-unbriefed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

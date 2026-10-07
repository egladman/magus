---
title: "buzz-unbriefed: the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill"
description: "An advisory by default: it explains, and blocks nothing, on the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill."
tags: [guard, rules, buzz-unbriefed, advise]
---

# buzz-unbriefed

An advisory by default: it explains, and blocks nothing, on the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill.

## What it catches

The first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill.

## Why

Buzz is in no model's training data, so what gets written is Go or TypeScript with the serial numbers filed off, and enough of it parses to reach review. Typical errors of an agent with this repository open throughout: fs\glob indexed as strings when it returns [Path]; .append on a list declared without mut; the ternary form, which upstream-strict parsing rejects outside --embedded; archive\extract, which does not exist; a missing `import "fs"`; and .sub sliced by character on BYTE-indexed strings. Reading first supplies every one of them. It grades the session, not the file: one Skill(magus-buzz-lang) and every later Buzz write passes. Reads are never gated, since reading is how the language gets learned, so `magus buzz <file>` and `magus buzz -t <file>` run something that already exists and go untouched.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"buzz-unbriefed": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [buzz-unbriefed]: ...
```

`magus describe rule buzz-unbriefed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

---
title: "stale-binary: a verdict from a binary older than the rules in the tree around it"
description: "An advisory: it explains, and blocks nothing, on a verdict from a binary older than the rules in the tree around it."
tags: [guard, rules, stale-binary, advise]
---

# stale-binary

An advisory: it explains, and blocks nothing, on a verdict from a binary older than the rules in the tree around it.

## What it catches

A verdict from a binary older than the rules in the tree around it.

## Why

The failure this catches is silent and convincing: change a rule, run the guard, read `pass`, conclude the rule does not work, when what answered was the previous build. That happened twice in one session, both times because a rebuild had been skipped without anyone noticing. It is appended to a DENY every time rather than held, because a refusal from rules the caller has already changed is the case it exists for.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [stale-binary]: ...
```

`magus describe rule stale-binary` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

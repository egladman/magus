---
title: "chained-run: several magus runs chained on one line, where the dependency graph would have run them"
description: "An advisory: it explains, and blocks nothing, on several magus runs chained on one line, where the dependency graph would have run them."
tags: [guard, rules, chained-run, advise]
---

# chained-run

An advisory: it explains, and blocks nothing, on several magus runs chained on one line, where the dependency graph would have run them.

## What it catches

Several magus runs chained on one line, where the dependency graph would have run them.

## Why

Targets compose through ctx.needs, so the last one usually pulls the rest in order and each extra invocation reloads the workspace. It ADVISES rather than refuses because two genuinely independent targets on one line are real work, and only the graph knows which case this is. The exception worth knowing: `affected ci` does not regenerate. Where the gate strips the workspace's default charms, its composed `generate` is a drift gate, so `affected generate:rw` comes first as its own invocation.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [chained-run]: ...
```

`magus describe rule chained-run` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

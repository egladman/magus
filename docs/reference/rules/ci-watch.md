---
title: "ci-watch: a `gh` invocation that BLOCKS until CI finishes, rather than asking once"
description: "A deny rule: it refuses a `gh` invocation that BLOCKS until CI finishes, rather than asking once, and names what to run instead."
tags: [guard, rules, ci-watch, deny]
---

# ci-watch

A deny rule: it refuses a `gh` invocation that BLOCKS until CI finishes, rather than asking once, and names what to run instead.

## What it catches

A `gh` invocation that BLOCKS until CI finishes, rather than asking once.

## Why

Watching costs a wake-up per completion and buys nothing, because GREEN CHANGES NOTHING: a person merges, not the watcher. Measured in one session: four watches, every one green, every one a turn spent re-reading a verdict that was already true. It is also the second half of a duplicate, since a gate already run locally is the same command on the same tree, and waiting for CI to agree pays twice for one answer. `gh pr list --state open --json number,mergeable,statusCheckRollup` answers every open pull request in one call. Iterating on a run that is already RED is the case worth following, and polling that command serves it too.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [ci-watch]: ...
```

`magus describe rule ci-watch` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "stage-all: `git add -A`, which sweeps regenerated output into a commit about something else"
description: "A deny rule: it refuses `git add -A`, which sweeps regenerated output into a commit about something else, and names what to run instead."
tags: [guard, rules, stage-all, deny]
---

# stage-all

A deny rule: it refuses `git add -A`, which sweeps regenerated output into a commit about something else, and names what to run instead.

## What it catches

`git add -A`, which sweeps regenerated output into a commit about something else.

## Why

A magus target writes its declared outputs as it runs, so the tree here is routinely dirty with files you did not edit. `-A` sweeps those and any build residue into a commit about something else, with no signal that it happened. Measured: one such call put 69 files, a whole regenerated docs site plus five untouched source files, into a commit about four collection methods. `magus vcs add` classifies every dirty path against the declared output globs, keeps a source change and the outputs it produced together, and reports anything undeclared instead of staging it.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [stage-all]: ...
```

`magus describe rule stage-all` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

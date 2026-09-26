---
title: "whole-tree: a whole-tree VCS reset, checkout, restore or clean, which cannot be undone"
description: "A deny rule: it refuses a whole-tree VCS reset, checkout, restore or clean, which cannot be undone, and names what to run instead."
tags: [guard, rules, whole-tree, deny]
---

# whole-tree

A deny rule: it refuses a whole-tree VCS reset, checkout, restore or clean, which cannot be undone, and names what to run instead.

## What it catches

A whole-tree VCS reset, checkout, restore or clean, which cannot be undone.

## Why

These destroy uncommitted and untracked work across the WHOLE tree, including a concurrent session's, and nothing recorded anywhere can give it back. It is the one category where an over-eager refusal is the safe direction, which is why an unparsable line falls back to the pattern rather than passing. Verify in place instead: no magus run needs a clean tree. git's own help passes, because git documents that it prints usage without running: `git stash --help`, `git reset -h`, `git help stash`. It has to be the whole line, with nothing between the verb and the flag, so `git reset --hard --help`, `git -c ... stash --help`, a `VAR=value` prefix, `sh -c` or a pipe are judged as work.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [whole-tree]: ...
```

`magus describe rule whole-tree` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "worktree-remove: removing a worktree magus cannot prove holds nothing that would be lost"
description: "A deny rule by default: it refuses removing a worktree magus cannot prove holds nothing that would be lost, and names what to run instead."
tags: [guard, rules, worktree-remove, deny]
---

# worktree-remove

A deny rule by default: it refuses removing a worktree magus cannot prove holds nothing that would be lost, and names what to run instead.

## What it catches

Removing a worktree magus cannot prove holds nothing that would be lost.

## Why

A worktree may be where another session is working right now, and what it holds may exist nowhere else. `git worktree remove` passes only when every condition holds: the path is a linked worktree of this repository (per `git worktree list`), not the main one and not the checkout the session runs in; it has no modified, staged or untracked files (ignored files do not count); every commit its HEAD carries is on a remote-tracking ref or the base branch, or every path those commits changed reads the same in the HEAD of the session's own checkout, so a cherry-pick under a new id counts, whatever state the job taken in it ended in; no live job was taken in it, and an exited one serves `magus job wait <id>`; and it is not locked, unless the session is the root and spawned that worktree's worker for a job that ended pass or fail, as its own spawn record shows. That lock is the one thing `-f -f` reaches past, and a plain removal of such a worktree is served the `git worktree remove -f -f <path>` line; a `magus checkout` lock never is. `git worktree unlock` is judged by the same conditions but the lock, since an unlocked worktree has nothing else stopping its removal. The refusal names each failed condition and the command that inspects it, and `--force` changes nothing else. Removal cannot be undone, so a fact that cannot be read refuses too: an unreadable job store, a line that does not parse, a path that is not a literal word, a removal inside a conditional or behind a wrapper. `git worktree prune` passes, since it only clears records of directories already gone, and `git worktree remove --help` and `-h`, alone on the line, print usage and pass. `jj workspace forget` stays refused: the jj driver cannot yet report a workspace's registration or which of its commits are published, so nothing proves a forget loses nothing.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"worktree-remove": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [worktree-remove]: ...
advise [worktree-remove]: ...
```

`magus describe rule worktree-remove` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

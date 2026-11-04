---
title: "conflict-commit: a `git commit` recording a merge conflict marker on a line it adds"
description: "A deny rule by default: it refuses a `git commit` recording a merge conflict marker on a line it adds, and names what to run instead."
tags: [guard, rules, conflict-commit, deny]
---

# conflict-commit

A deny rule by default: it refuses a `git commit` recording a merge conflict marker on a line it adds, and names what to run instead.

## What it catches

A `git commit` recording a merge conflict marker on a line it adds.

## Why

Staging a file marks it resolved, so git commits one still holding hunks, and after that every checkout carries them. It reads what the commit would record through `git diff --check`: the index, or the working tree for `-a`, named paths and an `add` earlier on the line. Only added lines count, so a marker already committed charges nobody, and a fixture that must hold one sets git's `conflict-marker-size` attribute.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"conflict-commit": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [conflict-commit]: ...
```

A workspace that sets this rule to `advise` sees `advise [conflict-commit]: ...` instead.

`magus describe rule conflict-commit` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

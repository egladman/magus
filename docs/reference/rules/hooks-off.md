---
title: "hooks-off: a git call with the repository's hooks switched off"
description: "A deny rule by default: it refuses a git call with the repository's hooks switched off, and names what to run instead."
tags: [guard, rules, hooks-off, deny]
---

# hooks-off

A deny rule by default: it refuses a git call with the repository's hooks switched off, and names what to run instead.

## What it catches

A git call with the repository's hooks switched off.

## Why

Hooks are the checks a repository puts in front of a commit or push, and `--no-verify`, `git commit -n` and `-c core.hooksPath=/dev/null` (or an empty path) skip every one of them. A hooks path naming a directory is a different set of hooks rather than none, so it passes; `-n` counts only on commit, where it means `--no-verify`. It cannot tell a repository with no hooks installed, where the flag skips nothing, from one where it skips them all.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"hooks-off": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [hooks-off]: ...
```

`magus describe rule hooks-off` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

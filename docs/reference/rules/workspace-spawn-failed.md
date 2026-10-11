---
title: "workspace-spawn-failed: a spawn the workspace's rule could not judge, so only the built-in rules graded it"
description: "An advisory by default: it explains, and blocks nothing, on a spawn the workspace's rule could not judge, so only the built-in rules graded it."
tags: [guard, rules, workspace-spawn-failed, advise]
---

# workspace-spawn-failed

An advisory by default: it explains, and blocks nothing, on a spawn the workspace's rule could not judge, so only the built-in rules graded it.

## What it catches

A spawn the workspace's rule could not judge, so only the built-in rules graded it.

## Why

A workspace spawn rule that fails to load or errors judges nothing, and a spawn or continuation reaching a verdict without it must say so rather than read as fully judged. The first notice in a session names each failing side and its error; a repeat is one line naming what applied. It stands alone only when the built-in rules passed the call; on any other verdict the note is appended to that verdict. A workspace cannot set it: magus\guard.builtins refuses the name, since the notice is how the workspace learns its own rule judged nothing.

## Default and override

This rule always takes the decision `advise`. A workspace cannot set it:
naming it in magus\guard.builtins fails to load, because the rule is how a workspace
learns that a rule of its own judged nothing.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [workspace-spawn-failed]: ...
```

`magus describe rule workspace-spawn-failed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

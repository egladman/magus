---
title: "brief-plain-commands: a brief for an isolated worker that does not hold it to one plain command per call"
description: "An advisory by default: it explains, and blocks nothing, on a brief for an isolated worker that does not hold it to one plain command per call."
tags: [guard, rules, brief-plain-commands, advise]
---

# brief-plain-commands

An advisory by default: it explains, and blocks nothing, on a brief for an isolated worker that does not hold it to one plain command per call.

## What it catches

A brief for an isolated worker that does not hold it to one plain command per call.

## Why

A host that runs a worker in a worktree of its own refuses each command it cannot prove stays inside that worktree, and a substitution, a loop or an exec is one it cannot. So a spawn into its own worktree must say "one plain command per call; no `$()`, loops or exec" (any wording naming one command per call, or plain commands), and must not itself show a command of those shapes. A spawn into a shared checkout passes. It cannot tell a brief that says the same thing in other words from one that never says it.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"brief-plain-commands": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [brief-plain-commands]: ...
```

A workspace that sets this rule to `deny` sees `deny [brief-plain-commands]: ...` instead.

`magus describe rule brief-plain-commands` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

---
title: "brief-plain-commands: a brief for an isolated worker that teaches a substitution, a loop or an exec"
description: "A deny rule by default: it refuses a brief for an isolated worker that teaches a substitution, a loop or an exec, and names what to run instead."
tags: [guard, rules, brief-plain-commands, deny]
---

# brief-plain-commands

A deny rule by default: it refuses a brief for an isolated worker that teaches a substitution, a loop or an exec, and names what to run instead.

## What it catches

A brief for an isolated worker that teaches a substitution, a loop or an exec.

## Why

A host that runs a worker in a worktree of its own refuses each command it cannot prove stays inside that worktree, and a substitution, a loop or an exec is one it cannot. A worker writes its commands the way its brief shows them, so a spawn into its own worktree must not show a command of those shapes. A brief need not say how to write commands: the host's refusal teaches that. A spawn into a shared checkout passes.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"brief-plain-commands": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [brief-plain-commands]: ...
```

A workspace that sets this rule to `advise` sees `advise [brief-plain-commands]: ...` instead.

`magus describe rule brief-plain-commands` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

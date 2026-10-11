---
title: "conflict-rewrite: an inline interpreter rewriting a file that holds merge conflict markers"
description: "A deny rule by default: it refuses an inline interpreter rewriting a file that holds merge conflict markers, and names what to run instead."
tags: [guard, rules, conflict-rewrite, deny]
---

# conflict-rewrite

A deny rule by default: it refuses an inline interpreter rewriting a file that holds merge conflict markers, and names what to run instead.

## What it catches

An inline interpreter rewriting a file that holds merge conflict markers.

## Why

A script that opens a file for writing truncates it there, so one that raises before it writes leaves the file empty, and mid-merge the working copy is the only copy of the half-resolved file. It denies by default because the policy that would raise interpreter-rewrite to deny is often what fails to load mid-merge, its own files conflicted too. It fires on two shapes: a write whose destination, read the way interpreter-rewrite reads one, is a file in the workspace holding conflict markers; and a write whose destination cannot be read at all, from a program that names such a file or carries a conflict marker itself, which is resolving hunks by script. A destination under a scratch path, a new file, and a program that only reads are untouched. Your editor tool reads the file first and reports what it changed; where the workspace carries a script that resolves hunks, the verdict names it.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"conflict-rewrite": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [conflict-rewrite]: ...
```

`magus describe rule conflict-rewrite` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

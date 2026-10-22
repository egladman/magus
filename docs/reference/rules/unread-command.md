---
title: "unread-command: a program or VCS subcommand the shell computes before it runs, which no rule can read"
description: "A deny rule by default: it refuses a program or VCS subcommand the shell computes before it runs, which no rule can read, and names what to run instead."
tags: [guard, rules, unread-command, deny]
---

# unread-command

A deny rule by default: it refuses a program or VCS subcommand the shell computes before it runs, which no rule can read, and names what to run instead.

## What it catches

A program or VCS subcommand the shell computes before it runs, which no rule can read.

## Why

Every shell rule reads the program and its subcommand as written, and the shell rewrites some words first: `\git` and `g\it` drop the escape, `$'g\x69t'` decodes it, `{git,push}` and `gi?` expand, `$x`, `${x}` and `$(echo git)` substitute. Each runs a command none of the rules judged. Spell the command plainly. Quotes are fine, since removing them changes nothing (`"git"`, `'git'`, `$'git'`), and so is an expansion in an argument (`git commit -m "$msg"`, `cd "$dir"`): only the words that pick what runs are read. A wrapper's program (`env \git`), a `sh -c` script and an `eval` are read the same way.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"unread-command": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [unread-command]: ...
```

A workspace that sets this rule to `advise` sees `advise [unread-command]: ...` instead.

`magus describe rule unread-command` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

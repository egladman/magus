---
title: "cd: a `cd` before a magus command, when the project is an argument"
description: "A deny rule: it refuses a `cd` before a magus command, when the project is an argument, and names what to run instead."
tags: [guard, rules, cd, deny]
---

# cd

A deny rule: it refuses a `cd` before a magus command, when the project is an argument, and names what to run instead.

## What it catches

A `cd` before a magus command, when the project is an argument.

## Why

magus is CWD-relative, so a leading `cd` is how the right command lands on the wrong project. The project is an argument and is written bare (`magus run build libs/foo`); a DIFFERENT workspace is `--root <path>`, and `magus where <name>` resolves a fuzzy name. A `cd` prefix also relocates every later command on the line and re-fires shell chpwd hooks, mise among them, which can fail on an empty command. A host shell tool that genuinely needs a different directory for one call has a working_directory field, which does not rewrite the command line.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [cd]: ...
```

`magus describe rule cd` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

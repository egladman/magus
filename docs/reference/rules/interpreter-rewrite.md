---
title: "interpreter-rewrite: an inline interpreter rewriting a file this tree already carries"
description: "A deny rule: it refuses an inline interpreter rewriting a file this tree already carries, and names what to run instead."
tags: [guard, rules, interpreter-rewrite, deny]
---

# interpreter-rewrite

A deny rule: it refuses an inline interpreter rewriting a file this tree already carries, and names what to run instead.

## What it catches

An inline interpreter rewriting a file this tree already carries.

## Why

A `python -c` or `node -e` that reads a tracked file, substitutes, and writes it back is an edit nobody reviewed: it lands before a diff exists, and the script that produced it is gone the moment the line ends. The editor tool reads the file first and reports what it changed, which is the same edit with a record of itself. It fires on the WRITE, not the interpreter: a one-liner that computes something, prints it, or creates a file the tree does not carry is untouched, and so is anything under a scratch path.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [interpreter-rewrite]: ...
```

`magus describe rule interpreter-rewrite` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

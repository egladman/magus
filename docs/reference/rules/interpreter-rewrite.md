---
title: "interpreter-rewrite: an inline interpreter rewriting a file this tree already carries"
description: "An advisory by default: it explains, and blocks nothing, on an inline interpreter rewriting a file this tree already carries."
tags: [guard, rules, interpreter-rewrite, advise]
---

# interpreter-rewrite

An advisory by default: it explains, and blocks nothing, on an inline interpreter rewriting a file this tree already carries.

## What it catches

An inline interpreter rewriting a file this tree already carries.

## Why

A `python -c` or `node -e` that reads a tracked file, substitutes, and writes it back is an edit nobody reviewed: it lands before a diff exists, and the script that produced it is gone the moment the line ends. The editor tool reads the file first and reports what it changed, which is the same edit with a record of itself. It fires on the WRITE, not the interpreter: a one-liner that computes something, prints it, or creates a file the tree does not carry is untouched, and so is anything under a scratch path. Only a write's destination counts: the path an `open(..., 'w')`, a pathlib or `writeFile` writer, an in-place flag or an awk redirect names, a variable read through its assignment. A tracked path the program carries as data, in a list it prints to stdout or a report it writes to scratch, is not one. A destination spelled from no literal at all, such as argv, is read as the interpreter's operands.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"interpreter-rewrite": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [interpreter-rewrite]: ...
```

`magus describe rule interpreter-rewrite` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

---
title: "copy-write: a file copied into the workspace from outside it, judged as the file write it is"
description: "A deny rule by default: it refuses a file copied into the workspace from outside it, judged as the file write it is, and names what to run instead."
tags: [guard, rules, copy-write, deny]
---

# copy-write

A deny rule by default: it refuses a file copied into the workspace from outside it, judged as the file write it is, and names what to run instead.

## What it catches

A file copied into the workspace from outside it, judged as the file write it is.

## Why

`cp`, `mv` and `install` from a scratch file into the workspace write that file's bytes the way a host's write tool does, so the source is read and judged as that write: the comment, lease and workspace write rules hold on either channel. A refused write refuses the line under this name, with the write rule's own in brackets. A source over 1 MiB, or one that cannot be read, is refused, since its bytes cannot be judged; a binary source is judged by its destination alone. A source already inside the workspace met the write rules on its way in and is not read again. It cannot read a source a variable or substitution names.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"copy-write": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [copy-write]: ...
```

A workspace that sets this rule to `advise` sees `advise [copy-write]: ...` instead.

`magus describe rule copy-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

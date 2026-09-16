---
title: "new-source-dir: a new file that opens a directory, which is a boundary rather than a file"
description: "An advisory: it explains, and blocks nothing, on a new file that opens a directory, which is a boundary rather than a file."
tags: [guard, rules, new-source-dir, advise]
---

# new-source-dir

An advisory: it explains, and blocks nothing, on a new file that opens a directory, which is a boundary rather than a file.

## What it catches

A new file that opens a directory, which is a boundary rather than a file.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [new-source-dir]: ...
```

`magus describe rule new-source-dir` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

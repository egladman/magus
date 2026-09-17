---
title: "new-file: a new file in a directory whose naming has settled"
description: "An advisory: it explains, and blocks nothing, on a new file in a directory whose naming has settled."
tags: [guard, rules, new-file, advise]
---

# new-file

An advisory: it explains, and blocks nothing, on a new file in a directory whose naming has settled.

## What it catches

A new file in a directory whose naming has settled.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [new-file]: ...
```

`magus describe rule new-file` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

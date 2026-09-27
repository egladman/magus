---
title: "read-symbol: a bounded read inside one indexed declaration, which refs --definition --source prints checked"
description: "An advisory: it explains, and blocks nothing, on a bounded read inside one indexed declaration, which refs --definition --source prints checked."
tags: [guard, rules, read-symbol, advise]
---

# read-symbol

An advisory: it explains, and blocks nothing, on a bounded read inside one indexed declaration, which refs --definition --source prints checked.

## What it catches

A bounded read inside one indexed declaration, which refs --definition --source prints checked.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [read-symbol]: ...
```

`magus describe rule read-symbol` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

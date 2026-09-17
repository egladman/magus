---
title: "focus: a read or write outside the paths the running job declared"
description: "An advisory: it explains, and blocks nothing, on a read or write outside the paths the running job declared."
tags: [guard, rules, focus, advise]
---

# focus

An advisory: it explains, and blocks nothing, on a read or write outside the paths the running job declared.

## What it catches

A read or write outside the paths the running job declared.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [focus]: ...
```

`magus describe rule focus` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

---
title: "stage-classify: staging without classifying, when generated and source differ"
description: "An advisory: it explains, and blocks nothing, on staging without classifying, when generated and source differ."
tags: [guard, rules, stage-classify, advise]
---

# stage-classify

An advisory: it explains, and blocks nothing, on staging without classifying, when generated and source differ.

## What it catches

Staging without classifying, when generated and source differ.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [stage-classify]: ...
```

`magus describe rule stage-classify` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

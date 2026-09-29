---
title: "notes-author: an agent authoring a human's note, whose only provenance is who wrote it"
description: "A deny rule: it refuses an agent authoring a human's note, whose only provenance is who wrote it, and names what to run instead."
tags: [guard, rules, notes-author, deny]
---

# notes-author

A deny rule: it refuses an agent authoring a human's note, whose only provenance is who wrote it, and names what to run instead.

## What it catches

An agent authoring a human's note, whose only provenance is who wrote it.

## Why

A note is the one thing in the knowledge graph nothing here corroborates later, so its only provenance is the person who wrote it and signed the commit. That is why it is refused however the write is spelled: `capture` files a review transcript as a note, where the commit puts a person's name on prose they never read. Read the store instead, and say what belongs in it so the person can write it.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [notes-author]: ...
```

`magus describe rule notes-author` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

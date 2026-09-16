---
title: "revert-classify: a revert that has not classified what it is reverting"
description: "An advisory: it explains, and blocks nothing, on a revert that has not classified what it is reverting."
tags: [guard, rules, revert-classify, advise]
---

# revert-classify

An advisory: it explains, and blocks nothing, on a revert that has not classified what it is reverting.

## What it catches

A revert that has not classified what it is reverting.

## Why

Reverting regenerated output is the wrong default. An agent that did not hand-edit a `gen/` file concludes it is not "its" change and discards it, but a generate target rewriting its declared outputs is the system working, and those outputs belong in the same commit as the source that moved them. The honest test is whether the SOURCE changed, not whether anyone typed into the output. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged; that drift is environmental and worth reporting rather than discarding.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [revert-classify]: ...
```

`magus describe rule revert-classify` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

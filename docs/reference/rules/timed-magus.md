---
title: "timed-magus: `time` around a silent magus run, which already reports its own durations"
description: "An advisory: it explains, and blocks nothing, on `time` around a silent magus run, which already reports its own durations."
tags: [guard, rules, timed-magus, advise]
---

# timed-magus

An advisory: it explains, and blocks nothing, on `time` around a silent magus run, which already reports its own durations.

## What it catches

`time` around a silent magus run, which already reports its own durations.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [timed-magus]: ...
```

`magus describe rule timed-magus` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

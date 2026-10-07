---
title: "timed-magus: `time` around a silent magus run, which already reports its own durations"
description: "An advisory by default: it explains, and blocks nothing, on `time` around a silent magus run, which already reports its own durations."
tags: [guard, rules, timed-magus, advise]
---

# timed-magus

An advisory by default: it explains, and blocks nothing, on `time` around a silent magus run, which already reports its own durations.

## What it catches

`time` around a silent magus run, which already reports its own durations.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"timed-magus": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

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
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

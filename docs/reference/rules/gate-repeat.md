---
title: "gate-repeat: the gate run again soon after it passed, repeating work already done"
description: "An advisory by default: it explains, and blocks nothing, on the gate run again soon after it passed, repeating work already done."
tags: [guard, rules, gate-repeat, advise]
---

# gate-repeat

An advisory by default: it explains, and blocks nothing, on the gate run again soon after it passed, repeating work already done.

## What it catches

The gate run again soon after it passed, repeating work already done.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"gate-repeat": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [gate-repeat]: ...
```

`magus describe rule gate-repeat` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

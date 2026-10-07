---
title: "echo-on-success: an `&& echo` that restates what the exit status already says"
description: "An advisory by default: it explains, and blocks nothing, on an `&& echo` that restates what the exit status already says."
tags: [guard, rules, echo-on-success, advise]
---

# echo-on-success

An advisory by default: it explains, and blocks nothing, on an `&& echo` that restates what the exit status already says.

## What it catches

An `&& echo` that restates what the exit status already says.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"echo-on-success": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [echo-on-success]: ...
```

`magus describe rule echo-on-success` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

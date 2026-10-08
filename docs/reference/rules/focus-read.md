---
title: "focus-read: a read outside the paths a focus lease was given"
description: "A deny rule by default: it refuses a read outside the paths a focus lease was given, and names what to run instead."
tags: [guard, rules, focus-read, deny]
---

# focus-read

A deny rule by default: it refuses a read outside the paths a focus lease was given, and names what to run instead.

## What it catches

A read outside the paths a focus lease was given.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"focus-read": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [focus-read]: ...
```

`magus describe rule focus-read` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

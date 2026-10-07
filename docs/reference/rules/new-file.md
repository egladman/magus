---
title: "new-file: a new file in a directory whose naming has settled"
description: "An advisory by default: it explains, and blocks nothing, on a new file in a directory whose naming has settled."
tags: [guard, rules, new-file, advise]
---

# new-file

An advisory by default: it explains, and blocks nothing, on a new file in a directory whose naming has settled.

## What it catches

A new file in a directory whose naming has settled.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"new-file": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [new-file]: ...
```

`magus describe rule new-file` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

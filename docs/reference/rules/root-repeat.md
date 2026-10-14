---
title: "root-repeat: a main thread running the same delegable command again with no write since"
description: "Off by default: once a workspace turns it on, it reports a main thread running the same delegable command again with no write since."
tags: [guard, rules, root-repeat, off]
---

# root-repeat

Off by default: once a workspace turns it on, it reports a main thread running the same delegable command again with no write since.

## What it catches

A main thread running the same delegable command again with no write since.

## Why

An identical search, read, diff, run or fetch with no file written since returns what the context already holds. At standard the second run is advised once; at strict it is advised and the third is refused. A file write starts the count over. A calls parameter replaces the level's count.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"root-repeat": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [root-repeat]: ...
```

A workspace that sets this rule to `deny` sees `deny [root-repeat]: ...` instead.

`magus describe rule root-repeat` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

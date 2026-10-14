---
title: "root-result-cap: one tool result too large for a main thread's context, said on its next call"
description: "Off by default: once a workspace turns it on, it reports one tool result too large for a main thread's context, said on its next call."
tags: [guard, rules, root-result-cap, off]
---

# root-result-cap

Off by default: once a workspace turns it on, it reports one tool result too large for a main thread's context, said on its next call.

## What it catches

One tool result too large for a main thread's context, said on its next call.

## Why

A tool result stays in the context every later call re-sends. The main transcript is read after each call for the results it gained, and one past the level's size is said on the next call: once a session at light, once per tool at standard and strict, with lower sizes at each. It never refuses, since the result has already arrived. A chars parameter replaces the level's size.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `advise` or `off`:

```buzz
magus\guard.builtins({"root-result-cap": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [root-result-cap]: ...
```

`magus describe rule root-result-cap` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

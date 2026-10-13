---
title: "spawn-names-model: a main thread spawning a subagent without naming the model it runs on"
description: "An advisory by default: it explains, and blocks nothing, on a main thread spawning a subagent without naming the model it runs on."
tags: [guard, rules, spawn-names-model, off]
---

# spawn-names-model

An advisory by default: it explains, and blocks nothing, on a main thread spawning a subagent without naming the model it runs on.

## What it catches

A main thread spawning a subagent without naming the model it runs on.

## Why

A spawn that names no model runs the child on the session's own, usually the costliest it uses. At standard it advises once; at strict it refuses, and also refuses a spawn whose model and its job's model both name rows of the model table and differ.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-names-model": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
off [spawn-names-model]: ...
```

`magus describe rule spawn-names-model` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

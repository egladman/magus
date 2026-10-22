---
title: "dependency-update: a raw dependency update outside a target's update charm"
description: "A deny rule by default: it refuses a raw dependency update outside a target's update charm, and names what to run instead."
tags: [guard, rules, dependency-update, deny]
---

# dependency-update

A deny rule by default: it refuses a raw dependency update outside a target's update charm, and names what to run instead.

## What it catches

A raw dependency update outside a target's update charm.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"dependency-update": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [dependency-update]: ...
```

A workspace that sets this rule to `advise` sees `advise [dependency-update]: ...` instead.

`magus describe rule dependency-update` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

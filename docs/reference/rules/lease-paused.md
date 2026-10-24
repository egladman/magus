---
title: "lease-paused: a leased worker starting a magus run while a person has its job paused"
description: "A deny rule by default: it refuses a leased worker starting a magus run while a person has its job paused, and names what to run instead."
tags: [guard, rules, lease-paused, deny]
---

# lease-paused

A deny rule by default: it refuses a leased worker starting a magus run while a person has its job paused, and names what to run instead.

## What it catches

A leased worker starting a magus run while a person has its job paused.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-paused": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-paused]: ...
```

A workspace that sets this rule to `advise` sees `advise [lease-paused]: ...` instead.

`magus describe rule lease-paused` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

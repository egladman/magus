---
title: "lease-invalid: a call naming a lease this workspace's job store does not declare"
description: "An advisory by default: it explains, and blocks nothing, on a call naming a lease this workspace's job store does not declare."
tags: [guard, rules, lease-invalid, advise]
---

# lease-invalid

An advisory by default: it explains, and blocks nothing, on a call naming a lease this workspace's job store does not declare.

## What it catches

A call naming a lease this workspace's job store does not declare.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-invalid": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [lease-invalid]: ...
```

`magus describe rule lease-invalid` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

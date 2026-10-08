---
title: "lease-gate: a leased worker running the gate instead of the check it was assigned"
description: "A deny rule by default: it refuses a leased worker running the gate instead of the check it was assigned, and names what to run instead."
tags: [guard, rules, lease-gate, deny]
---

# lease-gate

A deny rule by default: it refuses a leased worker running the gate instead of the check it was assigned, and names what to run instead.

## What it catches

A leased worker running the gate instead of the check it was assigned.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-gate": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-gate]: ...
```

`magus describe rule lease-gate` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

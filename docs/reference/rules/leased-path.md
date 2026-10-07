---
title: "leased-path: a write into paths a running lease owns, by a caller that names no lease"
description: "An advisory by default: it explains, and blocks nothing, on a write into paths a running lease owns, by a caller that names no lease."
tags: [guard, rules, leased-path, advise]
---

# leased-path

An advisory by default: it explains, and blocks nothing, on a write into paths a running lease owns, by a caller that names no lease.

## What it catches

A write into paths a running lease owns, by a caller that names no lease.

## Why

The writer is either that lease, not saying so, or someone about to collide with whoever took it, a person or not; magus cannot tell which, so it advises rather than refuses, and says where the job was taken. It speaks once per session per lease: repeating it on every write would tell the writer nothing the first did not.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"leased-path": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [leased-path]: ...
```

`magus describe rule leased-path` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

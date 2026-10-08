---
title: "shared-checkout: a spawn into a checkout a live job already holds and writes"
description: "An advisory by default: it explains, and blocks nothing, on a spawn into a checkout a live job already holds and writes."
tags: [guard, rules, shared-checkout, advise]
---

# shared-checkout

An advisory by default: it explains, and blocks nothing, on a spawn into a checkout a live job already holds and writes.

## What it catches

A spawn into a checkout a live job already holds and writes.

## Why

Two workers in one checkout share every file in it, and the pair nobody survives is a magusfile, magus.yaml or spell source: half-saved, it stops the workspace loading for everybody there at once. It fires on the spawn, the one moment the choice between one checkout and two is still free to make. Prove the write paths disjoint with `magus describe file`, or give the new worker its own worktree, which is the answer whenever the write paths touch workspace configuration.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"shared-checkout": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [shared-checkout]: ...
```

`magus describe rule shared-checkout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

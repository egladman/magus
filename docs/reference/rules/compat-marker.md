---
title: "compat-marker: a leased worker adding a marker that keeps code for older data or callers"
description: "An advisory by default: it explains, and blocks nothing, on a leased worker adding a marker that keeps code for older data or callers."
tags: [guard, rules, compat-marker, advise]
---

# compat-marker

An advisory by default: it explains, and blocks nothing, on a leased worker adding a marker that keeps code for older data or callers.

## What it catches

A leased worker adding a marker that keeps code for older data or callers.

## Why

Whether code stays for an old caller is a judgment about who still consumes the old behavior, which a worker cannot see from its write paths and the root can. When a write a worker's lease allowed leaves more of a marker than it found (`compat(until:` and `Deprecated:` unless a markers parameter names others), the worker is told the root decides and to name it in unresolved_risks, and one attention row per lease states the job, the file, the marker and the model it ran on. It never refuses and never asks the host: the write is not wrong, the decision is not the worker's. A session acting under no lease, a person or the root, is never told.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"compat-marker": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [compat-marker]: ...
```

`magus describe rule compat-marker` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

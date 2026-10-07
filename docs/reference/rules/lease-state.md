---
title: "lease-state: a leased write while its row reports a diverged base, a re-entered path, or a bad pattern"
description: "An advisory by default: it explains, and blocks nothing, on a leased write while its row reports a diverged base, a re-entered path, or a bad pattern."
tags: [guard, rules, lease-state, advise]
---

# lease-state

An advisory by default: it explains, and blocks nothing, on a leased write while its row reports a diverged base, a re-entered path, or a bad pattern.

## What it catches

A leased write while its row reports a diverged base, a re-entered path, or a bad pattern.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-state": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [lease-state]: ...
```

`magus describe rule lease-state` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

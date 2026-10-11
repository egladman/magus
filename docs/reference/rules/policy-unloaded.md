---
title: "policy-unloaded: a push, merge, spawn or state-writing magus verb while the guard policy that judges it does not load"
description: "A deny rule by default: it refuses a push, merge, spawn or state-writing magus verb while the guard policy that judges it does not load, and names what to run instead."
tags: [guard, rules, policy-unloaded, deny]
---

# policy-unloaded

A deny rule by default: it refuses a push, merge, spawn or state-writing magus verb while the guard policy that judges it does not load, and names what to run instead.

## What it catches

A push, merge, spawn or state-writing magus verb while the guard policy that judges it does not load.

## Why

Misconfiguration is an error: when neither the working tree nor its approved copy loads, the workspace rules that judge these calls are not running, and letting the call through would read as judged. It fires only when the last policy that did load in this cache registered the seam's rule; with no record there was never a rule to lose, and the call passes. Every other call still runs on the built-in rules, so the fix stays runnable. The verdict names the likeliest fix for the caller: a rebuild in a checkout of magus, a placed binary for a leased worker, an update elsewhere.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"policy-unloaded": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [policy-unloaded]: ...
```

`magus describe rule policy-unloaded` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

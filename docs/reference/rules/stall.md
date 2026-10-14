---
title: "stall: a leased worker refused by something only the root can clear"
description: "An advisory by default: it explains, and blocks nothing, on a leased worker refused by something only the root can clear."
tags: [guard, rules, stall, off]
---

# stall

An advisory by default: it explains, and blocks nothing, on a leased worker refused by something only the root can clear.

## What it catches

A leased worker refused by something only the root can clear.

## Why

A worker blocked on its lease's boundary or on a magus that cannot load the tree waits and reports again, and the root hears late. When lease-write, lease-vcs, lease-harness, lease-rebind, lease-undeclared or stale-binary refuses a leased worker, the refusal's verdict line ends with the job's exit, the block named as an unresolved risk, and one attention row per lease and rule states the facts: the job, the rule, the model it ran on and how often it was refused. It names no remedy, and never refuses anything itself. A refusal repeating the last one, and a run of refused calls (a calls parameter sets its length), are recorded per lease and said to no one.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `advise` or `off`:

```buzz
magus\guard.builtins({"stall": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [stall]: ...
```

`magus describe rule stall` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

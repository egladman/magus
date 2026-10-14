---
title: "gate-repeat: the gate run a third time over a tree its last green verdict already covers"
description: "An advisory by default: it explains, and blocks nothing, on the gate run a third time over a tree its last green verdict already covers."
tags: [guard, rules, gate-repeat, advise]
---

# gate-repeat

An advisory by default: it explains, and blocks nothing, on the gate run a third time over a tree its last green verdict already covers.

## What it catches

The gate run a third time over a tree its last green verdict already covers.

## Why

From the third gate run inside two hours, the run is refused when the newest verdict the branch recorded passed and no file that differs from that verdict's commit was written after it: such a gate can only pass again. Short of that, the rule states what the gate has cost so far and the command that sizes the risk, once per session, and refuses nothing. A run log, a session store or version control it cannot read refuses nothing.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"gate-repeat": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [gate-repeat]: ...
```

A workspace that sets this rule to `deny` sees `deny [gate-repeat]: ...` instead.

`magus describe rule gate-repeat` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

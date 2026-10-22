---
title: "checkpoint-state: a command reaching for a tree's identity, which a revision alone cannot give"
description: "A deny rule by default: it refuses a command reaching for a tree's identity, which a revision alone cannot give, and names what to run instead."
tags: [guard, rules, checkpoint-state, deny]
---

# checkpoint-state

A deny rule by default: it refuses a command reaching for a tree's identity, which a revision alone cannot give, and names what to run instead.

## What it catches

A command reaching for a tree's identity, which a revision alone cannot give.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"checkpoint-state": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [checkpoint-state]: ...
```

A workspace that sets this rule to `advise` sees `advise [checkpoint-state]: ...` instead.

`magus describe rule checkpoint-state` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

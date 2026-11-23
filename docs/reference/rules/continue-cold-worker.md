---
title: "continue-cold-worker: dispatching another model turn to a worker whose prompt-cache warmth or cost cannot be established"
description: "A deny rule by default: it refuses dispatching another model turn to a worker whose prompt-cache warmth or cost cannot be established, and names what to run instead."
tags: [guard, rules, continue-cold-worker, deny]
---

# continue-cold-worker

A deny rule by default: it refuses dispatching another model turn to a worker whose prompt-cache warmth or cost cannot be established, and names what to run instead.

## What it catches

Dispatching another model turn to a worker whose prompt-cache warmth or cost cannot be established.

## Why

A continuation past the prompt-cache TTL, near a model's long-context price step, or with unknown age or cost can start new model work at a higher price. The root must approve it through a host prompt when one is available; a leased worker cannot approve its own spend, and a host that cannot render approval refuses it. A recent worker with known model and context cost stays prompt-free.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"continue-cold-worker": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [continue-cold-worker]: ...
```

A workspace that sets this rule to `advise` sees `advise [continue-cold-worker]: ...` instead.

`magus describe rule continue-cold-worker` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

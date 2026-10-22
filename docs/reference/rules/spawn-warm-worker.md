---
title: "spawn-warm-worker: a fresh spawn for follow-up work while the worker that wrote the code is still warm"
description: "A deny rule by default: it refuses a fresh spawn for follow-up work while the worker that wrote the code is still warm, and names what to run instead."
tags: [guard, rules, spawn-warm-worker, deny]
---

# spawn-warm-worker

A deny rule by default: it refuses a fresh spawn for follow-up work while the worker that wrote the code is still warm, and names what to run instead.

## What it catches

A fresh spawn for follow-up work while the worker that wrote the code is still warm.

## Why

Within the prompt-cache TTL a resumed worker re-reads its context at the cache-read price and has already paid its orientation, so it costs no more than a fresh agent, which pays the prefix and the orientation again. Context size is not cost: a large context read from cache is cheaper than a small one written fresh. A spawn whose job names an ended job in `follows`, or shares its write paths, is refused while that job's worker is warm, naming the agent to continue and the TTL left. It passes once the TTL has passed, when the warm worker's context is near its model's long-context price step, or when the job record carries a `fresh_reason`. It sees only the workers this session spawned, and measures warmth from the worker's last call, or from its job's last write when magus never saw one.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-warm-worker": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-warm-worker]: ...
```

A workspace that sets this rule to `advise` sees `advise [spawn-warm-worker]: ...` instead.

`magus describe rule spawn-warm-worker` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

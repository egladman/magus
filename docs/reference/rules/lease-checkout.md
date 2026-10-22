---
title: "lease-checkout: a worker holding a checkout running magus outside it"
description: "A deny rule by default: it refuses a worker holding a checkout running magus outside it, and names what to run instead."
tags: [guard, rules, lease-checkout, deny]
---

# lease-checkout

A deny rule by default: it refuses a worker holding a checkout running magus outside it, and names what to run instead.

## What it catches

A worker holding a checkout running magus outside it.

## Why

A host shell can start a subagent in its parent directory, and a cd never persists between calls, so the guard places each magus call by its line and judges it there. The holder of a row with a standing checkout runs magus in that checkout: name it on the line with `-C <root>` or a cd before the call, and the refusal quotes that line. A call whose directory cannot be read is placed where the host started. A plan, a dry run, a graph or help runs nothing and passes, and so does a caller with no lease, a row with no checkout and the root session.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-checkout": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-checkout]: ...
```

A workspace that sets this rule to `advise` sees `advise [lease-checkout]: ...` instead.

`magus describe rule lease-checkout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

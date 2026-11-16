---
title: "lease-authority: a state-changing call under a live lease whose row records no authority"
description: "A deny rule by default: it refuses a state-changing call under a live lease whose row records no authority, and names what to run instead."
tags: [guard, rules, lease-authority, deny]
---

# lease-authority

A deny rule by default: it refuses a state-changing call under a live lease whose row records no authority, and names what to run instead.

## What it catches

A state-changing call under a live lease whose row records no authority.

## Why

The store records an authority on every fork (ADR 0045): the root checkout's committed revision for a fork by the root, the parent's authority for a worker's child. A row without one was forked by an older magus or where the root's version control named no revision, so nothing says which revision may judge the worker, and judging from the worker's own tree would let it approve its own loosening. Reading the tree, printing a schema or a usage line, and the job verbs still run, as lease-undeclared passes them. The orchestrator ends the job and forks it again.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-authority": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-authority]: ...
```

A workspace that sets this rule to `advise` sees `advise [lease-authority]: ...` instead.

`magus describe rule lease-authority` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

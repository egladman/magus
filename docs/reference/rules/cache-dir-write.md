---
title: "cache-dir-write: a write into this checkout's magus cache dir, which magus alone owns"
description: "A deny rule by default: it refuses a write into this checkout's magus cache dir, which magus alone owns, and names what to run instead."
tags: [guard, rules, cache-dir-write, deny]
---

# cache-dir-write

A deny rule by default: it refuses a write into this checkout's magus cache dir, which magus alone owns, and names what to run instead.

## What it catches

A write into this checkout's magus cache dir, which magus alone owns.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"cache-dir-write": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [cache-dir-write]: ...
```

`magus describe rule cache-dir-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

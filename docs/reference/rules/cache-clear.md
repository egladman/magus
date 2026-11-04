---
title: "cache-clear: clearing a build cache by hand, which a trim to its declared budget does and keeps what is in use"
description: "A deny rule by default: it refuses clearing a build cache by hand, which a trim to its declared budget does and keeps what is in use, and names what to run instead."
tags: [guard, rules, cache-clear, deny]
---

# cache-clear

A deny rule by default: it refuses clearing a build cache by hand, which a trim to its declared budget does and keeps what is in use, and names what to run instead.

## What it catches

Clearing a build cache by hand, which a trim to its declared budget does and keeps what is in use.

## Why

A raw clear throws away what the next build wants, and it frees nothing a trim would not. The trim keeps files inside a fence, and it says what it did, so a person learns why the disk filled.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"cache-clear": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [cache-clear]: ...
```

A workspace that sets this rule to `advise` sees `advise [cache-clear]: ...` instead.

`magus describe rule cache-clear` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

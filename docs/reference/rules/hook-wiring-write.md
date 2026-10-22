---
title: "hook-wiring-write: a leased or agent-attributed write to the host hook wiring or the workspace's ./magus"
description: "A deny rule by default: it refuses a leased or agent-attributed write to the host hook wiring or the workspace's ./magus, and names what to run instead."
tags: [guard, rules, hook-wiring-write, deny]
---

# hook-wiring-write

A deny rule by default: it refuses a leased or agent-attributed write to the host hook wiring or the workspace's ./magus, and names what to run instead.

## What it catches

A leased or agent-attributed write to the host hook wiring or the workspace's ./magus.

## Why

The wiring and the binary it runs decide whether any rule runs at all from the host's next call or session on, so no scope anybody hands out includes them. A write tool and a shell line are judged alike: a redirect, `cp`, `mv`, `ln`, `tee`, `dd of=`, `install` or `rm` whose target is `.claude/settings.json`, `.claude/settings.local.json`, another host's hook file or the workspace root's `magus` is refused, after the line's `cd`s. A person's own session goes ahead, told which wiring file it is editing. A line that runs `go` is left to raw-tool, which advises the one relink of `./magus` a checkout that cannot load its sources needs.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"hook-wiring-write": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [hook-wiring-write]: ...
```

A workspace that sets this rule to `advise` sees `advise [hook-wiring-write]: ...` instead.

`magus describe rule hook-wiring-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

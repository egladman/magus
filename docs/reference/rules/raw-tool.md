---
title: "raw-tool: a toolchain command a spell already wraps, run outside the cache"
description: "A deny rule: it refuses a toolchain command a spell already wraps, run outside the cache, and names what to run instead."
tags: [guard, rules, raw-tool, deny]
---

# raw-tool

A deny rule: it refuses a toolchain command a spell already wraps, run outside the cache, and names what to run instead.

## What it catches

A toolchain command a spell already wraps, run outside the cache.

## Why

magus covers these exactly and adds cache, sandbox and affected tracking, so the refusal costs nothing: `magus run <target> <project>`, and `magus describe targets -o name` lists what this workspace calls them. Tool flags go after `--`. A raw WRITE (codegen, a formatter with -w/--write/--fix, `go mod tidy`, build output landing on a tracked path) is the firm half: it leaves the owning target reporting drift it did not cause, and that has no exceptions. The guard reads the command being RUN, so a wrapper, a `VAR=value` prefix or `bash -c` reaches the same verdict, and `go -C <dir> <verb>` reads the same as `go <verb> -C <dir>`. One build is exempt, in a checkout of magus itself: `go build -o magus ./cmd/magus`, alone on its line, into a checkout root that has no `magus` binary yet, is advised rather than refused, because a fresh checkout has no other way to get its first binary. Once the binary exists the deny applies again and names `./magus run go-build .`, which regenerates the embedded spell bytecode a bare link bakes in stale. It was an advisory first, and changed behavior zero times over a long session while leaving the Go build cache poisoned by uninstrumented runs, which is why it denies.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [raw-tool]: ...
```

`magus describe rule raw-tool` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

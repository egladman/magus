---
title: "magus-timeout: a magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside"
description: "An advisory by default: it explains, and blocks nothing, on a magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside."
tags: [guard, rules, magus-timeout, advise]
---

# magus-timeout

An advisory by default: it explains, and blocks nothing, on a magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside.

## What it catches

A magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside.

## Why

A timeout wrapper ends magus with a signal from outside, so the run log records no cause, a target's tools can outlive the process that started them, and the next reader sees a run that stopped rather than one that timed out. magus bounds a run itself: `--timeout <dur>` bounds the whole run and cancels its own process tree, `--target-timeout <dur>` caps each target, and `--stall-timeout <dur>` stops a run making no progress. A `run` or `affected` is served the same argv with `--timeout` and the wrapper's duration (600 becomes 10m). Any other verb is served bare: it holds no lock worth waiting on, since a held lock refuses at once (MGS3009). `magus buzz` has no bound of its own, so a wrapped script is advised rather than refused. It reads every flag the wrapper takes and the launchers around it (`env`, `nice`, `nohup`, `sh -c`, `eval`). A wrapper sending QUIT or ABRT is left alone, since that is how a goroutine dump is taken from a hung run, and so is a verb that runs until interrupted: `watch`, `events`, `job watch` and `--version`.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"magus-timeout": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [magus-timeout]: ...
```

`magus describe rule magus-timeout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

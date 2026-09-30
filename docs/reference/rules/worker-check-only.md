---
title: "worker-check-only: a bound worker running a target other than its row's check or one writing its write paths"
description: "A deny rule: it refuses a bound worker running a target other than its row's check or one writing its write paths, and names what to run instead."
tags: [guard, rules, worker-check-only, deny]
---

# worker-check-only

A deny rule: it refuses a bound worker running a target other than its row's check or one writing its write paths, and names what to run instead.

## What it catches

A bound worker running a target other than its row's check or one writing its write paths.

## Why

A worker's row names one check, and the orchestrator runs every other target serially, in its own tree, after the units land. Measured 2026-09-29: briefs told six workers they "may also run" `magus run lint docs`, and two ran it at the same moment from two worktrees. The cache replays a run that has landed and never two in flight, and two trees share no key, so the pair doubled the load and proved nothing the orchestrator's one run does not. Allowed under a live lease: the row's check, with or without a charm and forwarded args; a `magus run` of a target that declares an output among the row's write paths, read from the workspace's own declarations (a `-generate` name or a charm stands in when the workspace cannot load); magus's own `go-build .` in its own checkout; and every other verb, and every run that only reports (--plan, --dry-run, --graph). `magus affected` is never the check, since it takes its projects from the diff. An unbound caller, a row that owns the gate and a row declaring no check are untouched. The same rule grades a spawn or continuation brief naming a live row (by `magus.lease=`, `job exec` or its JOB ID line): a brief telling that worker to run another target is refused under brief-command, quoting the line, before any worker exists.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [worker-check-only]: ...
```

`magus describe rule worker-check-only` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired

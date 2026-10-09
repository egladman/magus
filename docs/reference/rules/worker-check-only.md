---
title: "worker-check-only: a bound worker running a target other than its row's check or one writing its write paths"
description: "A deny rule by default: it refuses a bound worker running a target other than its row's check or one writing its write paths, and names what to run instead."
tags: [guard, rules, worker-check-only, deny]
---

# worker-check-only

A deny rule by default: it refuses a bound worker running a target other than its row's check or one writing its write paths, and names what to run instead.

## What it catches

A bound worker running a target other than its row's check or one writing its write paths.

## Why

A worker's row names one check, and the orchestrator runs every other target serially, in its own tree, after the units land. Two workers running the same target at once from two worktrees double the load and prove nothing the orchestrator's one run does not: the cache replays a run that has landed and never two in flight, and two trees share no key. Allowed under a live lease: the row's check, with or without a charm and forwarded args; a `magus run` of a target that declares an output among the row's write paths, read from the workspace's own declarations (a `-generate` name or a charm stands in when the workspace cannot load); and every other verb, and every run that only reports (--plan, --dry-run, --graph). `magus affected` is never the check, since it takes its projects from the diff. Rebuilding magus's own binary (`go-build .`) is not on the list either: there is one binary per base, the orchestrator builds it and places a copy in the worker's checkout. An unbound caller, a row that owns the gate and a row declaring no check are untouched. The same rule grades a spawn or continuation brief naming a live row (by `magus.lease=`, `job exec` or its JOB ID line): a brief telling that worker to run another target is refused under brief-command, quoting the line, before any worker exists.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"worker-check-only": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

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
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

---
title: "worker-check-only: a bound worker running other than its check or a writer of its write paths, or writing past them"
description: "A deny rule by default: it refuses a bound worker running other than its check or a writer of its write paths, or writing past them, and names what to run instead."
tags: [guard, rules, worker-check-only, deny]
---

# worker-check-only

A deny rule by default: it refuses a bound worker running other than its check or a writer of its write paths, or writing past them, and names what to run instead.

## What it catches

A bound worker running other than its check or a writer of its write paths, or writing past them.

## Why

A worker's row names one check, and the checkout's integrator runs every other target serially, in its own tree, after the units land. The integrator is a role, never a check: the subagent spawned `<parent>/integrate <job>` whose row was taken in the checkout the call runs in builds ./magus, regenerates and gates there, whatever its row's check names. A row whose check is the gate is refused it like any worker, since a job's check is never the gate. The root holds no spawn title, so it never integrates, and one build at a time per checkout is the project lock's: a second run of a project the checkout is already running is refused, naming the run holding it. Two workers running the same target at once from two worktrees double the load and prove nothing the integrator's one run does not: the cache replays a run that has landed and never two in flight, and two trees share no key. Allowed under a live lease: the row's check, with or without a charm and forwarded args; a `magus run` of a target that declares an output among the row's write paths, read from the workspace's own declarations (a `-generate` name or a charm stands in when the workspace cannot load); and every other verb, and every run that only reports (--plan, --dry-run, --graph). A `magus run` whose target names the rw charm, the check included, is held to the write paths as well: it passes only when every glob that target declares writing (ctx.writesFiles, with or without editsExistingSource), and every file each target its ctx.needs chain reaches in the same project declares modifying, lies inside them. A needed step's outputs and another project's steps build what the run consumes, and are not counted. A target whose chain declares no write passes, since nothing says what it touches, so a formatter declares the files it rewrites. The refusal names the globs and the same run with neither the charm nor the default charms. A run the workspace's default_charms give rw is judged the same way, unless it passes --no-default-charms. A spell op run on its own under the rw charm (`markdown::dprint:rw`, or a target no magusfile it selects exports) is refused even when the check composes it: an op declares no writes, so it would always pass and what it rewrites shows only after the write, as MGS3001. The refusal names the magusfile target that calls the op, whose declarations are judged, or the run without the charm when none does. An op whose spell declares no rw charm runs unpatched under default_charms [rw] and passes. A run regenerating an output another declared or running lease holds is refused, so the two never overwrite each other's copy; a lease that filed its result holds no output. The refusal names `magus job wait` for a holder below the caller, and otherwise the root of the holder's job tree, for which it files one attention row. `magus affected` is never the check, since it takes its projects from the diff. Rebuilding magus's own binary (`go-build .`) is not on the list either: there is one binary per base, the integrator builds it and a copy is placed in the worker's checkout. An unbound caller, the checkout's integrator and a row declaring no check are untouched. The same rule grades a spawn brief naming a live row (by `magus.lease=`, `job exec` or its JOB ID line): a brief telling that worker to run another target is refused under brief-command, quoting the line, before any worker exists. A continuation to a worker already bound to a live row is not graded against the row: that worker's own runs are.

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

A workspace that sets this rule to `advise` sees `advise [worker-check-only]: ...` instead.

`magus describe rule worker-check-only` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default

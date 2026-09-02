---
title: Concurrency
order: 8
description: How magus coordinates parallel work - the intra-process scheduler that parallelizes a single run, the cross-process workspace lock that keeps two separate magus invocations from clobbering each other's outputs and cache, and the daemon-owned machine budget that keeps every magus on the host from oversubscribing it.
tags:
  [
    concurrency,
    parallelism,
    workspace-lock,
    scheduler,
    daemon,
    needs,
    MAGUS_NO_WAIT,
    machine budget,
    admission,
    memory_mb,
  ]
---

# Concurrency

magus coordinates parallel work at two distinct scopes, and it helps to keep them
apart:

- **Within one run** - the scheduler fans a single invocation out across projects
  and targets, ordered by the dependency graph. This is [dependencies](dependencies.md)
  and per-target policy (`slots`, `exclusive`) doing their job.
- **Across separate runs** - the **workspace lock** stops two _independent_ `magus`
  processes from mutating the same project at the same time.
- **Across the whole machine** - the **machine budget** stops every magus on the
  host, in every worktree, from starting more work than the machine can carry.

The first is about _ordering and fan-out_, the second about _mutual exclusion_, the
third about _capacity_. They solve different problems and none replaces the others.

## Within one run: the scheduler

A single `magus run`/`magus affected` invocation builds the dependency graph, then
runs targets concurrently where the graph allows. `magus\needs` edges order the work
([dependencies](dependencies.md)); a target's `slots` and `exclusive` policy tune how
much of it runs at once ([targets](targets.md)). When a daemon is present, that
fan-out draws from **one shared concurrency pool** across every client
([daemon](../guides/integrations/daemon.md)).

All of this lives inside one process. It orders nothing in a _second_ `magus` you
start in another terminal: the two invocations have separate graphs and separate
schedulers, and neither can see the other's plan. What they DO share is the machine
budget below, which is what stops them from starting more work than the host can
carry.

## Across separate runs: the workspace lock

That second invocation is the problem the workspace lock exists for. Two `magus`
processes running at once - two terminals, or two agents - can collide: one running
`generate` or `clean` rewrites or deletes a project's declared outputs while the
other is reading or writing them, and work is lost. Both also write the project's
[cache](cache.md). Serializing that is **mutual exclusion**, which is why `needs`
cannot solve it: `needs` orders targets _inside one run_ and has no visibility into a
separate process. Only a lock does.

So before a non-dry run begins mutating, magus takes a **per-project advisory lock**
for every project the run will touch, holds it for the whole invocation, and releases
it at the end. A second `magus` that wants the same project waits for the first to
finish, then proceeds automatically.

Key properties:

- **Per project, not per workspace.** Runs on _different_ projects proceed in
  parallel; only runs on the _same_ project serialize. The lock is not directory- or
  target-scoped - a project's outputs and cache are the unit being protected, and
  that is exactly a project.
- **Advisory.** It serializes _magus_ processes and nothing else. A raw `git clean`,
  an `rm`, or any other tool ignores it. The guarantee is "no two magus invocations
  mutate the same project at once," not "the tree is untouchable."
- **Crash-safe.** It is an OS file lock (`flock`), which the kernel releases when the
  holding process exits or crashes - never a stale PID file that would wedge a project
  after a `Ctrl-C`.
- **Taken by every real run**, not just `generate`/`clean`. Even `magus test` writes
  the project's cache and run log, so two concurrent runs on one project are
  serialized regardless of whether either touches the source tree.

### When a run is waiting

If another magus holds the lock, your run does not fail and does not hang silently -
it prints one line up front and starts the moment the other finishes:

```text
magus: project web is being changed by another magus process; waiting for it to
finish. This run starts automatically once it does; set MAGUS_NO_WAIT=1 to fail
fast instead.
magus: lock on project web released; starting.
```

On a terminal the wait is also pinned as a notification, so it stays visible
instead of scrolling away behind the run it is queued behind:

![A magus run queued on the workspace lock, with a yellow notification naming the process that holds it and the two ways out](../../assets/gen/terminal-lock-waiting.svg)

It is a condition rather than an event - true until the lock clears - so it is
pinned rather than given a countdown, and it is retracted once the lock is
acquired. See [Terminal](terminal.md).

Set `MAGUS_NO_WAIT=1` to make a contended run **fail fast** instead of blocking -
useful in CI or a script that would rather error than queue behind another process.
It names the holder the same way the wait message does, and exits **75**
(`EX_TEMPFAIL`) rather than 1:

```text
magus: project web is locked by another magus process (pid 4821 (magus run ci .),
running 12s, in /Users/me/src/acme); not waiting (MAGUS_NO_WAIT set)
```

75 is the transient-failure convention, so a caller can branch on "the machine is
busy, retry later" without treating a genuinely broken build the same way. Nothing
ran, and the same invocation succeeds once the holder finishes.

The wait happens at the very start of the invocation, before the concurrency pool is
even set up, so a blocked run does not yet appear in `magus status` (there is nothing
running to report - it is queued behind the lock). The stderr line above is how you
know why.

## Across the whole machine: the budget

The lock protects a project's outputs. Nothing in it protects the machine: two runs
on _different_ projects proceed in parallel by design, and so do runs in different
worktrees, so N agents each running `magus affected ci` start N budgets' worth of
work against one host. Measured on a ten-core workstation: four concurrent gates,
load average 13.7, and tests failing because they were starved rather than wrong.

So before a step starts, magus takes its concurrency slots and its declared
`memory_mb` from a budget shared by every magus on the machine. The budget lives in
the [daemon](../guides/integrations/daemon.md) - one daemon per user means one budget
per machine - and a run starts one if none is up.

Key properties:

- **Per machine, not per workspace.** The whole point is the worktree this run
  cannot see. The budget is a fraction of the memory the daemon may commit, and the
  daemon's concurrency capacity.
- **Declared, not observed.** It arbitrates what targets say they need
  ([`memory_mb`](targets.md)), so the same command on the same machine reaches the
  same verdict whatever else is running. Observed pressure warns separately and
  never blocks.
- **It queues.** A step that does not fit waits, and starts the moment room frees.
  The wait names who holds the budget - pid, project, target, and directory - and
  repeats on a heartbeat, so it never reads as a hang. `MAGUS_NO_WAIT=1` fails fast
  instead, exiting **75** ([MGS3009](../reference/codes/sandbox/MGS3009.md)) - the same
  transient-failure code a contended lock uses above, and for the same reason.
- **It fails open.** A daemon that will not start, or that dies mid-run, leaves the
  run unarbitrated and finishing, having said once that it is. Claims are retired by
  process liveness, so nothing has to release cleanly.
- **Only runs pay for it.** `magus ls`, `describe`, and `query` cost the same however
  loaded the machine is, so none of them starts a daemon.

`magus status` shows the whole budget: what is held, what is queued, and by whom,
across every worktree on the machine.

## Relationship to the daemon

The [daemon](../guides/integrations/daemon.md) is the long-lived process that hosts the shared pool,
owns the machine budget, and serves clients. It is the natural single point that
knows what is running everywhere, which is why the budget lives there and why a run
starts one.

It does not run your work. A top-level `magus run` executes in your own process and
prints to your own terminal; it asks the daemon for admission and nothing else. A
nested `magus` a magusfile spawns still adopts into its parent's pool, which is where
its output belongs.

The workspace lock is the floor underneath both: it holds even with no daemon in the
loop, because it is an OS file lock rather than a process anyone has to start. The
three compose - ordering inside a run, exclusion per project, capacity per machine.

## See also

- [Dependencies](dependencies.md): `magus\needs` and `depends_on`, how a single run is ordered.
- [Targets](targets.md): per-target `slots` and `exclusive` policy.
- [Daemon](../guides/integrations/daemon.md): the persistent process and the shared concurrency pool.
- [Cache](cache.md): what a run writes, and why concurrent writers are serialized.
- [MGS3009](../reference/codes/sandbox/MGS3009.md): the machine budget, when it queues and when it refuses.

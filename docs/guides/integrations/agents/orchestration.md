---
title: How orchestration works
description: The three roles a fanned-out agent session splits into (orchestrator, worker, integrator), the boundary each one holds, and the pieces of magus that hold it (the guard, the job store, the broker's machine budget and the worktree bootstrap).
tags:
  [
    agents,
    orchestration,
    orchestrator,
    worker,
    integrator,
    jobs,
    leases,
    guard,
    broker,
    bootstrap,
  ]
---

# How orchestration works

When one agent session hands work to several, magus splits the work into three
roles. Each role has a boundary, and the [guard](guard.md) enforces every one of
them from the same record: the job row. This page covers who does what and what
stops a role from doing someone else's work. The loop itself (checkpoint,
partition, fork, release, verify) is in [Jobs and leases](leases.md), which has
its own figure.

<!--diagram:agent-orchestration-->

| role         | who it is                                       | what it may do                                                          |
| ------------ | ----------------------------------------------- | ----------------------------------------------------------------------- |
| orchestrator | the root session                                | fork rows, spawn, `magus job wait`, the final gate, push, open the PR   |
| worker       | a subagent bound to one row                     | write its row's write paths, run its row's check, exit its row          |
| integrator   | the subagent spawned `<parent>/integrate <job>` | merge finished branches, build ./magus, regenerate, run the full checks |

Above all three is the [human in the loop](../../../doctrine.md#human-in-the-loop).
What lands, what is published and what a round may spend are that person's
calls.

## The orchestrator plans and never does groundwork

The orchestrator is the root session. It owns the goal, the partition and the
final verification, and it hands out everything else:

1. Record the working state with `magus vcs checkpoint -o name`.
2. Partition the work by write set from graph evidence (`magus refs`,
   `magus explain`, `magus affected ci --plan --stdin`), as the
   [`magus-multi-agent` skill](../../../reference/skills/magus-multi-agent.md)
   lays out.
3. Fork one row per unit with `magus job fork`: criteria, write paths, one
   check, a model tier, the checkpoint.
4. Spawn each worker with the description `<parent>/<role> <job>`, which binds
   the worker to that row.
5. Collect with `magus job wait <job>`, which grades the filed result against
   the diff since the checkpoint and against a recorded passing run of the
   job's own check.
6. Run `magus affected ci` once, in its own tree, after every unit lands, then
   push and open the pull request.

The boundary is [the doctrine](../../../doctrine.md#the-orchestrator-plans-workers-do-the-work)
"the orchestrator plans; workers do the work", and the guard's
[orchestrator-groundwork](../../../reference/rules/orchestrator-groundwork.md)
rule enforces it. While a row the session handed out is declared, running or
exited, the guard refuses that session's recursive searches, source reads, full
diffs, target runs, edits and integration. It lets the job verbs, graph reads,
branch work, the final gate, `git push` and `gh pr` through. The role comes from
the live lease, never from the session's history or its context size, and a
person at a terminal is never judged by it.

The root may take one row itself with `magus job exec <row>`. It spawns nothing
while it holds that row, and `magus job exit <row>` returns it to
orchestrating. The one build it runs itself is the fix for a missing or stale
./magus: `./magus run go-build .` or the bootstrap, alone on its line.

## A worker holds one row

A worker is bound to exactly one job row by its lease. The row is everything it
is handed, and `magus describe job <job>` renders it the same way every time:

- **Write paths.** Exact files, or one declaration of a file
  (`run.go#executeStages`), so two workers can share a file without sharing a
  declaration. A write outside them, or into a path another live job holds, is
  refused and names the owner.
- **One check.** The narrowest target that covers the edits, never the gate.
  The guard's [worker-check-only](../../../reference/rules/worker-check-only.md)
  rule refuses any other target, except the generator of an output the worker's
  own write paths make stale.
- **Its own checkout and branch.** A worker names its checkout on every magus
  line, and the guard refuses a magus call that resolves outside it.
- **Its result.** The last act is `magus job exit <job> --stdin` with the
  changed paths, the check's command and output ref, and the risks it could not
  settle. There is no `passed` field: `magus job wait` reads the outcome from the
  recorded run.

### Isolation levels

`magus job fork` chooses where each job runs, cheapest first, and records the
reason:

| level | where the job runs                                       | chosen when                                                              |
| ----- | -------------------------------------------------------- | ------------------------------------------------------------------------ |
| 1     | the forker's checkout                                    | a read-only job, or a disjoint writer at a high `risk_tolerance`         |
| 2     | one scratch checkout its wave shares, with one ./magus   | a light writer, or one touching a file the workspace loads beside a peer |
| 3     | its own checkout, with the forker's ./magus placed in it | a writer by default, a generator's output, or an overlap with no claim   |
| 4     | its own checkout, where the holder builds ./magus        | a write path is a build input of `./cmd/magus`                           |

A write path covering a magusfile, `magus.yaml` or a spell source a magusfile
imports never shares a checkout with another writer: half-saved, it stops the
workspace loading for everyone in that checkout.

### When a worker retries

A worker reruns only its own check. When the machine's build budget refuses
that check ([MGS3009](../../../reference/codes/sandbox/MGS3009.md)), the worker
may wait for a seat with `magus status --wait | magus run <its check>`, which
keeps the seat the wait sized for the run it pipes into. Any other wait, or a
bare rerun within the minute, is refused.

Retrying a whole job is not the worker's. `magus job fork <same id>` on an ended
job starts its next attempt and folds the one that ended into the row's `prior`,
which the next holder reads in `magus describe job`. magus never retries on its
own, and an attempt it marks not retryable names what must change first.

## The integrator merges, regenerates and gates

Each checkout that lands work has one integrator: the subagent spawned
`<parent>/integrate <job>`, unisolated, in that checkout. It runs everything
the workers and the root are refused there: the build of ./magus, the generate
targets, and the full checks, one at a time.

An integration job is a row whose `integrates` names the finished branches it
merges:

- `magus job fork` computes its write paths from the files those branches
  changed since its checkpoint, and notes a live holder of one of them instead
  of refusing, since the merge settles it.
- The guard lets its holder run `git merge --no-ff <branch>` for each branch
  the row names, and no other merge.
- It regenerates declared outputs once, after the merges and before the full
  test run, so a drift test does not fail for nothing.

magus counts the finished branches waiting off the base. From
`jobs.integrate_advise_at` other forks are advised to integrate first, and from
`jobs.integrate_refuse_at` they are refused.

## The guard enforces every boundary

The guard is the one reader that turns a row into a verdict. It runs on every
file write and every command the host reports, reads the job store for the
lease the caller acts under, and denies what crosses that lease's boundary with
the command to run instead. The full table of what it refuses under a lease is
in [Jobs and leases](leases.md#what-the-guard-enforces-under-a-lease).

The [sandbox](../../../concepts/sandbox.md) reads the same row for a run: when
the sandbox is on, every target run and `magus buzz` script in a worker's
checkout gets a write grant of exactly its write paths, the cache and a private
temp directory. A boundary written twice would disagree with itself, so both
read one declaration.

## The job store is one `jobs.jsonl` per repository

Every row lives in `<XDG state>/magus/jobs/<repo>/jobs.jsonl`, one line per
version of a row, the last line per id winning. Every worktree and clone of one
repository reads the same set, which is how an orchestrator forks in its own
checkout and a worker takes the lease from another.

The store enforces one thing itself: one author per job. A holder may record
its base, shrink its own write paths, fork a child inside them and end its own
job. Widening a boundary and verifying a result are the forking session's.

## The broker holds one budget per machine

Editing costs nothing; validation contends. Every magus run on the machine, in
every worktree, takes its slots and memory from one budget the
[broker](../server.md) holds, so N workers each running a check cannot start N
budgets of work. A step the budget cannot seat is refused at once with
MGS3009, naming who holds it, rather than queued. `magus status` shows what is
held and by whom. See [Concurrency](../../../concepts/concurrency.md#across-the-whole-machine-the-budget).

## Each checkout gets its own ./magus

A worktree starts with no ./magus, and the guard's
[stale-binary](../../../reference/rules/stale-binary.md) rule refuses calls that
change state until a magus that loads the tree is in place. A leased worker is
never told to build: there is one binary per base. The worktree bootstrap,
`hack/dev/bootstrap-worktree.buzz`, places ./magus, installs the harness skills,
runs `magus job exec`, checks the fit and builds the graph, in that order, so an
edit lands only after the base is recorded.

`magus job fork` places the forker's ./magus in a new checkout whenever no build
input of `./cmd/magus` differs from its commit. A job that changes one runs at
isolation level 4, where the holder builds its own.

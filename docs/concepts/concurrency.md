---
title: Concurrency
order: 8
description: How magus coordinates parallel work - the intra-process scheduler that parallelizes a single run, the cross-process workspace lock that keeps two separate magus invocations from clobbering each other's outputs and cache, and the broker-held machine budget that keeps every magus on the host from oversubscribing it.
tags:
  [
    concurrency,
    parallelism,
    workspace-lock,
    scheduler,
    broker,
    needs,
    machine budget,
    admission,
    memory_mb,
    jobserver,
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
much of it runs at once ([targets](targets.md)). Nested `magus` invocations a magusfile
spawns adopt into this same pool rather than standing up their own
([the broker and the server](../guides/integrations/server.md)).

All of this lives inside one process. It orders nothing in a _second_ `magus` you
start in another terminal: the two invocations have separate graphs and separate
schedulers, and neither can see the other's plan. What they DO share is the machine
budget below, which is what stops them from starting more work than the host can
carry.

### How wide the pool is by default

`concurrency` (`-j`) sets the pool width outright; leaving it unset falls back to
`concurrency_profile`: `conservative` (half the cores), `balanced` (`min(cores, 8)`,
the default), or `aggressive` (every core). The default is `balanced` everywhere: magus
reads no environment variable to guess where it is running, so the same command run
the same way behaves the same on a laptop and on any CI provider. `MAGUS_CONCURRENCY`
overrides both. A CI job that wants every core asks for it explicitly - a
`--concurrency-profile aggressive` flag, `MAGUS_CONCURRENCY_PROFILE=aggressive`, or
`concurrency_profile: aggressive` in `magus.yaml` - the same way any other caller would.

`concurrency_profile` sizes the machine budget's memory the same way, not just the
pool's core count: see [below](#across-the-whole-machine-the-budget).

### Tools that run their own jobs: the jobserver

A slot count only helps if the tool inside the target honors it. `make`, `cargo` and
the `cc` crate schedule their own parallel jobs, so a target holding 4 slots can still
start 16 compilers. To close that gap, magus acts as a
[GNU make jobserver](https://www.gnu.org/software/make/manual/html_node/Job-Slots.html)
for every step that runs holding **two or more** slots.

- The step's processes get `MAKEFLAGS` and `CARGO_MAKEFLAGS` naming a pipe preloaded
  with one token per slot beyond the first. The process magus starts holds the
  implicit first one, so a `make` the step runs never has more jobs going than the
  step holds slots. Two processes the step runs side by side each hold an implicit
  token of their own.
- The pool belongs to one step. It is opened when the step's body starts and closed
  when it returns, so a child that dies holding tokens cannot shrink anything else's
  grant. A step replayed from the cache opens none.
- `proc\withSlots(n, callback)` seats a pool of `n` for its callback, replacing the
  step's.
- A step holding one slot gets no pool, and that covers every target that declares
  nothing. A pool of zero tokens would make `cargo build` run one `rustc` at a time
  where it uses every core today. Declaring `slots` (or a `memory_mb` that converts
  to more than one slot) is what opts a target in.

The pipe form (`--jobserver-auth=3,4`) is the only one every GNU make reads: 3.81
(macOS's `/usr/bin/make`) knows only `--jobserver-fds`, and 4.2 and 4.3 exit with an
error on the 4.4 `fifo:` form. The limits follow from the protocol:

- A `-jN` on the tool's own command line starts a pool of its own and ignores this
  one. Run `make` without `-j` to share the step's slots.
- ninja 1.13 reads only the `fifo:` form. It prints a warning and schedules itself as
  it would with no magus above it.
- A target that sets `MAKEFLAGS` in its own environment keeps its value.
- Windows gets no pool. GNU make there shares slots through a named semaphore, and a
  process cannot inherit extra descriptors, so children schedule themselves as before.

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
it at the end. **magus never waits on another magus invocation**: a second `magus` that
wants the same project is refused immediately rather than queued behind the first.

Key properties:

- **Per project, not per workspace.** Runs on _different_ projects proceed in
  parallel; only runs on the _same_ project contend. The lock is not directory- or
  target-scoped - a project's outputs and cache are the unit being protected, and
  that is exactly a project.
- **Advisory.** It serializes _magus_ processes and nothing else. A raw `git clean`,
  an `rm`, or any other tool ignores it. The guarantee is "no two magus invocations
  mutate the same project at once," not "the tree is untouchable."
- **Crash-safe.** It is an OS file lock (`flock`), which the kernel releases when the
  holding process exits or crashes - never a stale PID file that would wedge a project
  after a `Ctrl-C`.
- **Taken by every real run**, not just `generate`/`clean`. Even `magus test` writes
  the project's cache and run log, so two concurrent runs on one project contend
  regardless of whether either touches the source tree.

### When a run is contended

If another magus holds the lock, your run fails fast rather than blocking or hanging
silently. It names the holder and exits **75** (`EX_TEMPFAIL`) rather than 1:

```text
magus: project web is locked by another magus process (pid 4821 (magus run ci .),
running 12s, in /Users/me/src/acme); not waiting
```

75 is the transient-failure convention, so a caller can branch on "the machine is
busy, retry later" without treating a genuinely broken build the same way. Nothing
ran, and the same invocation succeeds once the holder finishes.

Two exceptions queue instead, because the holder shares something with the run that
wants the lock:

- **The same process.** The server runs adopted nested runs, background jobs and its
  own symbol indexer in one process. Those queue for a project rather than refusing
  each other.
- **The same run.** A `ci` target whose parallel steps each run `magus run build
  libs/shared` starts two nested magus processes under one root invocation. They are
  one run, so the second waits for its sibling rather than failing the parent.

### When the contender is a newer gate

One contention is not fail-fast. A **gate** is the whole `ci` target, run through
`magus affected ci` or `magus run ci`. A gate that contends for a lock held by an
**earlier gate on the same workspace root** does not refuse: it asks the earlier
run to stop, which it does
with [MGS3014](../reference/codes/sandbox/MGS3014.md), and takes the lock, usually within
a second:

```text
magus: superseded the earlier gate on project . (held by pid 40118 (magus affected ci .),
running 4m12s, in /Users/me/src/acme); its verdict would have described a tree that has
since changed.
```

The older run's verdict is about files that have already changed, so the machine spends
its time on the newer one instead. The ordering comes from the tree and never from the
caller: one resolved root, both invocations the whole `ci` target, later start wins.
There is no priority to set. A sibling worktree is a different tree and still refuses, a
`run build` behind a `run test` still refuses, and a lock held by one of the run's own
ancestors is still refused as [MGS3007](../reference/codes/sandbox/MGS3007.md). A holder
that does not answer within thirty seconds is refused exactly like any other contention.

### Piping one magus into another

A shell pipe between two magus runs is supported, including when both need the same
project:

```sh
magus affected ci --plan --preflight generate | magus run ci-shard:gha
```

The two stages start together, so without help one of them reaches the lock first and
the other is refused. Instead, a run whose standard input is written by another magus
process takes no lock while that upstream stage holds, or has yet to take, a project it
needs. It drains the pipe while it waits, so the upstream never blocks writing, and
its targets read the same bytes afterwards. When the wait is for a real conflict,
the run says so:

```text
magus: waiting for pid 40118 (magus affected ci --plan --preflight generate), upstream
of this run in a pipe, to finish with the projects this run needs before taking their locks.
```

`magus status` lists such a run under "waiting on a pipe upstream".

Stages on different projects run at once and stream. A downstream run waits only until
the upstream has taken its locks, then starts as soon as it sees they do not overlap
with its own. A read-only upstream, like `magus ls` or `magus status --watch`, never
holds a reader back. Three or more magus stages work the same way: each stage
considers every magus stage upstream of it.

Magus stages also pass each other typed records rather than text. A run, an
`affected` run or a `magus buzz` script whose stdout another magus reads writes the
records [`-o jsonl`](../reference/manpage/magus-run.md) writes there, and prints its
usual prose on stderr, so a person still reads the run. Nothing changes when the reader
is a terminal, a file or another tool like `jq`, and an explicit `-o` always wins.
Projects flow forward: a run that names no projects runs on the projects its upstream
ran on, the way `xargs` takes its arguments, and one that names projects runs exactly
those.

```sh
magus run format libs/x | magus run lint | magus run test
```

runs all three on `libs/x`, each after the one before it. Bytes a target writes to its
stdout still reach the targets of the stage downstream, beside the records.

A `magus buzz` script can sit anywhere in the pipeline. The
[`pipe`](../reference/buzz/pipe.md) module reads the records of the stage before it as
typed values and emits records for the stage after it, and while a magus reads the
script's stdout, what it prints goes to stderr. This one, `docs/concepts/failures.buzz`
in the magus repository, replaces `| grep FAIL`:

```buzz
import "std";
import "pipe";

fun main(args: [str]) > void !> any {
    while (pipe\more()) {
        final rec = pipe\next();
        if (rec.@"type" == "run.target.result" and rec.status == "failed") {
            std\print("FAIL {rec.project}:{rec.target}  magus query output {rec.ref}");
        }
    }
}
```

```sh
magus run test . | magus buzz failures.buzz
```

A script that emits a `run.scope` record chooses the projects the run after it runs on,
so `magus run test . | magus buzz retry.buzz | magus run test` reruns only what failed.
A stage whose stdout is its product, like `magus affected ci --plan`, writes no
records; a script reads it from stdin as it is. A script's exit status counts in the
pipeline like any stage's.

A pipeline of runs stops at its first failed stage, and the last stage exits with it,
so `magus run generate:rw . | magus run test .` is a chain you can trust without
`set -o pipefail`: several targets on one line, concurrent where their projects are
disjoint and ordered where they overlap. A run starts nothing once a magus stage
upstream of it has failed, and a run that succeeded waits for every magus stage
upstream of it to end, then fails if one did. Both refusals are
[MGS3030](../reference/codes/sandbox/MGS3030.md), exiting with the failed stage's own
status. A read-only upstream like `magus ls` counts too.

The upstream is proven from the kernel, never taken on trust: the run follows its stdin
back to the processes writing it, and counts one as a magus stage only when it runs the
same magus executable. Tools in between, like `magus ... | jq ... | magus ...` or
`| tee log |`, are followed through the same way: the run reads which pipes they hold,
never their arguments. Input that no magus feeds, like `echo x | cat | magus run ...`,
proves nothing, so a lock held elsewhere is refused as above. Two different magus
binaries also meet as strangers, and so does everything on Windows, where no kernel
interface proves who writes an anonymous pipe. A run nested in
another never waits on its own ancestor, which is still
[MGS3007](../reference/codes/sandbox/MGS3007.md). A pipe that loops back into the run
reading it is refused with [MGS3023](../reference/codes/sandbox/MGS3023.md).

## Across the whole machine: the budget

The lock protects a project's outputs. Nothing in it protects the machine: two runs
on _different_ projects proceed in parallel by design, and so do runs in different
worktrees, so N agents each running `magus affected ci` start N budgets' worth of
work against one host. Measured on a ten-core workstation: four concurrent gates,
load average 13.7, and tests failing because they were starved rather than wrong.

So before a step starts, magus takes its concurrency slots and its declared
`memory_mb` from a budget shared by every magus on the machine: the host's capacity.
It lives in the [broker](../guides/integrations/server.md) - one broker per user means
one budget per machine - and a run starts one if none is up.

Key properties:

- **Per machine, not per workspace.** The whole point is the worktree this run
  cannot see. The budget is a share of the memory the broker may commit, and the
  machine's cores - both sized by the same `concurrency_profile` that
  decided the pool's width above: `balanced` and `conservative` reserve a quarter of
  memory for the OS, magus's own processes, and everything else sharing the
  machine; `aggressive` reserves none of that, taking every usable megabyte down to a
  fixed 512 MiB floor for the kernel and its page cache. A CI job that asks for
  `aggressive` claims memory the same way it claims cores.
- **Declared, not observed.** It arbitrates what targets say they need
  ([`memory_mb`](targets.md)), so the same command on the same machine reaches the
  same verdict whatever else is running. Observed pressure warns separately and
  never blocks.
- **It does not queue behind another invocation.** A step kept out by another magus
  invocation is refused immediately, exiting **75**
  ([MGS3009](../reference/codes/sandbox/MGS3009.md)) - the same transient-failure code
  a contended lock uses above, and for the same reason. The refusal names who holds
  the budget - pid, project, target, and directory - so a caller can go see why and
  retry once it frees. A step kept out only by its own process or its own run waits
  for them, on the same terms as the lock.
- **What a missing broker means is a setting.** `broker: best-effort` (the default)
  leaves a run whose broker will not start, or dies mid-run, unarbitrated and
  finishing, having said once that it is. `broker: required` refuses the step instead
  ([MGS3022](../reference/codes/sandbox/MGS3022.md), exit 69), and `broker: off` never
  asks one. Nothing has to release cleanly: each claim rides the run's one connection
  to the broker, and a run that dies, even to `SIGKILL`, releases it when the kernel
  closes that connection.
- **Only runs pay for it, and only for as long as they need it.** `magus ls`,
  `describe`, and `query` cost the same however loaded the machine is, so none of them
  starts a broker. The broker exits by itself ten minutes after it last held a claim
  or a service with a dependent.

`magus status` shows the whole budget: what is held, and by whom, across every
worktree on the machine.

## Relationship to the broker and the server

The [broker](../guides/integrations/server.md) is the per-user process that holds the host's
capacity and the shared services runs keep warm. It is the natural single point that
knows what is running everywhere, which is why the budget lives there and why a run
starts one. The server, which a person starts for MCP and the console, asks the broker
like any run.

Neither runs your work. A top-level `magus run` executes in your own process and
prints to your own terminal; it asks the broker for capacity and nothing else. A
nested `magus` a magusfile spawns still adopts into its parent's pool, which is where
its output belongs.

The workspace lock is the floor underneath both: it holds even with no broker in the
loop, because it is an OS file lock rather than a process anyone has to start. The
three compose - ordering inside a run, exclusion per project, capacity per machine.

## See also

- [Dependencies](dependencies.md): `magus\needs` and `depends_on`, how a single run is ordered.
- [Targets](targets.md): per-target `slots` and `exclusive` policy.
- [The broker and the server](../guides/integrations/server.md): the process that owns the machine budget, and the one a person starts.
- [Cache](cache.md): what a run writes, and why concurrent writers are serialized.
- [MGS3009](../reference/codes/sandbox/MGS3009.md): the machine budget, and the two ways it refuses.

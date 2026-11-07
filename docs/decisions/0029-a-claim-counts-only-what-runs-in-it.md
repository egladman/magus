---
title: "ADR 0029: a claim counts only what runs in it"
order: 29
description: On 2026-11-07 the broker refused 20 runs while slots sat idle. Root lint claimed 16.3 GiB of a 21 GiB budget; its own measured runs peak at 5.0 GiB. The gap is the chain fold, which adds every ctx.needs member's peak to the composer's claim even when that member runs as a step with its own claim, has already passed in this run, or replays from cache, and which drops the whole call to flat summed peaks when one member has no curve. Half the refusals were slots alone. This record proposes that a composer's claim count only the work that runs under it, that members compose curve by curve where curves exist, that a step take its slots elastically between one and its measured width, and that the margin be revisited now that MGS3053 exists. budget_wait stays 0. No role is added.
tags: [adr, decision, concurrency, memory, admission, broker, forecast, history, roles]
status: proposed
date: 2026-11-07
---

# ADR 0029: a claim counts only what runs in it

## Context

### What a person saw

During the combined-PR integration (egladman/magus#704, 2026-11-07), on a 10-core, 24 GiB
macOS host with profile `aggressive` (budget 21504 MiB, 10 slots), `magus server status`
showed 2 of 10 slots held and many runs refused with MGS3009. Eli:

> we're too risk averse ... I'm pretty sure we're heavily underutilizing this machine ...
> update our algorithm used for fitting, or our machine budget.

### What the broker recorded

The broker's `capacity.refused` log held 20 refusals between 15:05 and 15:46 on 2026-11-07:

| refused because             | count | example                                                                   |
| --------------------------- | ----- | ------------------------------------------------------------------------- |
| slots alone (10/10 held)    | 10    | 15:33:59, nine generate targets, 13083 MiB of 21504 held                  |
| memory (held + ask > 21504) | 9     | 15:18:00, `generate` asking 18 MiB, 21487 held: lint 16349 + go-test 5138 |
| both                        | 1     | 15:45:37, test 11209 MiB x 4 slots against 12361 MiB / 8 slots held       |

Every memory refusal had a lint holder claiming 16349 to 17245 MiB.

### Lint's claim is not lint's peak

From the forecast history (`~/.local/state/magus/history/v1.json`, 100-outcome window, passing
runs of root targets, 2026-11-07):

| target      | runs | median MiB | p90 MiB | max MiB | 1.25 x max | claim the broker held |
| ----------- | ---- | ---------- | ------- | ------- | ---------- | --------------------- |
| lint        | 48   | 3839       | 4346    | 5008    | 6261       | 16349 to 17245        |
| conventions | 12   | 4137       | 4376    | 4440    | 5551       | 12837                 |
| test        | 40   | 3634       | 6014    | 7314    | 9143       | 10313 to 11209        |
| buzz-test   | 53   | 2827       | 4396    | 6135    | 7670       | not held at snapshot  |

No measured run moved: lint's own maximum in the window is 5008 MiB. The 16 GiB is the claim's
arithmetic, `types.SizedChainMemory` (types/chain_memory.go:47):

- lint is cacheable, not `skip_cache`, so `types.MembersClaimTheirOwn` (chain_memory.go:21-23)
  is false and its one `ctx.needs(install_linter, conventions, harness_drift)` call
  (magusfile.buzz:2499-2503) folds into lint's claim. conventions folds its own eleven-member
  call (magusfile.buzz:2385-2388) the same way before lint sums it.
- The fold is sized before anything runs and takes no cache input (chain_memory.go:47-100). A
  member that already passed in this run (`body.MarkDone`, run.go:916), that replays from the
  local cache, or that the scheduler ran as its own upstream step with its own claim, still
  adds its whole peak. The graph lists conventions, harness-drift, install-linter and
  proofread-build as lint's dependencies, and conventions keeps a history of its own, so its
  processes are claimed once in its own step and again inside lint's.
- Curve composition is all or nothing per call: the first member with no curve returns flat
  summed peaks (`foldCall`, types/memory_curve.go:105-113). The holder's sizing said
  "bindings-generate: no memory curve; summed the peaks of its ctx.needs call". ADR 0012's
  curve is recorded (11371 of 68350 stored outcomes carry one) but does not reach lint.

So lint claims 3.3 times its measured maximum, conventions 2.9 times, test 1.4 to 1.5 times.

### Slots

Half the refusals involved no memory at all. A step claims the 75th percentile of its
CPU-time over wall time, rounded up, doubled while saturated (`widthSlots`,
internal/ci/forecast/history.go:1315-1340), and runs Go that wide (`spawn.SeatJobserver`,
internal/proc/spawn/jobserver.go:160-183). The generate targets measure 1.5 cores and claim 2;
test and go-test claim 4. A claim is all of its slots or none: a 2-slot step is refused while
one slot is free, and nothing lets it run at 1.

### What the host really held

At 15:53, with magus holding 3.2 GiB of claims, the host reported 22 GiB used, 1.2 GiB unused,
7.3 GiB in the compressor and 23.3M pages swapped out since boot. Most of it was not magus:
the desktop app's helpers, a VM, a browser. The 21 GiB budget is a figure on paper, not free
memory, and MGS3053 is what notices when the paper and the machine disagree. Raising the
budget would make the paper wronger. The waste is in the claims.

### Measurement error runs both ways

The tree sampler sums `pti_resident_size` over the process tree (internal/sys/mem/tree_darwin.go,
procrss_libproc_darwin.go:33-46). Resident size counts shared pages once per process, so a tree
of many Go and node processes reads high, by an estimated 0.1 to 1.5 GiB for 50 to 100
processes (not measured). The 200 ms sample misses short spikes, and pages the compressor took
leave resident size, so it also reads low under pressure. Neither explains an 11 GiB gap.

### Waiting

`budget_wait` defaults to 0 since ADR 0001's 2026-11-05 amendment: "A wait nobody asked for
looks like work, and a caller who wants one can say so." On 2026-11-07 every refused agent ran
the same command again with `--budget-wait`, and the runs went through one at a time. That is
a round trip per refusal, but it is the caller asking, which is what the amendment wanted. The
refusals were wrong because the claims were wrong, not because nobody waited.

### Roles

Roles are computed from the live lease (`types.AgentRole`, types/guard.go:12-35): root, worker,
integrator. Today:

- A read-only row with no check and no write paths holds no seat (`holdsNoBuildSeat`,
  internal/guard/waiter.go:403-408), and its spawn skips the budget advice.
- A worker never waits for a seat. A refused check is filed `"owed": "no seat"`, and the
  integrator runs it beside the full gate (changes/unreleased/workers-never-wait-integrators-do.md).
- The integrator waits, for as long as it chose. The root never builds.
- `magus job exec` with no command takes no slots; with `--timeout` it holds `--slots`
  (default 1) and no memory (cmd/magus/job.go:1730-1810).

One defect: `magus job fork` prints the budget-full advice for every row, read-only included
(cmd/magus/job.go:1465-1471), where the spawn rule already skips it. It printed three times
while this record's research was being forked.

### What other tools do

Fetched 2026-11-07. Input, not justification.

- A static per-machine width is the norm: `make -j`, Ninja `-j`, Gradle
  `org.gradle.workers.max` (default: processors), Turborepo `--concurrency` (default 10, or a
  percentage), Nx `--parallel` (default 3), Go `-p` (default GOMAXPROCS), Cargo `build.jobs`.
- Declared per-action resources: Bazel's `ResourceManager` admits an action when its declared
  CPU and memory fit, defaulting the host to its cores and two thirds of RAM, and blocks and
  queues otherwise; its user manual calls the estimates "very crude". `--local_resources`
  takes `HOST_RAM*.5`-style expressions. Pants assumes 512 MiB per pooled child process.
- Tokens taken as needed: the GNU make jobserver hands one token per job, recursive makes
  share the pipe, and a job runs with however many it gets. Ninja joins a FIFO jobserver.
- A live brake: `make -l` will not start a job while load average is high; Bazel's
  `--experimental_memory_load_scheduling` (default off) schedules from machine memory load;
  systemd-oomd kills on PSI thresholds.
- Overcommit with a backstop: Kubernetes admits on requests, lets limits exceed the node, and
  evicts by QoS class; Slurm admits on `--mem` and can kill the step on OOM.
- No fetched tool learns its estimates from history. Buck2's concurrency docs did not fetch.

Sources: bazel.build/docs/user-manual, bazel.build/reference/command-line-reference,
github.com/bazelbuild/bazel ResourceManager.java, nx.dev/docs/reference/nx-commands,
turbo.build/repo/docs/reference/run, docs.gradle.org build_environment and worker_api,
gnu.org make manual (Parallel, POSIX-Jobserver), ninja-build.org/manual.html,
doc.rust-lang.org/cargo/reference/config.html, pkg.go.dev/cmd/go,
kernel.org accounting/psi.html, freedesktop.org systemd-oomd.service,
kubernetes.io manage-resources-containers and quality-service-pod, slurm.schedmd.com/sbatch.html,
pantsbuild.org/stable/reference/global-options.

## Decision

Every item is proposed; nothing is built until Eli approves.

| item                                                         | State    |
| ------------------------------------------------------------ | -------- |
| A composer's claim counts only members that run inline in it | proposed |
| A call's members compose curve by curve where curves exist   | proposed |
| A step's slots are elastic, from one to its measured width   | proposed |
| The memory margin is decided again, with MGS3053 as backstop | proposed |
| A refusal prints the claim's breakdown                       | proposed |
| `job fork` prints no budget advice for a seatless row        | proposed |
| `budget_wait` stays 0                                        | proposed |
| No new role                                                  | proposed |

### A composer's claim counts only members that run inline in it

The fold adds a member only when that member's processes will run under the composer's
collector. A member is left out when:

- it already passed in this run (the set `body.MarkDone` reads);
- it is a local cache hit at sizing time (the same key `RunAside` tries before
  `claimMachine`, internal/cache/cache.go:1495-1520);
- the scheduler runs it as its own step with its own claim, which is the case for every
  member the graph lists as an upstream dependency of the composer, `skip_cache` or not.

Sizing moves from before the body to the moment the claim is taken, which is when those
three facts are known. When the composer has three or more measured runs of its shape whose
members ran inline, that measured peak, which already saw those members' processes, bounds
the fold from above: a measured total beats a summed estimate.

Lint, sized this way on 2026-11-07: install-linter, conventions and harness-drift run as their
own upstream steps, so lint claims its own work, 1.25 x 5008 = 6261 MiB, instead of 16349.

### A call's members compose curve by curve where curves exist

`foldCall` stops returning at the first member without a curve. Members with a profile
compose by bucket as ADR 0012 decided; each member without one adds its flat peak to every
bucket. The fallback names every member that went flat, not only the first.

### A step's slots are elastic, from one to its measured width

A claim carries a floor of 1 and a ceiling of its declared or measured width. The broker
seats it when the floor and its memory fit, and grants up to the ceiling from what is free,
leaving room for the head of the queue. The step runs Go as wide as it was granted, as it
does today with what it claimed. The outcome records the slots granted, so saturation still
reads against what the run had. A declared `slots` is a floor and a ceiling both, so `scip`'s
declared 4 still means 4.

This is the jobserver's rule at the step's grain: run with the tokens you get. It removes
the slots-only refusals whenever a slot is free, and costs a step's duration when it runs
narrow. The verdict depends on the held state, as it does now.

### The memory margin is decided again, with MGS3053 as backstop

The claim is ceil(1.25 x the highest passing peak). The margin predates MGS3053 (2026-10-31),
which now refuses new steps while the kernel says memory is bad. Three choices, for Eli:

| claim basis  | lint + test + buzz-test + spells-generate, own peaks | all four fit in 21504 |
| ------------ | ---------------------------------------------------- | --------------------- |
| 1.25 x max   | 23784 MiB                                            | no (3 fit)            |
| max          | 19024 MiB                                            | yes                   |
| p90 of peaks | 15288 MiB                                            | yes                   |

This record recommends max, no margin: resident-size sums already read high, the window
holds 100 runs, and an overrun meets MGS3053 before the OOM killer. p90 is refused below.

### A refusal prints the claim's breakdown

MGS3009 and `magus server status` print, for each holder over a quarter of the budget, what
its claim is made of: its own measured peak, each folded member and why it folded, and which
members went flat. For example: "lint 6.1 GiB: own peak 5.0 GiB over 48 runs, x1.25; folded
nothing: install-linter, conventions, harness-drift run as their own steps". `magus session
-o json` already records sizing; the breakdown is that record, printed.

`magus doctor` adds one check over the broker's `capacity.refused` log: refusals in the last
day where the free slots or memory exceeded the refused ask once the fold is removed. It
reports a count and the targets; it changes nothing.

### `job fork` prints no budget advice for a seatless row

`jobFork` (cmd/magus/job.go:1465-1471) checks `holdsNoBuildSeat` as the spawn rule does.

### `budget_wait` stays 0

No new evidence reverses it. The waits on 2026-11-07 were callers asking, as designed, and
most of the refusals they answered were caused by the fold.

### No new role

Eli asked whether a role that consumes no slots would help. The roles already split the
budget that way: a read-only row holds no seat, a worker never waits and files its check
owed, the integrator waits, and `job exec` without a command holds nothing. A new role would
name a seatless runner that already exists as a read-only row. What starved the slots was the
claims of the runs that did take seats.

### Principles checked

- [Measured, not asserted](../design/principles/measured-not-asserted.md): relied on. The
  composer's measured peak bounds the summed estimate, and every figure above has a sample and
  a date.
- [Delete before you add](../design/principles/delete-before-you-add.md): relied on. The fix
  removes double counting rather than adding a budget, a role or a knob.
- [Determinism at chosen points](../design/judgment/determinism-at-chosen-points.md): bends.
  Sizing at claim time reads which members already passed or hit the cache, facts of this run
  and this cache, not of the host. Elastic slots make a step's width depend on the held state
  at its admission, as its admission already does.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. Every member left
  out of the fold, and every one held flat, is named in the breakdown.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md): relied
  on. The breakdown is the reason for a memory refusal.
- [Friction that carries meaning](../design/principles/friction-that-carries-meaning.md):
  relied on. A refusal by a phantom claim is a toll and is removed; a refusal by a real one
  stays, and waiting stays opt-in.
- [A record of refusals](../design/refusals.md): relied on. Claims stay declared or measured
  from recorded runs; nothing observed live enters a verdict beyond MGS3053's existing reading.
- [The orchestrator plans](../design/principles/the-orchestrator-plans.md): relied on, unchanged.

## Alternatives

- **Raise the budget, or default to a smaller reserve.** Lost: at 15:53 the host had 1.2 GiB
  unused while magus held 3.2 GiB of claims; the rest was other software. A larger budget
  admits more against memory that is not there. Reopen if a host's non-magus use is measured
  low across a week of refusals.
- **Every composer's same-project members claim their own seat, as `skip_cache` composers'
  do.** Lost for now: it moves the cache boundary, since a cacheable member is "the lexical
  cache boundary: its own needs calls remain inline" (run.go:921-926), and it runs the
  interceptor under every cacheable body. Leaving out members that run as steps reaches the
  same claim for lint without it. Reopen if a composer is found whose inline members still
  over-claim after this record.
- **Claim the p90 of peaks.** Lost: one run in ten would exceed its claim by design, and a
  claim exceeded that often is not a claim. Reopen with ADR 0012's overrun-to-peak rule built
  and measured.
- **Oversubscribe CPU: budget slots at 1.5 x cores.** Lost: ADR 0001 records four concurrent
  gates at load average 13.7 with tests failing because they were starved. Elastic slots fill
  idle cores without promising more than exist. Reopen if load average at refusal, recorded
  beside `capacity.refused`, shows idle cores while slots are full.
- **Tune each machine once, at setup.** Lost: a calibration describes the code it ran on and
  goes stale with the next change to a target. The history is already per host and per
  target, read when a claim is sized, and learns from every run. Reopen if a fresh machine's
  first three runs per target are shown to cost more than a calibration would.
- **Learn the budget itself from swap or pressure history.** Lost: the budget would then
  depend on what else ran last week, the brake the record of refusals refused in its
  2026-10-16 form. MGS3053 reads pressure at admission, which is enough. Reopen if MGS3053
  fires repeatedly on one host while claims fit.
- **Wait by default, or by role.** Lost: ADR 0001's 2026-11-05 amendment, and no new evidence;
  see Decision. Reopen if, after the fold is fixed, refusals stay common and callers always
  run again with a wait.
- **A seatless role that submits checks to a runner.** Lost: the read-only row and the owed
  check already are that, and a runner that executes on its own is the agentic tool magus
  refuses to be. Reopen if owed checks pile up unrun at integration.
- **Preempt or pause an overrunning step.** Not proposed, as in ADR 0001 and ADR 0012: a
  killed build wastes the work done.
- **Proportional set size or `phys_footprint` instead of resident size.** Deferred: Linux
  PSS needs a page-table walk per process, and macOS footprint disagrees with `ru_maxrss`
  (ADR 0012). The double counting it fixes is smaller than the fold. Reopen if the fold is
  fixed and claims still exceed measured host use by more than the sampling error.

## Consequences

- Before, on 2026-11-07's history: lint 16349 MiB; lint and test do not fit together (26662
  MiB); two lints do not fit (32698 MiB); one lint at a time with nothing large beside it.
  After the fold change alone: lint 6261 MiB; lint and test 15404 MiB fit; two lints 12522 MiB
  fit. After the margin change too: lint, test, buzz-test and spells-generate all fit
  (19024 MiB).
- The slots-only refusals, 10 of the 20, are seated at one slot whenever a slot is free.
- A step granted fewer slots than its width runs longer; its outcome records the grant, so its
  duration band widens honestly.
- Sizing at claim time runs once per step admission instead of once per plan; its inputs are
  the run's passed set and a local key lookup the admission already makes.
- Tests that pin the fold change with it: `TestChainMemorySumsOneCallAndPeaksAcrossCalls`
  (limiter_test.go), `TestChainMemoryMBPeaksOverOneCall` (types/target_test.go), and
  machine_gate_test.go's lending cases. `types.SizedChainMemory`'s signature gains the run's
  passed set and a cache probe, which updates internal/commands/testdata/api-go.lock.
- Docs to update with the code: docs/concepts/concurrency.md's budget section; MGS3009.md,
  including its stale "maximum rather than the sum" sentence; types/target.go's `MemoryMB` doc
  and docs/concepts/workspace.md:221, which both still say a chain takes its largest
  declaration; ADR 0012, whose `cache.ChildrenClaimTheirOwn` names a symbol that no longer
  exists; and the magus-multi-agent skill's `status --wait`, a flag that was deleted.

## Open questions

- Does lint's recorded peak include its inline members' processes, or only its body? The code
  reading infers that inline members feed the composer's collector; no run has proven it.
  Measure one `magus run lint . --no-cache` with members cold before building the upper bound.
- How wide is the duration band once grants vary? Elastic slots may widen D90/D10 enough to
  blunt ADR 0012's packing.
- Which `concurrency_profile` is the default? docs/concepts/concurrency.md says `balanced` in
  one section and `aggressive` in another; this host runs `aggressive`.

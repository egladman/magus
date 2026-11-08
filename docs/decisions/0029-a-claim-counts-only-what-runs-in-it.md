---
title: "ADR 0029: a claim counts only what runs in it"
order: 29
description: On 2026-11-07 the broker refused 20 runs while slots sat idle. Root lint claimed 16.3 GiB of a 21 GiB budget; its own measured runs peak at 5.0 GiB. The gap is the chain fold, which adds every ctx.needs member's peak to the composer's claim even when that member runs as a step with its own claim, has already passed in this run, or replays from cache, and which drops the whole call to flat summed peaks when one member has no curve. Half the refusals were slots alone, and 15 of 20 refused agent runs were run again with --budget-wait within seconds. Eli decided on 2026-11-07 that a composer's claim counts only the work that runs under it, that members compose curve by curve where curves exist, that a claim is the highest measured peak with no margin, that a step takes its slots elastically between one and its measured width while leaving room for waiters, that a run that does not fit waits by default and says so, reversing ADR 0001's 2026-11-05 zero default, and that refusals and waits are recorded in a JSON Lines store. No role is added.
tags: [adr, decision, concurrency, memory, admission, broker, forecast, history, roles]
status: accepted
date: 2026-11-07
supersedes: "ADR 0001, Amendments: 2026-11-05, waiting is opt-in, budget_wait defaults to 0"
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

The broker's `capacity.refused` list (`magus server status -o json`) held 20 refusals between
15:05 and 15:46 on 2026-11-07:

| refused because             | count | example                                                                   |
| --------------------------- | ----- | ------------------------------------------------------------------------- |
| slots alone (10/10 held)    | 10    | 15:33:59, nine generate targets, 13083 MiB of 21504 held                  |
| memory (held + ask > 21504) | 9     | 15:18:00, `generate` asking 18 MiB, 21487 held: lint 16349 + go-test 5138 |
| both                        | 1     | 15:45:37, test 11209 MiB x 4 slots against 12361 MiB / 8 slots held       |

Every memory refusal had a lint holder claiming 16349 to 17245 MiB.

### Lint's claim is not lint's peak

From the forecast history (`~/.local/state/magus/history/v1.json`, 100-outcome window, passing
runs of root targets, read 2026-11-07):

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

At 15:53, with magus holding 3.2 GiB of claims, `top` and `vm_stat` reported 22 GiB used,
1.2 GiB unused, 7.3 GiB in the compressor and 23.3M pages swapped out since boot. Most of it
was not magus: the desktop app's helpers, a VM, a browser. The 21 GiB budget is a figure on
paper, not free memory, and MGS3053 is what notices when the paper and the machine disagree.
Raising the budget would make the paper wronger. The waste is in the claims.

### Measurement error runs both ways

The tree sampler sums `pti_resident_size` over the process tree (internal/sys/mem/tree_darwin.go,
procrss_libproc_darwin.go:33-46). Resident size counts shared pages once per process, so a tree
of many Go and node processes reads high, by an estimated 0.1 to 1.5 GiB for 50 to 100
processes (not measured). The 200 ms sample misses short spikes, and pages the compressor took
leave resident size, so it also reads low under pressure. Neither explains an 11 GiB gap.

### Waiting

`budget_wait` defaults to 0 since ADR 0001's 2026-11-05 amendment: "A wait nobody asked for
looks like work, and a caller who wants one can say so." The 30-minute default it reversed
lived only on the #641 branch, from c6f6829ee to 2bb45341c, about 3h47m, and never ran on
main. No refusal count, hang or complaint precedes the reversal in that day's transcripts.

On 2026-11-07, agent transcripts show 20 MGS3009 refusals. 15 were followed by the same
command with `--budget-wait`, 13 of those within 7 s (median 3 s). After one refusal an agent
pasted the flag onto every later run: 160 commands that day carried it. Waits that followed
lasted 3m03s, over 3m04s and 5m18s; one `--budget-wait 10m` waited the full bound behind a
45-minute `affected ci` and was refused anyway. Estimates of time-to-seat from the holders'
recorded durations ran 2.4 to 4 times low, because other waiters take a freed seat first.

The broker keeps only the last 20 refusals, in memory (`refusedKeep`, broker/budget.go:292),
and every one of its 104 owner fields read `unleased`, so this count rests on transcripts.

Replaying the 20 refusals of 15:05-15:46 under the claims decided below, memory refuses none
(10 before). 11 are still refused on slots alone, among them the nine-target burst at
15:33:59, because the holders had taken their full widths.

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
(cmd/magus/job.go:1465-1471), where the spawn rule already skips it. It printed for each
read-only row forked for this record's research.

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
- Waiting by default: schedulers wait. Bazel blocks, make waits for a token, Kubernetes and
  Slurm hold a job pending and say why, Buildkite queues, and Cargo waits for its package-cache
  lock and prints `Blocking waiting for file lock`. Locks held by one person's command refuse:
  apt's `DPkg::Lock::Timeout` and git's `index.lock` default to 0. No fetched tool varies the
  default by whether a terminal is attached.
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
kubernetes.io manage-resources-containers and quality-service-pod, slurm.schedmd.com/sbatch.html
and squeue.html, pantsbuild.org/stable/reference/global-options, Debian apt.conf, git-scm.com.

## Decision

An inline member takes a machine claim of its own, sized from its own history, while its composer's claim is lent (`LendMachineClaim`). No cache step is added, so the cache boundary stays where ADR 0012 put it. The composer claims, measures and records only its own body. A cross-project member claims the same way. The fold of `ctx.needs` members into one claim is gone. Lint, on 2026-11-07, claims its own 5008 MiB; install-linter, conventions and harness-drift each hold a seat of their own while they run.

Eli decided every item on 2026-11-07. Jobs A to F built every item marked done. The curve row is not built: no fold is left for it to compose.

| item                                                                 | State     |
| -------------------------------------------------------------------- | --------- |
| A composer claims its own body; each inline `ctx.needs` member claims its own seat | done      |
| A call's members compose curve by curve where curves exist          | not built |
| A claim is the highest measured peak, with no margin                 | done      |
| A step's slots are elastic, and a grant leaves room for waiters      | done      |
| A run that does not fit waits by default, and says so                | done      |
| Refusals and waits are recorded in a JSON Lines store                | done      |
| A refusal and a wait print the claim's breakdown                     | done      |
| `job fork` prints no budget advice for a seatless row                | done      |
| No new role                                                          | done      |

### A composer claims its own body; each inline ctx.needs member claims its own seat

The composer's claim, its measured peak and its recorded outcome cover its body alone. An inline `ctx.needs` member takes its own claim, sized from its own runs, while the composer's claim is lent (`LendMachineClaim`), and the broker packs it like any other claim. A target that a step and an inline member both reach runs once per invocation; the second waits on the first, with its claim and slots lent. Before this record, a composer's recorded peak included its inline members' processes: T1 (out1c639c47c5dd) showed it.

### A call's members compose curve by curve where curves exist

Not built. Nothing composes a call's members into one claim any more, so there is no fold to compose curve by curve; the broker packs each member's claim by its own curve.

### A claim is the highest measured peak, with no margin

The claim was ceil(1.25 x the highest passing peak). The margin predates MGS3053
(2026-10-31), which refuses new steps while the kernel says memory is bad. Against the 21504
MiB budget, lint, test, buzz-test and spells-generate together need:

| claim basis  | the four, own peaks | all four fit |
| ------------ | ------------------- | ------------ |
| 1.25 x max   | 23784 MiB           | no (3 fit)   |
| max          | 19024 MiB           | yes          |
| p90 of peaks | 15288 MiB           | yes          |

Eli chose max, no margin: resident-size sums already read high, the window holds 100 runs,
and an overrun meets MGS3053 before the OOM killer. `peakMargin` (limiter.go) is removed.

### A step's slots are elastic, and a grant leaves room for waiters

A claim carries a floor of 1 and a ceiling of its declared or measured width. The broker
seats it when the floor and its memory fit. With nobody queued it grants up to the ceiling
from what is free. With waiters queued it grants at most a fair share of the free slots,
free / (waiters + 1), and never less than one, so a burst starts narrow instead of being
refused behind steps that took every slot. The step runs Go as wide as it was granted, as it
does today with what it claimed. The outcome records the slots granted, so saturation still
reads against what the run had. A declared `slots` is a floor and a ceiling both, so `scip`'s
declared 4 still means 4.

This is the jobserver's rule at the step's grain: run with the tokens you get. A step granted
narrow runs longer, and its outcome says so.

### A run that does not fit waits by default, and says so

This reverses ADR 0001's 2026-11-05 amendment, which made waiting opt-in. Its one recorded
reason, that a wait nobody asked for looks like work, is answered by printing the wait: a
wait that names what it waits on does not look like work. The evidence against refusing at
once is the 2026-11-07 transcripts above: every refusal was answered with the same flag
within seconds, and the flag then rode along on 160 commands. A refusal every caller answers
the same way carries no information; it is the toll
[Friction that carries meaning](../design/principles/friction-that-carries-meaning.md) removes.

- `budget_wait` defaults to 30 minutes, or the run's own deadline when that is sooner. The
  flag, `MAGUS_BUDGET_WAIT` and `magus.yaml` still set it, and 0 still refuses at once.
- A run that parks prints, on stderr, what it waits for, who holds the room (pid, project,
  target, directory), the claim's breakdown, and its place in the queue; then again whenever
  the holders change, with the time waited and the time left; nothing on a timer. This is
  the existing parked-frame text of the 2026-11-05 amendment above it, now on by default.
- Any time-to-seat it prints says "at least": estimates from recorded durations ran 2.4 to 4
  times low on 2026-11-07.
- At the bound it is refused with MGS3009, as today, naming the holders and the bound.
- A leased worker still never waits: the guard's sleep-poll rule and the owed check are
  unchanged, and the worker's check is run with `--budget-wait 0`.

### Refusals and waits are recorded in a JSON Lines store

Each refusal, and each claim seated after waiting on other runs, appends one entry to `admissions.jsonl`, beside `broker.log`, which keeps the newest 5000: when, who asked
(pid, project, target, directory, lease), the claim's breakdown, the held state at the
verdict, the verdict, and for a wait how long until seated. One `.jsonl` file, last line per
key wins, compaction by rename, encoding on the entry type. The 20-entry in-memory ring
stays for `server status`. The owner field records the lease the run's `BAGGAGE` or checkout
binding names; on 2026-11-07 all 104 read `unleased`, which is fixed with it.

### A refusal and a wait print the claim's breakdown

MGS3009, a parked wait and `magus server status` print, for each holder over a quarter of the
budget, what its claim is made of: its own measured peak and the number of runs it was
measured over. For example: "conventions 4.3 GiB under lint: own peak over 12 runs".
`magus session -o json` already records sizing; the breakdown is that record, printed.

`magus doctor` adds one check, `budget-admissions`, over the admission store: refusals in the last day, and how many
waits ran past a quarter of their bound. It reports counts and targets; it changes nothing.

### `job fork` prints no budget advice for a seatless row

`job fork` prints no budget advice for a row that holds no seat, as the spawn rule already does.

### No new role

Eli asked whether a role that consumes no slots would help. The roles already split the
budget that way: a read-only row holds no seat, a worker never waits and files its check
owed, the integrator waits, and `job exec` without a command holds nothing. A new role would
name a seatless runner that already exists as a read-only row. What starved the slots was the
claims of the runs that did take seats.

### Principles checked

- [Measured, not asserted](../design/principles/measured-not-asserted.md): relied on. The
  composer's measured peak bounds the summed estimate, every figure above has a sample and a
  date, and the refusal store makes the next review rest on records instead of transcripts.
- [Delete before you add](../design/principles/delete-before-you-add.md): relied on. The fix
  removes double counting and the margin rather than adding a budget, a role or a knob.
- [Friction that carries meaning](../design/principles/friction-that-carries-meaning.md):
  relied on. A refusal by a phantom claim, and a refusal every caller answers with the same
  flag, are tolls and are removed. A wait at its bound still refuses.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. The wait is a
  default, so it prints when it parks and why; every member left out of the fold, and every
  one held flat, is named in the breakdown.
- [Determinism at chosen points](../design/judgment/determinism-at-chosen-points.md): bends.
  Sizing reads each claimant's own history, not which members already passed or hit the
  cache. Elastic grants depend on the held state and the queue at admission, as admission
  already does. Eli decided this on 2026-11-07.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md): relied
  on. The breakdown is the reason for a memory refusal and for a wait.
- [Automation you can interrogate](../design/judgment/automation-you-can-interrogate.md):
  relied on. The refusal store answers how often and why after the fact.
- [Everything is a file](../design/principles/everything-is-a-file.md): relied on. The store
  is JSON Lines.
- [A record of refusals](../design/refusals.md): relied on. Claims stay declared or measured
  from recorded runs; nothing observed live enters a verdict beyond MGS3053's existing reading.
  Its machine-wide admission row is updated: the default wait is no longer 0.
- [The orchestrator plans](../design/principles/the-orchestrator-plans.md): relied on,
  unchanged. Workers still never wait.

## Alternatives

- **Raise the budget, or default to a smaller reserve.** Lost: at 15:53 the host had 1.2 GiB
  unused while magus held 3.2 GiB of claims; the rest was other software. A larger budget
  admits more against memory that is not there. Reopen if a host's non-magus use is measured
  low across a week of refusals.
- **Taken, without the cache step.** Every composer's inline members claim their own seat,
  as `skip_cache` composers' do, and no cache step is added: a claim without one leaves the
  lexical cache boundary (run.go:921-926) unmoved. Eli chose this on 2026-11-07. Reopen if a
  composer is found whose inline members still over-claim after this record.
- **Keep the 1.25 margin, or take 1.1.** Lost: with the fold fixed, 1.25 fits three of the
  four targets above and 1.1 fits all four by 0.6 GiB; MGS3053 now covers the overrun the
  margin guarded. Reopen if MGS3053 fires on a host while every claim fit.
- **Claim the p90 of peaks.** Lost: one run in ten would exceed its claim by design. Reopen
  with ADR 0012's overrun-to-peak rule built and measured.
- **Oversubscribe CPU: budget slots at 1.5 x cores.** Lost: ADR 0001 records four concurrent
  gates at load average 13.7 with tests failing because they were starved. Elastic slots fill
  idle cores without promising more than exist. Reopen if load average at refusal, recorded in
  the refusal store, shows idle cores while slots are full.
- **Elastic grants of full width whenever free.** Lost: the replay leaves the nine-target
  burst refused behind holders that took every slot. Reopen if fair-share grants show steps
  starved of width while nobody waits.
- **Keep refusing at once, and print an estimate.** Lost: it keeps the round trip and the
  copied flag, and the estimates ran 2.4 to 4 times low. Its estimate survives inside the
  wait's parked line, marked "at least".
- **Repo policy: this workspace sets `budget_wait` in magus.yaml, the binary stays at 0.**
  Lost: every workspace with agents pays the same toll, and a shipped default each workspace
  overrides the same way is the wrong default. Reopen if another workspace shows a reason to
  refuse at once.
- **Default by caller: a terminal waits, a leased worker refuses.** Lost: the same command
  would reach a different verdict by who typed it, and refusals.md removed "detecting where
  magus runs, to pick a default" (`TestNoEnvironmentSniffing`). The worker already passes
  `--budget-wait 0` through its check.
- **A ticket: a refusal holds the caller's place for a short window.** Lost: 2026-11-05
  deleted exactly this (wait-reserves-its-seat), and ADR 0001's 2026-10-31 amendment says a
  refused claim holds no place in the queue; a ghost place blocks others.
- **A short grace wait of 10 to 60 s.** Lost: real waits ran minutes, so a grace absorbs few,
  and it adds a second constant beside `budget_wait`.
- **Tune each machine once, at setup.** Lost: a calibration describes the code it ran on and
  goes stale with the next change to a target. The history is already per host and per
  target, read when a claim is sized, and learns from every run. Reopen if a fresh machine's
  first three runs per target are shown to cost more than a calibration would.
- **Learn the budget itself from swap or pressure history.** Lost: the budget would then
  depend on what else ran last week, the brake the record of refusals refused in its
  2026-10-16 form. MGS3053 reads pressure at admission, which is enough. Reopen if MGS3053
  fires repeatedly on one host while claims fit.
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
  After the fold and margin changes: lint 5008 MiB; lint and test 12322 MiB fit; two lints
  10016 MiB fit; lint, test, buzz-test and spells-generate all fit (19024 MiB).
- Of the 20 refusals replayed, memory refuses none; the 11 slot refusals start at a fair-share
  width, and anything still kept out waits instead of exiting 75.
- A run now blocks by default for up to 30 minutes. An agent harness that moves long calls to
  the background sees the parked line first; a script that relied on exit 75 at once passes
  `--budget-wait 0`. This is a deliberate break: the changes fragment says so.
- A step granted fewer slots than its width runs longer; its outcome records the grant, so
  its duration band widens honestly.
- Sizing at claim time runs once per step admission instead of once per plan; its inputs are
  the run's passed set and a local key lookup the admission already makes.
- Tests that pinned the fold, the margin and the zero wait changed with them.
  `types.SizedChainMemory`, `types.ChainMemoryMB`, `types.ChainMemory`, `types.MemorySizer`
  and `types.MembersClaimTheirOwn` are removed, which updates
  internal/commands/testdata/api-go.lock.
- Docs updated with the code: docs/concepts/concurrency.md's budget section; MGS3009.md,
  which no longer says the maximum rather than the sum; ADR 0001 and ADR 0012, each with an
  amendment pointing here; docs/design/refusals.md's machine-wide admission row;
  docs/design/principles/friction-that-carries-meaning.md; docs/concepts/workspace.md's
  `memory_mb` row; and the magus-multi-agent skill's wait default. The generated config and
  man pages follow from internal/config/config.go and are regenerated by the integrator.

## Open questions

- Answered by T1 (out1c639c47c5dd): before this record, a composer's recorded peak included
  its inline members' processes. No upper bound was built, so the measurement it asked for is
  not needed.
- How wide is the duration band once grants vary? Elastic slots may widen D90/D10 enough to
  blunt ADR 0012's packing.
- Which `concurrency_profile` is the default? docs/concepts/concurrency.md says `balanced` in
  one section and `aggressive` in another; this host runs `aggressive`.

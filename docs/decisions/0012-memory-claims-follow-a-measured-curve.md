---
title: "ADR 0012: memory claims follow a measured curve"
order: 12
description: A memory claim today is 1.25 times a target's highest recorded peak, held for the whole step, and the members of one parallel ctx.needs call add their peaks as if they all peaked at once. ci's claim of 12.3 GiB is that sum. This record first pools run shapes so histories reach three runs, then replaces the peak with a time-bucketed curve recorded from the samples magus already takes, composes a call's members bucket by bucket, and admits a claim when every future instant fits the broker budget. A claim that overruns its own curve is charged its peak. It amends ADR 0001's summed-peak chain fold.
tags: [adr, decision, concurrency, memory, admission, broker, forecast, history]
status: accepted
date: 2026-10-20
supersedes: "ADR 0001, Consequences: the machine claim keeps the chain fold at its fan-out's summed peak"
---

# ADR 0012: memory claims follow a measured curve

## Context

### The headline case: ci's summed claim

`magusfile/ci` calls `ctx.needs(lint, build, test, security)` once, and
`dispatchBuzzTargets` (internal/interp/bindings/target.go:780) runs the four at once.
`types.SizedChainMemory` (types/target.go:590) sums the members of one call and takes the
max across calls. Each member claims `sizeMemoryClaim` (limiter.go), ceil(1.25 x max
peak):

| member   | claim (MiB)  | from                                         |
| -------- | ------------ | -------------------------------------------- |
| test     | 7235         | 5.65 GiB peak, 13 measured runs, 3 full runs |
| lint     | 4216 to 4301 | lint or its `lint_rules` chain call          |
| build    | 1102         | `go_build`                                   |
| security | 0            | no history, no declaration                   |

The sum of the one parallel call, test 7235 MiB + lint about 4216 MiB + build 1102 MiB, is
12553 to 12638 MiB and prints as 12.3 GiB (`FormatMB`, internal/cache/machine.go:389).
Another holder had 12.2 GiB of the 18 GiB balanced budget, so the run was refused. No
budget profile admits it: 12.2 + 12.3 = 24.5 GiB exceeds `aggressive`'s 24064 MiB.

The sum is right about concurrency and wrong about time. It assumes test, lint and build
peak at the same instant. Nothing measures whether they do: ci's own outcomes record about
250 MiB, the Buzz body alone. The rule is ADR 0001's (lines 241-243) and two tests pin it:
`TestChainMemorySumsOneCallAndPeaksAcrossCalls` (limiter_test.go) and
`TestChainMemoryMBPeaksOverOneCall` (types/target_test.go:177).

### What is measured today

- **Per exec.** `spawn.Exec` keeps `max(ru_maxrss, treePeak)` (internal/proc/spawn/exec.go:306).
  The kernel figure folds a subtree as a maximum, so on its own it names the largest process,
  not what `go test`'s package binaries held together (exec.go:139-156).
- **In-run sampling exists.** `treeSampler` (internal/proc/spawn/tree_sample.go:30) totals
  the live process tree every 200ms (`treeSampleEvery`, :22) through `mem.TreeBytes`. It keeps
  only the running maximum (`record`, :78) and drops every sample's time.
- **Per step.** `types.RecordPeakRSS` (types/exec_cost.go:61) keeps the max across a step's
  execs. Two commands a step runs at once (internal/interp/bindings/command.go:528) are
  recorded as the larger, not the sum.
- **Stored.** `forecast.Outcome.MaxRSSBytes` (internal/ci/forecast/history.go:257), written at
  run.go:2796, one per run, at most `OutcomeWindow` = 100 per target (history.go:94-95).
  Replayed ops record nothing, so a warm run adds no sample.

### What each platform allows

- **Linux.** `TreeBytes` (internal/sys/mem/tree_linux.go:17) walks
  `/proc/<pid>/task/<tid>/children` and reads `/proc/<pid>/statm`, touching only the tree.
  `smaps_rollup` gives PSS but costs a page-table walk per process; statm is the cheap one.
  cgroup v2 `memory.current` and `memory.peak` would total a tree exactly, but magus creates
  no cgroup: the sandbox only binds `/sys/fs/cgroup` read-only (internal/sandbox/defaults.go:218),
  and the broker's systemd unit puts services, not runs, in its cgroup (broker/units.go:64).
  `memory.current` also counts page cache, which a build churns. Not free today.
- **macOS.** `TreeBytes` (internal/sys/mem/tree_darwin.go:47) reads the whole process table
  with one `kern.proc.all` sysctl, then `proc_pidinfo(PROC_PIDTASKINFO)` per tree member
  (procrss_libproc_darwin.go:33, or the raw `SYS_PROC_INFO` trap under `noffi`,
  procrss_syscall_darwin.go:28). `task_info` needs a task port that `task_for_pid` refuses
  without an entitlement, so it is out. `proc_listchildpids` is `proc_listpids(PROC_PPID_ONLY)`,
  the same trap with call 1, reachable without cgo; it would replace the table read with one
  call per tree node. `proc_pid_rusage`'s `ri_phys_footprint` counts compressed memory, which
  is what the OS charges, but it disagrees with `ru_maxrss`; switching metrics is out of scope.
- **Cost.** tree_sample.go:18-21 measures about 1.5ms per darwin sample on a busy machine, so
  200ms is under 1% of one core per running exec. Ten concurrent execs each reading the table
  is 7.5%. Linux reads only the tree and is cheaper.

### What forbids the obvious version

- `MachineBudget` is "DECLARED, not observed, so the same command on the same host reaches
  the same verdict" (internal/cache/machine.go:28-29), and sys/mem says the observed half
  only ever warns (internal/sys/mem/watch.go, `LowHeadroomDivisor`).
- [A record of refusals](../doctrine.md#a-record-of-refusals) refused, on 2026-10-16, "a
  pressure brake that withholds new claims while observed memory is high". Its reason: the
  verdict would depend on what else is running. The row above it says a claim is "declared or
  measured from recorded runs, never observed live".
- ADR 0001 rejected clamping an oversized claim because it "moves the arbiter to the OOM
  killer" (line 199). Packing below the summed peak is the same risk, priced below.

## Decision

### A shape forks only on what changes the work

_Planned._ `forecast.Shape` today forks on every active charm and on `ArgsHash` of the
forwarded args (`NewShape`, `Shape.matches`, internal/ci/forecast/history.go). Root `test`
has 14 runs in the forecast history from 10-17 to 10-21. Of those, 13 are measured runs,
the failed one left out, and 3 of the 13 are full runs, with no args forwarded. No shape
reaches 3: rw against no charms and one arg hash per diff split them, so every claim falls
back to the any-shape peak, 5.65 GiB over the 13 measured runs.
A curve needs three runs of its shape too, so unpooled shapes would starve curves worse than
they starve peaks. This rule lands before the curve for that reason.

A shape keeps only the charms the target's chain reads: the charms its `ctx.needs` closure
tests with `ctx.hasCharm`, or that an op it drives reads, as `readsCharm` (run.go) answers
today for the write grants. Of the args it keeps only whether any were forwarded, empty
versus non-empty, so a full run and a narrowed run never share a shape.

Narrowed runs pool with each other only to lower a narrowed run's claim, and that claim is
capped by the full-run peak. A narrowed run never sizes a full run. Under this rule `test`,
which reads only `extended`, pools its rw and no-charm full runs: 3 runs, 5.65, 5.44 and
5.11 GiB. That reaches three, but the pooled peak is still 5.65 GiB, so the claim stays
7235 MiB and ci's refusal is unchanged by pooling alone.

### A run records a curve, not a peak

_Planned._ `treeSampler` keeps every sample with its offset from the step's start, and the
step folds its execs into one curve: per time bin, the sum of each exec's largest sample in
that bin. That fixes the step-level max at command.go:528 too. A curve is K = 20 buckets of
the run's own duration (5% each), each bucket the largest sample in it, in MiB. The kernel's
`ru_maxrss` stays the floor of the curve's peak, as exec.go:306 does now.

One sampler serves every exec of a process: one table read per tick on darwin, whatever the
count of trees followed. Ten concurrent execs then cost what one does, under 1% of a core.

### Normalized time, stretched by the duration band

_Planned._ Buckets are fractions of the run, not absolute seconds. A cold `go test` and a
warm one run the same phases in the same order (compile, link, run packages) at different
speeds. Absolute buckets would put the predicted peak early on a slow run and late on a fast
one, and either way another claim would be admitted into the real peak. Normalized buckets
keep the phases aligned, and the duration uncertainty is handled once, at admission.

The profile per target and shape is the p90 of each bucket over the trusted runs (the same
`minMeasuredRuns` = 3 and `Shape` as today, limiter.go), times `peakMargin`, plus that
shape's p10 and p90 duration. It is computed when read, from the outcome window, like
`MeasuredRuns.Peak` (history.go:1129). No profile is stored apart from the outcomes.

### Siblings compose bucket by bucket

_Planned._ `SizedChainMemory` folds the members of one call by curve: each member's profile is
laid out in absolute time from the call's start, and the call's curve is the sum at every
instant. Sequential calls still take the max. The chain's claimed peak is the peak of that
sum, never above the summed peaks it replaces. For ci, test's link-and-run peak and lint's
analysis peak land in different buckets unless the runs say they coincide; when they do, the
claim stays 12.3 GiB and that is the honest answer.

### Admission packs curves under the budget

_Planned._ A claim carries its curve (`types.MachineClaim.Profile`) beside `MemoryMB`, which
stays the curve's peak. `MachineBudget.Request` admits c when, at every future instant t,
the sum of each held claim's remaining contribution plus c's stays within `budgetMB`.

A held claim that started at s, with duration band [D10, D90], contributes at t the largest
bucket over the fractions [(t-s)/D90, (t-s)/D10]: wherever in its band the run really is.
Past (t-s) > D90 it contributes its peak until released. Every contribution is a step
function that changes only at s + k x D10/K or s + k x D90/K, so checking the union of those
breakpoints over every claim is exact: O(n^2 x K), under 10,000 additions for 20 claims. The
clock is `MachineBudget.now` (machine.go:37) and the start is `machineEntry.started` (:45);
both exist. The free-slot rule (`freeSlot`, :324) and the refusal across invocations (exit 75)
are unchanged.

A claim with a tight band packs tightly. A claim whose shape runs anywhere from 40s to 400s
contributes nearly its peak for its whole life, which is the behavior today.

### No profile claims the peak and says so

_Planned._ Fewer than three runs of the shape with a curve, an outcome from before the curve
field, or a broker that predates `Profile`: the claim is its peak, held flat. `MemorySizing`
gains the reason, and the refusal and `describe` print it: "test: no memory curve for this
shape (2 of 3 runs); claimed the 7.1 GiB peak". The broker's verdict says whether it packed by
curve or by peak. Version skew in either direction degrades to the peak, which is today's
answer, and is named, not silent.

### Slots stop being derived from memory under the broker

_Planned._ `slotsForPolicy` (limiter.go) raises slots to `ceil(MB / (hostUsable / local
pool width))`: 3072 MiB per slot on 24 GiB with width 8. The broker divides nothing: it holds
18 GiB and 10 slots, so its own ratio would be 1843 MiB. The two units disagree because one
quantity is spelled twice. The broker reads megabytes directly (`slotsForPolicy`'s doc comment), so a
claim the broker arbitrates keeps slots as CPU, its declared or measured width, and memory as
megabytes. The conversion remains only for the local pool with no broker, where it divides
`mem.BudgetMB` by the local width, so both sides start from the same 0.75 fraction. ci's
"5 slots" then becomes its measured width.

### A claim that overruns its own curve is charged its peak

_Planned._ The client's sampler already reads the claim's own process tree. When that tree
exceeds the claim's predicted value for the current bucket, the client tells the broker once,
and the broker charges that claim its full peak from then on. Nothing is preempted.

There is no global admission brake. The verdict reads only magus's own claimed tree, never
host free memory, so the 2026-10-16 refusal row ("a pressure brake that withholds new claims
while observed memory is high") stands unchanged.

The overrunning run's recorded curve already widens the next profile, so no separate outcome
field is added: the profile learns retrospectively, the same way the peak claim learns today.

### Doctrine checked

- [No silent defaults](../doctrine.md#no-silent-defaults): relied on. Every fallback to the
  peak prints its reason, and the verdict says which mode admitted it.
- [Built for the expensive era](../doctrine.md#built-for-the-expensive-era): relied on. The
  profile is a fixed computation over recorded runs; no model is asked how much memory a run
  uses.
- [Everything is a file](../doctrine.md#everything-is-a-file): relied on. The curve is a field
  on the existing `Outcome` in the existing JSON Lines history, encoded by its own type.
- [Automation you can interrogate](../doctrine.md#automation-you-can-interrogate): relied on. A
  refusal names the instant the sum exceeds the budget and which claims make it up.
- [A record of refusals](../doctrine.md#a-record-of-refusals): relied on. The packing relies
  on the "measured from recorded runs" row, and the self-overrun correction relies on the
  2026-10-16 brake row, which stands: no claim waits on host free memory. It bends only the
  memory-admission row's "never observed live" wording, and only for magus's own claimed
  tree; Eli decided this on 2026-10-20. Determinism bends too: the verdict now depends on how
  long each held claim has run, a wall-clock fact rather than a host reading. Two identical
  states at the same elapsed bucket reach the same verdict.

## Alternatives

- **Keep the summed peak, tune the margin.** Lost: the margin is not the error; the sum is.
  Evidence: no profile admits ci (12.2 + 12.3 GiB > 24064 MiB). Reopen if curves show the
  siblings' peaks coincide in most runs.
- **Absolute-second buckets.** Lost: a run 2x slower than the profile puts its real peak where
  the profile predicts low use. Reopen if a target's duration band is consistently narrow
  enough (D90/D10 under 1.2) that alignment never matters; then both are equivalent.
- **Store the profile as its own entry.** Lost: a second store with its own compaction for a
  figure the outcome window already determines. Reopen if computing profiles at load shows up
  in `describe` latency.
- **cgroup v2 per run on Linux.** Lost for now: magus creates no cgroup, delegation needs a
  systemd scope per run, and `memory.current` counts page cache. Reopen if magus starts runs in
  their own cgroup for another reason; `memory.peak` would then replace the sampler there.
- **Measure only the chain's concurrent peak.** A scalar fixes ci
  but not two unrelated invocations whose peaks are minutes apart. The curve subsumes it.
- **Host hold.** Hold new claims while `mem.Available` is below the summed prediction. Lost:
  it is the brake [A record of refusals](../doctrine.md#a-record-of-refusals) refused on
  2026-10-16, "a pressure brake that withholds new claims while observed memory is high",
  because the verdict would depend on what else is running. The self-overrun correction
  reads only magus's own tree instead. Reopen only if that refusal row is reversed.
- **Shapes that fork on every charm and every arg hash.** Lost: they split root `test`'s 13
  measured runs so no shape reaches the 3 a trusted claim needs, and a curve needs three.
  Reopen if a charm the chain never reads is shown to change a run's peak.
- **Preemption** (pause or kill a run that overruns). Not proposed: ADR 0001 refuses moving the
  arbiter to the OOM killer, and a killed build wastes the work done.

## Consequences

- ci's claim drops to the peak of its members' summed curves when their peaks do not coincide.
- A misprediction can over-admit. Bounds: p90 per bucket, `peakMargin` on every bucket, the
  duration band, the overrun-to-peak rule, and kernel OOM as the backstop it already is when a
  declaration is wrong. A prediction is never worse than the observed run it was built from
  by more than the band allows.
- Store growth: 20 numbers per outcome, about 100 bytes of JSON, at most 100 outcomes per
  target: 10 KB per target. `HistoryVersion` goes from 8 to 9.
- Tests stay deterministic: the sampler takes an injected reader and tick, the budget an
  injected clock.
- The comment on `sizeClaim` (limiter.go), "the claim is held for the whole step", stops
  being true and is rewritten with the code.

## Open questions

- How often do cold and warm `go test` runs share a shape but not a band? The Go build cache is
  invisible to magus. If D90/D10 is wide for most targets, packing gains little; measure after
  the curve lands, before admission changes.
- Should a refusal across invocations say when the claim would fit? magus never waits on
  another invocation, so the hint must not read as a queue.

## Amendments

### 2026-10-21: a call's members claim their own

The curve did not reach ci. `SizedChainMemory` holds a composed target's peak flat, so
lint and test carried no curve, and the call summed their peaks. On an 18.0 GiB budget
across 10 slots, `magus affected ci` was refused every time: ci claimed 18.9 GiB, measured
over 8 runs, with "lint: no memory curve; summed the peaks of its ctx.needs call".

The members do run at once. `dispatchBuzzTargets` sends one call to `Pool.Dispatch`,
which submits every member before it waits on any. The sum was honest about that
concurrency. What it got wrong was the unit: under a skip_cache composer such as ci, the
run's interceptor already makes each same-project member a cache step of its own
(`RunAside`), and only `claimMachine`'s "a step beneath a claim takes none" rule kept those
steps from claiming.

- Each member that `dispatchBuzzTargets` hands to the interceptor takes its own machine
  claim (`cache.ChildrenClaimTheirOwn`). A member that fits waits for a seat beside its
  siblings, so four members that each fit and together do not all run, in turn.
- The composer's claim leaves those members out (`SizedChainMemory`'s `ownClaims`). One
  predicate, `types.MembersClaimTheirOwn` (the composer's skip_cache), decides both the
  fold and the dispatch, so a member is never counted twice or left uncovered.
  Cross-project members still run inline and still fold. ci now claims
  its own inline work only, not a floor of its largest member. A floor would hold memory
  no process uses while the member it copies holds its own.
- Any step holding its own claim lends it, with its limiter slots, for every ctx.needs
  wait, same-project or cross-project, and takes the claim again after
  (`cache.LendMachineClaim`). No claim is held while it waits on another. Keeping it
  hung twice the same day. First, `magus affected generate:rw`: root generate's members
  each composed generators of their own, held all 10 slots, and waited on leaves that
  could never be seated. Then, with only skip_cache composers lending, a `vcs resolve`:
  ten generators held every slot, each waiting on types-generate, which generate had
  also dispatched and which needed a seat of its own. Refusing a member that could not
  fit beside its waiting composers caught only the composers above it, not siblings or
  shared needs.
- The cost is twofold. The retake can be refused by another invocation after the
  members ran, a loud MGS3009 where the alternative was a hang. And members that run
  inline rather than as steps run on no claim while it is lent; the claim's figure still
  folds them.
- `ownClaims` drops members from the fold only for the step being sized.
- A member larger than the whole budget is refused by name, as before.
- `magus doctor`'s gate-fits check sizes ci and every same-project step it reaches with
  the admission sizer and fails on any step larger than the whole budget.

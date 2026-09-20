---
title: "ADR 0001: bounded concurrency with recursive invocation"
order: 1
description: Where magus takes a concurrency slot, and why it stops taking one for a target's whole body and starts taking one around the work that executes. Records the forces behind the isolation gate, the slot yield and the deadlock detectors, the alternatives rejected since May 2026, what other build systems that support recursive invocation do, and the staged path that keeps each step reversible.
tags: [adr, decision, concurrency, deadlock, scheduler, recursion, slots, isolation]
---

# ADR 0001: bounded concurrency with recursive invocation

- **Status:** Proposed
- **Date:** 2026-09-19
- **Supersedes:** nothing. This is the first ADR in this repository.

## Why this document exists

magus has rebuilt its concurrency machinery several times since May 2026, and runs hung
along the way. Most of them are one shape: work dispatched inside a running target waited
for a resource that target's own ancestor held. Each change fixed the instance in front of
it, and nobody wrote down the rule.

The most recent change deleted a mechanism nobody had ever justified, and establishing that
took an afternoon of archaeology. The first draft of this document then asserted five
things about the code that were false. Both are recorded below.

## Context

### The requirement that shapes everything

**A running target can invoke other targets.** `ctx.needs(other)` dispatches another target,
in this project or another, from inside a target body; that target can do the same;
`magus\cmd` recurses across a process boundary. This is how composite targets are written
and it is not up for removal.

Every difficulty below follows from combining that with a bounded worker pool.

### What magus does today

The local limiter is a FIFO weighted semaphore. A step takes slots in `Cache.admit`; the
Buzz pool takes the **same** limiter again per target; the spell fan-out takes it a third
time. Every nested call yields: `Limiter.Yield` releases every slot the context says are
held, runs the nested work, then re-acquires **non-cancellably at the back of the FIFO**.

Held across waits and never yielded: the keyed cache lock (`hashLocks`), for the whole body.
Inherited rather than yielded: the run-isolation lease and the cross-process machine claim.

Two watchers remain, plus one deleted:

| Code | Watches | Grace | Kind |
|---|---|---|---|
| MGS3013 | the slot pool | 3s | reduction over **registered** holds |
| MGS3012 | invocation silence | 15m | timeout |
| ~~MGS3015~~ | the isolation gate | 30s | deleted 2026-09-19 |

MGS3013's 3s grace is a settling delay rather than the test. The test is a predicate: "no
holder can release and the free count cannot satisfy any waiter". But it is **one-sided**,
and its own code says so: raw acquisitions that register no hold "leave the sum short" and
answer no. The Buzz pool, the spell fan-out, `os.with_slots`, `archive.*` and the daemon's
adopted runs all acquire raw. It cannot produce a false positive from that; it is blind to
any wedge through one of those holders.

The marking discipline is the guess: a `blocked` string somebody must remember to set. A
wait that forgets it reads as running and hangs silently; a mark attributed to the
wrong record refuses healthy runs. Both happened; the second is why MGS3015 is gone.

### The forces, recovered from the history

- **`Limiter.Yield` (initial commit, 2026-05-07).** A composite step is admitted on a slot
  and dispatches children needing slots from the same pool; at low concurrency the parent
  holding while its children queue deadlocks. Adopted with a single clause of justification.
  The jobserver rule now cited as its principle first appears **four months later**.
- **`Step.Exclusive`** (named 2026-07-01, renamed from `isolated`). Adopted with **no
  recorded reason**: no motivating target, no incident. Everything built on it since serves
  a flag nobody argued for. `generate` at the root, which drove that engineering, dropped
  the flag once the engine honored it, because honoring it serialized every `ci` member.
- **Lease inheritance (Sept 2026).** Two hangs forced it: 27 minutes at 13s of CPU when a
  needs child double-claimed memory its parent held, and 19 minutes with every project lock
  held when a child asked for the exclusive side of a gate its ancestor held shared.

### The contradiction already in the codebase

The dependency barrier has never deadlocked, and its doc says why: *"Every goroutine is
launched immediately and blocks on deps without holding a slot, so the pool never
deadlocks."* That is the opposite principle from `Yield`, in the same package, and nobody
reconciled the two.

## Decision

**The rule: no holder of a local slot may wait on anything in-process.** A slot is taken
around work that executes, not around a target body that may suspend.

Adopted in three stages, each independently reversible, because the end state touches
admission, the Buzz pool, `proc`, the daemon and every raw acquisition site.

**Stage 1. Direct hand-off replaces yield-and-retake.** The parent hands its slot to its
first child; the last child hands it back. Same parallelism, no non-cancellable re-acquire,
no FIFO priority inversion where a parent that already owns work queues behind strangers,
and a continuous hold record. This is GNU make's token rule ("every recursive invocation can
always run at least one job"), which magus already adopted for the *machine* budget as
`freeSlot`. Both detectors stay. Small, and valuable even if the later stages never land.

**Stage 2. Leaf acquisition inside the yielded regions.** A slot is taken where a subprocess
starts or a CPU-bound operation runs, at the weight of **the target's own declaration, not
the chain fold**. `magus\cmd` stays a hand-off site rather than becoming a leaf, because
parent and child share one limiter under the daemon. The daemon's shared limiter gets
make's free-slot rule. Measure live Buzz sessions on `affected ci` before proceeding.

**Stage 3. Remove body-level admission.** Step admission, the Buzz pool's per-target
acquire and the spell fan-out's acquire all stop taking slots. Only then delete MGS3013 and
the isolation gate.

**Stage 0, and the prerequisite for the rest: `Step.Exclusive` is split into the two things
it conflates and then deleted.**

It was introduced for GREEDY targets, ones that will use the whole machine. None of the ten
targets that declare it is greedy. Every one gates on the working tree: six `generate` targets and
`console:build` drift-check via `git status`, `release` mutates go.mod and creates tags,
`release-index` pushes a branch. That is mutual exclusion on one shared mutable resource,
which is a different thing from greed, and both are different from "exclude every batch peer",
which is what it was implemented as.

- **Greed is a weight**, and already has one: `slotsForPolicy` derives slots from `memory_mb`,
  which is also what the machine budget arbitrates on. Nothing to build.
- **A quiet tree is a named region**, not a target flag: `ctx.exclusive("worktree", fn)`, a
  keyed mutual exclusion acquired in canonical order when a region names more than one.
- The ten wrap **only their measurement**, the hash-before against hash-after
  comparison, not their generator chain. The root magusfile already wrote this diagnosis and
  did not act on it: "narrowing the exclusive region to the measurement is the fix, and it is
  not this line."

Folding exclusivity into "acquire every slot at a leaf" would have been wrong here: it
changes those targets from "this body runs alone" to "each subprocess runs alone with peers
interleaved between them", and a peer writing the tree between the generator and the
`git status` is the race the flag exists to prevent. Buck2's `ExclusiveAccess` and
Bazel's `exclusive` are per-action because their actions are leaves; magus's are bodies.

With the flag gone, the run-isolation gate, its lease and the inheritance rule go with it.

MGS3012 stays throughout, as the only timer. It bounds invocation *silence*, not a resource
wait, and it is the backstop for waits whose subject is outside this process.

### What this buys

Hold-and-wait is one of Coffman's four necessary conditions. Removing it for the resource
recursion re-enters makes that cycle impossible rather than detected. A slot x cache-lock
cycle, which is the shape Gradle keeps hitting, also becomes impossible, because a leaf
takes no `hashLocks`.

It does **not** make runs deterministic, and this ADR claims no such thing. Admission order
among simultaneously ready steps is unspecified today and stays unspecified; neither Shake
nor Bazel is schedule-deterministic either. The honest claim is narrower: **the schedule
space contains no in-process deadlock.**

## Alternatives considered

**Keep yield-and-retake.** Gradle's `WorkerLeaseService.withoutLocks` does exactly this, and
Gradle has been fixing deadlocks from it since 2021: issues 17812 and 20269 (both since
fixed) and 37613 (open, a 2026 regression). The shape in 20269 and 37613 is magus's: a lock
held while a lease is re-taken. Gradle's cycles need a second lock beside the lease; magus
has one, `hashLocks`, held across the whole body.

**Exact detection via a wait-for graph.** Postgres, InnoDB and the JVM do this and it is
correct. It is also what this codebase just failed at: MGS3015 *was* a reduction and it
answered off an aliased record. A full graph would cover five in-process wait types, cost
nothing at runtime, and inherit exactly the marking discipline that produced the incident.
It names deadlocks; it does not remove them.

**Drop the local limiter entirely and let the machine budget be the only bound.** Rejected,
priced: there is no gate without a daemon, the budget fails **open** when the daemon is
lost, hand-off polls at 200ms, and its claim is per step so it cannot bound leaves anyway.
`affected ci` on a laptop with no daemon would launch every test suite at once.

**Avoidance (Banker's algorithm).** Needs each process to declare its maximum resource need
up front. A target's transitive slot need is unknowable before its body runs. No build
system uses it for this reason.

**Ordered acquisition alone.** Already in place (machine, gate, slot, key) and kept. Breaks
cycles *between* resource types; cannot address a re-entrant request for the same type at a
deeper nesting level, which is the only cycle recursion creates.

**Priority ceiling protocols.** Require static knowledge of which task can ever take which
resource. magus has neither priorities nor a static map of nested work.

### Rejected earlier, recorded so they are not re-proposed

Yielding the machine claim (its re-acquire is fallible and can be refused after children have
run); releasing the exclusive lease across a fan-out (an all-fan-out body is shareable for
its whole life); releasing the shared lease (it reopened the window inheritance closed); an
RWMutex as the gate (not context-aware; writer preference parked sixteen readers); clamping
an oversized machine claim (moves the arbiter to the OOM killer); a timeout as the answer to
a nested-lock deadlock (converts a hang into a late failure that still does not say why); and
a detector for the cross-root machine wedge, built and dropped 26 minutes later in favor of
a structural rule.

## What other systems do

| System | Recursion | What is bounded | Nested wait |
|---|---|---|---|
| **GNU make** | yes | one token per *job* | the recipe's token becomes the sub-make's implicit token: direct hand-off |
| **Shake** | yes | `shakeThreads` over running rules; `Resource` around the expensive part | `need` captures a continuation; no thread blocks |
| **Buck2 / DICE** | yes | permits around *command execution* | an awaiting computation holds no thread; identifier semaphores taken before permits |
| **Bazel** | no, actions are leaves | `--jobs` over actions | not evidence about recursion |
| **Tokio** | yes | worker threads | `block_in_place` hands the worker's tasks away first |

make holds its bound across a nested wait by design, with bounded overshoot; that is
stage 1. Shake, Buck2 and Tokio move the bound off the suspending computation; that is
stages 2 and 3.

## Consequences

**Good.** The deadlock class recursion creates becomes impossible rather than detected, for
both the slot pool and the slot x cache-lock shape. Stage 3 removes the isolation gate,
MGS3013 and the marking discipline they read. `magus status` means "executing" again, rather
than a seat held by a body that is waiting.

**Bad, and named rather than discovered later.**

- **`--step` changes meaning, and is answered rather than noted.** Today it sets concurrency
  to 1 and means "one target at a time". Under leaf acquisition that would become "one
  subprocess at a time", with every body interleaving its output: a behavior change in a
  debugging feature, which is the worst place for one. The answer reuses stage 0: `--step`
  wraps each step's body in a named exclusion, which restores "one target at a time" exactly
  and adds no second scheduling mode.
- **Peak live state is less bounded, and unmeasured.** Every step past its dependencies
  becomes a live goroutine plus a Buzz session, and the worker pool caps retention rather
  than creation. Stage 2 must measure session RSS times live steps before stage 3.
- **The discipline moves rather than disappearing.** "A slot is taken where work executes"
  is itself something a future site must remember to do. It fails **open** (oversubscription)
  rather than closed (a silent hang), which is the better direction. A site that forgets
  still oversubscribes the pool.
- **Leaf weight is the target's own declaration**, while the machine claim keeps the chain
  fold. Without that split a parent's own small step would be weighted at its fan-out's
  summed peak.

**Not addressed.** Cross-process cycles between two invocations with mutual cross-project
needs; nothing detects those today either.

## When to revisit

The real dependency is **the daemon's shared limiter**: parent and adopted child draw from
one pool, so `magus\cmd` cannot become a plain leaf without make's free-slot rule. If that
rule proves insufficient under the daemon, stage 2 stops there and `magus\cmd` stays a
hand-off site permanently, which is stage 1's end state and still an improvement.

If the rule itself is rejected, because slots must be held for a body's whole life for a
reason this ADR has not found, then prevention is off the table and the honest answer is
exact detection: a short optimistic wait, then a wait-for graph reduction, no grace beyond
that, and the cycle named in the refusal. Pair it with stage 1 regardless.

## Corrections to this document's first draft

- It claimed the CI shard forecaster arbitrates declared `memory_mb`. It does not: it packs
  on measured peak RSS from run history and reads no declaration.
- It claimed the decision would charge memory per leaf. It would not: the machine claim is
  taken outside `admit` and is untouched by any stage here. The revisit condition built on
  that premise could never have fired.
- It said two release targets use `Exclusive`. Ten do, and most are composites.
- It called MGS3013's predicate Holt reduction without qualification. It is sound only over
  registered holds and is blind to five raw acquisition sites.
- It said magus is the only system in the table holding its bound across a nested wait. make
  does too, by design.

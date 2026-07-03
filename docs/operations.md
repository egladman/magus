---
title: Operations
description: Defines an Operation, its place in the Spell to Target to Process hierarchy, and how ops compose into runnable targets.
tags: [operations, ops, hierarchy, spells, targets, ci, work-model, execution]
---

# Operations and the magus work hierarchy

This document defines an **Operation**, magus's smallest named unit of
tool work, and fixes its place in the hierarchy between [Spells](spells.md),
[Targets](targets.md), and the result a run produces. It also disambiguates the
two words magus overloads, _op_ and _Target_.

> **Status.** The hierarchy, the `ExecResult` value type, and the `ispell.Op` (née
> `ispell.Target`) Operation type all exist today. A per-op `OpResult`/`TargetResult`
> consolidation was prototyped and removed as speculative (no consumer); the run
> path reports at the **target** level only, via the `target.result` event
> (`internal/report`). The Operation-layer rows below are kept as the conceptual
> model, marked **(not built)**.

## What an Operation is

An **Operation** (op) is one tool-native action a [Spell](spells.md) exposes,
named after the CLI command it runs: `go-build`, `go-vet`, `golangci-lint`,
`cargo-clippy`, `eslint`. It is the unit a [Target](targets.md) _composes_: a
target body calls ops, ops do the tool work.

An Operation is one of two declarative shapes, and the shape is its **kind**: a
**command** op returns a `Command` (`{bin, args, charms}`) magus forks directly
(no shell, one process, run to completion) — the default; a **service** op returns a
`Service` (`{command, readiness?, stop?}`), a long-running process `magus run` forks
in the foreground and **blocks** on. You author either as a function that returns it
(`fun(Target) > Command` or `fun(Target) > Service`) or as a bare record; the kind is
inferred from the return
(see [An operation is a command or a service](spells.md#an-operation-is-a-command-or-a-service)).
Because both are declarative data, the argv is charm-patchable, cache-keyable, and
previewable without running. The kind lives on the op, so one spell mixes command and
service ops. In-VM work that magus neither forks nor blocks on (a remote cache
backend) is not an op at all: it is a separate contract magus's core invokes by name.

An Operation is _how_ a tool performs an action; a [Target](targets.md) is _what_
you run. You **bind** spells (which contribute ops) and **invoke** targets (which
call ops). A target with no ops of its own, like `ci`, is pure composition: it
only `needs` other targets.

## The work hierarchy

```text
Spell ──exposes──▶ Operation (op)              go-build, eslint, golangci-lint
  │                   │  a command → one process (no shell)
  │                   ▼ runs
  │                Process  ──yields──▶  ExecResult
  │
Target (export fun) ──composes──▶ Operations  +  ──needs──▶ other Targets
  │
  ▼ run by the dispatcher (cacheable, charm-modified)
target.result event   (per target; no per-op breakdown)
```

| Layer         | Entity                  | Cardinality                   | Identity                             |
| ------------- | ----------------------- | ----------------------------- | ------------------------------------ |
| **Spell**     | a library of operations | many spells per project       | `name` + its ops                     |
| **Operation** | one tool-native action  | many ops per spell            | `spell` + op name                    |
| **Process**   | one forked command      | 1 per op (0 for a no-op marker)   | argv                                 |
| **Target**    | a runnable `export fun` | one per name per project      | `Path + Name` ([Target](targets.md)) |

Charms ([charms.md](charms.md)) sit orthogonal to this stack: a charm rewrites an
Operation's argv (_in what manner_ it runs), it is not a layer of its own.

## Results: what each layer produces

| Result              | Layer     | Shape                                               | Returned or emitted                                                                   | Status          |
| ------------------- | --------- | --------------------------------------------------- | ------------------------------------------------------------------------------------- | --------------- |
| **`ExecResult`**    | Process   | `{stdout, stderr, code, ok}`                        | **returned** by `os.exec`, `magus.cmd`/`run`/`describe`/`insight`/`doctor`, a Capture op | exists          |
| `OpResult`          | Operation | `ExecResult` + op identity (`spell`, `op`)          | would be returned by the op handler                                                   | **(not built)** |
| **`target.result`** | Target    | `{project, target, status, cache_hit, duration_ms}` | **emitted** by the dispatcher (`internal/report`)                                     | exists          |

- **`ExecResult` exists in both worlds.** It is the Go `run.ExecResult` and the
  spell-op **capture record** a `Capture: true` op returns "instead of void": the
  same `{stdout, stderr, code, ok}` shape `os.exec` returns.

- **The target result is emitted, not returned.** A target is cacheable, and on a
  **cache hit the body never runs**: outputs are replayed without executing the
  `export fun`. A return value cannot exist on a hit, yet a cache hit is exactly
  what you most want to report. So the dispatcher assembles and emits a
  `target.result` event from `cache.OnResult`, which fires for both the ran and the
  cached case. It reports at the **target** level; a per-op `[OpResult]` breakdown
  was prototyped and removed as speculative (no consumer, and it misattributed ops
  across the cross-project boundary).

## Disambiguating "op" and "Target"

magus overloads two words. Formalizing **Operation** fixes the first and exposes a
latent misnaming in the second.

**Three unrelated "op"s.** Only the first is the Operation defined here:

| Term          | Type        | Shape                            | Domain                                        |
| ------------- | ----------- | -------------------------------- | --------------------------------------------- |
| **Operation** | `Operation` | `spell` + op name + impl         | the spell op defined in this doc              |
| **PatchOp**   | `PatchOp`   | `{op, path, value, from}`        | RFC 6902 charm patch ([charms.md](charms.md)) |
| **RemoteOp**  | `RemoteOp`  | `{op, outcome, duration, bytes}` | remote-cache backend call (telemetry)         |

`PatchOp` and `RemoteOp` keep their names; they are genuinely different
operations. magus never names anything just `op` in the spell API, for exactly this reason.

**Two "Target"s.** These are distinct and must not be conflated:

- **`types.Target`**: the addressable **work-unit** `Path + Name`, plus charms
  and changed files. This is _the_ Target ([targets.md](targets.md)).
- **`ispell.Op`** (formerly `ispell.Target`): "a single dispatchable surface of a
  spell," i.e. an **Operation**. It was named `Target`, colliding with the
  work-unit above; renamed to `Op` to formalize this vocabulary.

### Naming decision (done): `ispell.Target` → `ispell.Op`

`ispell.Target` _was_ an Operation misnamed as a Target. It is now `ispell.Op`
(`Spec.Ops`, `OpNames`, and the resolve/fork/bind paths followed), wire formats
preserved. The docs warn against substituting "Operation" for a work-unit Target
([targets.md](targets.md)); that warning is about `types.Target` and never
protected `ispell.Target`, which was the actual offender.

## Relationship to the value types

The serializable Buzz value types model the _nouns_ around this hierarchy:

| Value type        | Models                                                                                                           | Layer it touches            |
| ----------------- | ---------------------------------------------------------------------------------------------------------------- | --------------------------- |
| **`Target`**      | a resolved work-unit (`Path + Name + charms + files`) plus its per-target policy (`skipCache`, `exclusive`, `slots`, ...) | Target                      |
| **`TargetQuery`** | an unresolved dependency edge (a query → 0..N Targets)                                                           | Target (`magus.needs` edge) |
| **`ExecResult`**  | the `{stdout, stderr, code, ok}` outcome of one process                                                          | Process                     |

`TargetQuery` _produces_ `Target`s; a `Target` is run as a set of `Operation`s;
each `Operation` yields an `ExecResult`.

## Glossary

| Term               | Meaning                                                                                               |
| ------------------ | ----------------------------------------------------------------------------------------------------- |
| **Spell**          | A library of tool-native Operations for one toolchain ([spells.md](spells.md)).                       |
| **Operation (op)** | One tool-native action a spell exposes, named after its CLI command. The unit a target composes.      |
| **Target**         | A runnable `export fun`; the work-unit `Path + Name` you invoke ([targets.md](targets.md)).           |
| **Charm**          | A named modifier of an Operation's argv ([charms.md](charms.md)).                                     |
| **ExecResult**     | The result of one process: `{stdout, stderr, code, ok}`.                                              |
| **target.result**  | The dispatcher-emitted report event for one target run: project, target, status, cache hit, duration. |

## See also

- [spells.md](spells.md): anatomy of a spell, the two op shapes, naming operations.
- [targets.md](targets.md): the work-unit Target, `Path + Name`, the seven lifecycle names.
- [charms.md](charms.md): how a charm patches an Operation's argv.

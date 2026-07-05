---
title: Wards
description: Wards are coded guardrails that inspect a resolved op before it runs and reject an argv that contradicts the op's declared kind, so a misconfigured service or command op fails fast with a fix.
tags: [wards, diagnostics, operations, services, kind, MGSxxxx, guardrails]
---

# Wards

A **ward** is a guardrail magus runs against a *resolved op* - after a target's
operation is fully assembled but before it executes. The ward inspects the op's
argv and rejects it when the command contradicts the op's declared *kind*, so a
misconfigured op fails immediately with a coded, actionable diagnostic instead of
misbehaving at run time.

Wards share the `MGSxxxx` diagnostic rail with the rest of magus's [diagnostics and
error codes](codes/services/README.md): each ward raises a typed error with a stable
code, a plain-language explanation, and a suggested fix.

## Kind coherence

magus ops carry a *kind* - a **service** op is a long-running process magus
supervises in the foreground; a **command** op runs to completion. A ward fires when
the argv lies about that kind:

- **A service op that detaches** ([MGS5002](codes/services/MGS5002.md)) - `docker run -d`,
  a `--detach` flag, and friends fork the process away from magus, so foreground
  supervision, readiness, and stop all stop working. Drop the detach flag, or make it
  a command op if detaching is what you want.
- **A command op that never exits** ([MGS5003](codes/services/MGS5003.md)) - a watcher
  like `tsc --watch` in a run-to-completion op hangs the run. Make it a service op, or
  drop the watch flag.

Both are mirror images of the same bug: the argv and the kind disagree. Catching it
at resolution time turns a confusing hang or a lost log into a one-line fix.

See [Services](services.md#kind-coherence-wards) for the full rationale, and
[Operations](operations.md) for how op kinds fit the work hierarchy.

## Where wards fit

Wards are one family in magus's diagnostics. The complete catalog, grouped by area,
lives under the diagnostic codes:

- [magusfile](codes/magusfile/README.md) - authoring and configuration problems.
- [race](codes/race/README.md) - concurrency and ordering hazards.
- [sandbox](codes/sandbox/README.md) - filesystem and exec isolation violations.
- [services](codes/services/README.md) - service-op problems, including the kind-coherence wards above.

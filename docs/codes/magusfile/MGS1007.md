---
title: "MGS1007: target dependency cycle"
description: Fires when target dependencies form a cycle, so a target would have to run before itself. Detected across projects during cross-project dispatch.
tags: [MGS1007, magusfile, targets, needs, depends, cycle, cross-project]
---

# MGS1007: target dependency cycle

A target's dependencies form a cycle: following them leads back to the target
itself, so it would have to finish before it could start. Magus detects this
and errors instead of deadlocking.

```text
[MGS1007] cross-project cycle: ../api target "build"
  see: .../MGS1007.md
```

## Why

Targets declare what they depend on (`magus.needs(...)`, cross-project handles).
Magus runs a target's dependencies before the target. If A depends on B and B
depends on A - directly, or through a longer chain A -> B -> C -> A - there is no
valid order: each is waiting on the other. Left unchecked that is a deadlock, so
magus fails fast with this diagnostic naming the target the cycle closes on.

The check fires during cross-project dispatch: a `(project, target)` pair that is
already on the current call stack when it is requested again is a cycle.

## Resolution

Break the loop by removing one edge:

- **Find the back-edge.** Follow the `needs`/cross-project dependencies from the
  named target until you return to it. One of those edges is the one to cut.
- **Invert or extract.** If two targets genuinely need each other's output,
  factor the shared work into a third target both depend on, so the graph stays
  acyclic (A -> C, B -> C instead of A <-> B).
- **Cross-project cycles** usually mean two projects each build-depend on the
  other. Decide which is the leaf and make the dependency one-directional.

Run `magus describe graph` (or open the Graph Explorer) to see the dependency
edges and spot the cycle.

## What this is NOT

- **Not a data race.** A cycle is a structural dependency loop, not concurrent
  writes; those are the MGS4001-4004 race diagnostics.
- **Not caused by concurrency settings.** Lowering concurrency will not resolve
  a cycle - the dependency graph itself has no valid order.

## See also

- [targets.md](../../targets.md): how targets declare dependencies.
- `magus describe graph`: the dependency edges, to locate the cycle.

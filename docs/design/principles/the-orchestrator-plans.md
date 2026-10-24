---
title: The orchestrator plans, workers work, the integrator lands
order: 11
description: Delegated work has three roles; the orchestrator plans and coordinates, workers read, edit and check, and the integrator builds, regenerates and commits.
tags: [design, principle, agents, jobs]
---

# The orchestrator plans, workers work, the integrator lands

Delegated work has three roles, and each keeps to its own:

- **The orchestrator** (the root) plans and coordinates. It forks job rows,
  reads the graph, and decides what lands. It does no groundwork.
- **A worker** does the work its row hands it: it reads and searches source,
  edits inside its write paths, and runs its one check, at any context size.
- **The integrator** owns the landing checkout. Spawned
  `<parent>/integrate <job>` in that checkout, it cherry-picks finished work,
  builds magus, regenerates outputs and commits. The root coordinates and never
  builds.

Size and cost never decide whether a session delegates; they only tune the
advice given to a session that has handed nothing out. magus knows a session
orchestrates from what it recorded, a job row it forked or a spawn bound to
one; without that record the guard advises and never refuses.

This prevents the root whose context fills with work a worker would have
returned as one finding, and which then pays for that work on every call after
it, and the root that becomes the build bottleneck for every worker waiting on
it.

## Enforced by

- The guard's `orchestrator-groundwork` rule refuses a proven orchestrator's
  searches, source reads, diffs, runs, fetches and edits, and passes its job
  verbs, graph reads and integration.
- A spawn's description names its row and role (`<parent>/<role> <job>`), so the
  guard binds each worker or integrator to its lease.
- A person at the keyboard is the [human in the loop](human-in-the-loop.md),
  not an orchestrator, and is never judged by it.

## Good

The root forks a row with a narrow write set and a check, then spawns a worker
named for it:

```sh
magus job fork design-index --model economy \
  --criteria "design.md lists every design page" \
  --write-paths 'docs/design.md' \
  --check "conventions docs"
```

## Bad

```sh
grep -rn "doctrine" .
sed -n 1,400p docs/doctrine.md
```

The root reads the tree itself. Every line it pulled in rides along on each
later call, and a worker would have returned the same answer as one paragraph.

## See also

- [Human in the loop](human-in-the-loop.md)
- [Jobs](../../guides/jobs.md)
- [Orchestration](../../guides/integrations/agents/orchestration.md)

---
title: Glossary
description: The core magus vocabulary - workspace, project, magusfile, target, spell, operation, charm, ward, module, and engine - each defined in one line with a pointer to the page that covers it in full.
tags: [glossary, reference, terminology, concepts]
---

# Glossary

The vocabulary that runs through the rest of the docs. Each entry is a short
definition; follow the link for the page that covers the term in depth. Every
term has its own anchor, so you can deep-link a single definition (for example
`glossary/#output-reference`).

## Core model

### Workspace

The magus root directory that owns a set of projects and shared config; the unit
magus operates over. See [workspace.md](workspace.md).

### Project

A directory magus recognizes as a unit of work (it has a magusfile); the unit of
caching, scheduling, and dependency tracking. See [workspace.md](workspace.md).

### Magusfile

The `magusfile.buzz` that declares a project's targets (as `export fun`s) and
binds its spells. See [targets.md](targets.md).

### Target

A named operation (`build`, `test`, ...) you invoke with `magus run <target>`; it
may compose a spell's tool-native operations and depend on other targets. See
[targets.md](targets.md).

### Operation

A single tool-native command a target composes; the middle of the work hierarchy
(Spell to Operation to Target). See [operations.md](operations.md).

### Spell

A language/runtime adapter (e.g. `go`, `md`) that maps generic targets onto a
toolchain's real commands. See [spells.md](spells.md).

### Charm

An execution modifier attached with `:` (`lint:rw`) that changes _how_ a target
runs, not _which_ one; the built-in `rw` flips a check-only target to mutate in
place, and `ci` always strips it. See [charms.md](charms.md).

### Ward

A coded diagnostic that inspects a resolved op and nudges or blocks an
anti-pattern before it runs. See [wards.md](wards.md).

### Module

A magus stdlib namespace a magusfile imports for host capabilities: filesystem,
exec, vcs, and more. See [the module reference](buzz/modules/index.md).

### Buzz

The language magusfiles are written in (the `.buzz` engine). See
[engines.md](engines.md).

### Engine

The interpreter a magusfile runs on; magus embeds the Buzz engine. See
[engines.md](engines.md).

## Execution and caching

### Cache

The content-addressed store magus consults before running a target, so unchanged
work is skipped. See [cache.md](cache.md).

### Affected

The set of projects touched by a change; `magus affected <target>` runs a target
only over them. See [affected.md](affected.md).

### Sandbox

The restricted filesystem and environment a target runs in, so builds stay
reproducible and side-effect-free. See [sandbox.md](sandbox.md).

### Service

A long-running or shared process magus manages across runs, distinct from a
one-shot target. See [services.md](services.md).

### Daemon

The background magus host that owns shared state such as services and the warm
knowledge graph. See [daemon.md](daemon.md).

### CI

The composite pipeline (lint, build, test, coverage); run with
`magus affected ci`, handled internally by `Magus.RunCI`.

### Output reference

A short, shareable id (`ref1a2b3c`, "ref" for short) for one target execution's
captured output; it appears on each target's line, and
`magus query output ref1a2b3c` prints those exact bytes. In OpenTelemetry terms
it corresponds to a **span** (one target execution) within its **trace** (the
whole `magus` invocation). See [output-refs.md](output-refs.md).

### Trace

OpenTelemetry's name for one whole `magus` invocation; every target it runs is a
span beneath it. See [telemetry.md](telemetry.md).

### Span

OpenTelemetry's name for one unit of work under a trace - a target execution,
whose sub-operations are child spans. An output reference points at a span's
captured output. See [telemetry.md](telemetry.md).

### Pool

The concurrency pool: the shared set of slots that caps how many targets run in
parallel on one machine. Its capacity defaults to `MAGUS_CONCURRENCY`, then 4 on
GitHub-hosted runners, then `min(NumCPU, 8)`; `magus status` and the
[dashboard](daemon.md) report it live. See [daemon.md](daemon.md).

### Slot

One unit of the pool's capacity. A target acquires the slots it needs to run
(most take one) and releases them when it finishes; the pool tracks capacity
(total slots), running (acquired), and queued (blocked). See
[daemon.md](daemon.md).

### Concurrency

How many targets run at once. It is bounded by the pool's capacity and set with
`--concurrency`, `MAGUS_CONCURRENCY`, or the `concurrency` config key. See
[daemon.md](daemon.md).

### Queued

A target that wants a slot while the pool is full; it blocks first-in-first-out
until a slot frees. The dashboard colors a sample with queued > 0 accordingly.
See [daemon.md](daemon.md).

### Pool mode

Which pool a run uses: **daemon** (one shared pool the background daemon owns
across every workspace and client) or **proc** (a per-process pool for a single
one-off invocation). See [daemon.md](daemon.md).

### One-off

A single `magus` invocation that runs a target and exits, using a per-process
pool; the opposite of the long-lived daemon or a service. See
[daemon.md](daemon.md).

### Remote cache

A CI-only backend that shares content-addressed artifacts across runners: a cold
machine replays a build another runner already did instead of rebuilding. Every
remote artifact must be signed by a trusted key. See
[remote-cache.md](remote-cache.md).

### Snapshot

A point-in-time view of live state - the pool's occupancy or a tick of exported
metrics - as opposed to accumulated history. See [daemon.md](daemon.md).

### Backfill

The recent history the daemon replays to a dashboard on connect, so its charts
start populated instead of empty. It is served from a bounded ring buffer of the
last few hundred samples. See [daemon.md](daemon.md).

## Telemetry and health

### Latency

How long an operation takes. magus records latency as OpenTelemetry histograms
per family - target execution, cache op, pool wait, and graph query - and reports
each as a count, sum, and percentiles. See [telemetry.md](telemetry.md).

### Percentile

A latency value at a given rank, interpolated from a histogram's buckets: **p50**
is the median, **p95** and **p99** are the tail that most latency budgets care
about. See [telemetry.md](telemetry.md).

### Health

The at-a-glance daemon state derived from the pool: **healthy** when the pool is
reporting, **degraded** when it reports an error, **down** when there is no pool.
The dashboard color-codes each state. See [daemon.md](daemon.md).

### Flake

A target that fails once and passes on rerun (also **flaky**), as opposed to a
regression that started failing and stays failing. magus keeps per-target
pass/fail history and a Wilson-score flake rate to tell them apart and auto-retry
the noise. See [flaky-builds.md](flaky-builds.md).

## Insight and knowledge

### Knowledge graph

The queryable graph of a workspace's spells, targets, docs, and code
relationships; query it with `magus query`/`explain`/`path`. See
[knowledge.md](knowledge.md).

### Insight

The reports magus derives over the graph and history (hotspots, affinity,
ownership, trend). See [insight.md](insight.md).

### Hotspot

An insight lens: edit frequency times complexity, the prime refactoring targets.
The project view heat-colours the dependency graph by churn; `--files` ranks
individual files. See [insight.md](insight.md).

### Affinity

An insight lens: projects that change together (temporal coupling). A pair that
co-changes without either declaring a dependency on the other is a candidate
architectural smell. See [insight.md](insight.md).

### Ownership

An insight lens: author concentration - the primary author and their share, the
distinct-author count (the bus factor), and abandonment. See
[insight.md](insight.md).

### Trend

An insight lens: the recent half of the window against the earlier half. A
positive delta is a rising hotspot; a negative one is cooling. See
[insight.md](insight.md).

### Diagnostic code

A stable `MGSxxxx` identifier attached to a magus warning or error, so it can be
referenced and looked up; some are guardrails (see [wards](wards.md)), others hard
errors.

## See also

- [Documentation conventions](conventions.md) - how to read the placeholders,
  shell commands, and admonitions used across these pages.
- [Targets](targets.md) - the fuller Target-struct glossary (Path, Name, Files)
  for magusfile authors.
</content>
</invoke>

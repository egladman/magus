---
title: Glossary
description: The core magus vocabulary - workspace, project, magusfile, target, spell, operation, charm, ward, module, engine, and the console's own terms - each defined in one line with a pointer to the page that covers it in full.
tags: [glossary, reference, terminology, concepts, console]
---

# Glossary

The vocabulary that runs through the rest of the docs. Each entry is a short
definition; follow the link for the page that covers the term in depth. Every
term has its own anchor, so you can deep-link a single definition (for example
`glossary/#output-reference`).

## Core model

### Workspace

The magus root directory that owns a set of projects and shared config; the unit
magus operates over. See [workspace.md](concepts/workspace.md).

### Project

A directory magus recognizes as a unit of work (it has a magusfile); the unit of
caching, scheduling, and dependency tracking. See [workspace.md](concepts/workspace.md).

### Magusfile

The `magusfile.buzz` that declares a project's targets (as `export fun`s) and
binds its spells. See [targets.md](concepts/targets.md).

### Target

A named operation (`build`, `test`, ...) you invoke with `magus run <target>`; it
may compose a spell's tool-native operations and depend on other targets. See
[targets.md](concepts/targets.md).

### Operation

A single tool-native command a target composes; the middle of the work hierarchy
(Spell to Operation to Target). See [operations.md](concepts/operations.md).

### Spell

A language/runtime adapter (e.g. `go`, `md`) that maps generic targets onto a
toolchain's real commands. See [spells.md](concepts/spells.md).

### Charm

An execution modifier attached with `:` (`lint:rw`) that changes _how_ a target
runs, not _which_ one; the built-in `rw` flips a check-only target to mutate in
place, and `ci` always strips it. See [charms.md](concepts/charms.md).

### Ward

A coded diagnostic that inspects a resolved op and nudges or blocks an
anti-pattern before it runs. See [wards.md](concepts/wards.md).

### Module

A magus stdlib namespace a magusfile imports for host capabilities: filesystem,
exec, vcs, and more. See [the module reference](reference/buzz/index.md).

### Buzz

The language magusfiles are written in (the `.buzz` engine). See
[engines.md](concepts/engines.md).

### Engine

The interpreter a magusfile runs on; magus embeds the Buzz engine. See
[engines.md](concepts/engines.md).

## Execution and caching

### Cache

The content-addressed store magus consults before running a target, so unchanged
work is skipped. See [cache.md](concepts/cache.md).

### Affected

The set of projects touched by a change; `magus affected <target>` runs a target
only over them. See [affected.md](guides/affected.md).

### Sandbox

The restricted filesystem and environment a target runs in, so builds stay
reproducible and side-effect-free. See [sandbox.md](concepts/sandbox.md).

### Service

A long-running or shared process magus manages across runs, distinct from a
one-shot target. See [services.md](concepts/services.md).

### Daemon

The background magus host that owns shared state such as services and the warm
knowledge graph. See [daemon.md](guides/daemon.md).

### CI

An ordinary magusfile-defined target you compose yourself with `magus.needs` -
magus does not hardcode its stages. `Magus.RunCI` treats it specially only in
that it strips the `rw` charm, it is the anchor `magus affected ci` keys off,
and a selected scope with no project declaring it is a load error rather than
a silent no-op. See [targets.md](concepts/targets.md#the-target-name).

### Output reference

A short, shareable id (`ref1a2b3c`, "ref" for short) for one target execution's
captured output; it appears on each target's line, and
`magus query output ref1a2b3c` prints those exact bytes. In OpenTelemetry terms
it corresponds to a **span** (one target execution) within its **trace** (the
whole `magus` invocation). See [output-refs.md](concepts/output-refs.md).

### Trace

OpenTelemetry's name for one whole `magus` invocation; every target it runs is a
span beneath it. See [telemetry.md](concepts/telemetry.md).

### Span

OpenTelemetry's name for one unit of work under a trace - a target execution,
whose sub-operations are child spans. An output reference points at a span's
captured output. See [telemetry.md](concepts/telemetry.md).

### Pool

The concurrency pool: the shared set of slots that caps how many targets run in
parallel on one machine. Its capacity defaults to `MAGUS_CONCURRENCY`, then 4 on
GitHub-hosted runners, then `min(NumCPU, 8)`; `magus status` and the
[dashboard](guides/daemon.md) report it live. See [daemon.md](guides/daemon.md).

### Slot

One unit of the pool's capacity. A target acquires the slots it needs to run
(most take one) and releases them when it finishes; the pool tracks capacity
(total slots), running (acquired), and queued (blocked). See
[daemon.md](guides/daemon.md).

### Concurrency

How many targets run at once. It is bounded by the pool's capacity and set with
`--concurrency`, `MAGUS_CONCURRENCY`, or the `concurrency` config key. See
[daemon.md](guides/daemon.md).

### Queued

A target that wants a slot while the pool is full; it blocks first-in-first-out
until a slot frees. The dashboard colors a sample with queued > 0 accordingly.
See [daemon.md](guides/daemon.md).

### Pool mode

Which pool a run uses: **daemon** (one shared pool the background daemon owns
across every workspace and client) or **proc** (a per-process pool for a single
one-off invocation). See [daemon.md](guides/daemon.md).

### One-off

A single `magus` invocation that runs a target and exits, using a per-process
pool; the opposite of the long-lived daemon or a service. See
[daemon.md](guides/daemon.md).

### Remote cache

A CI-only backend that shares content-addressed artifacts across runners: a cold
machine replays a build another runner already did instead of rebuilding. Every
remote artifact must be signed by a trusted key. See
[remote-cache.md](concepts/remote-cache.md).

### Snapshot

A point-in-time view of live state - the pool's occupancy or a tick of exported
metrics - as opposed to accumulated history. See [daemon.md](guides/daemon.md).

### Backfill

The recent history the daemon replays to a dashboard on connect, so its charts
start populated instead of empty. It is served from a bounded ring buffer of the
last few hundred samples. See [daemon.md](guides/daemon.md).

## Telemetry and health

### Latency

How long an operation takes. magus records latency as OpenTelemetry histograms
per family - target execution, cache op, pool wait, and graph query - and reports
each as a count, sum, and percentiles. See [telemetry.md](concepts/telemetry.md).

### Percentile

A latency value at a given rank, interpolated from a histogram's buckets: **p50**
is the median, **p95** and **p99** are the tail that most latency budgets care
about. See [telemetry.md](concepts/telemetry.md).

### Health

The at-a-glance daemon state derived from the pool: **healthy** when the pool is
reporting, **degraded** when it reports an error, **down** when there is no pool.
The dashboard color-codes each state. See [daemon.md](guides/daemon.md).

### Volatility

A target that fails once and passes on rerun is **volatile**, as opposed to a
regression that started failing and stays failing. magus keeps per-target
pass/fail history and a Wilson-score volatility rate to tell them apart and
auto-retry the noise. See [volatility.md](concepts/volatility.md).

## Insight and knowledge

### Knowledge graph

The queryable graph of a workspace's spells, targets, docs, and code
relationships; query it with `magus query`/`explain`/`path`. See
[knowledge.md](concepts/knowledge.md).

### MAGUS.md

The committed routing index at a workspace root, regenerated from the knowledge
graph: it lists every node and points at the exact query for a given question,
so it is the entry point an agent reads first. See
[knowledge.md](concepts/knowledge.md).

### Insight

The reports magus derives over the graph and history (hotspots, affinity,
ownership, trend). See [insight.md](guides/insight.md).

### Hotspot

An insight lens: edit frequency times complexity, the prime refactoring targets.
The project view heat-colours the dependency graph by churn; `--files` ranks
individual files. See [insight.md](guides/insight.md).

### Affinity

An insight lens: projects that change together (temporal coupling). A pair that
co-changes without either declaring a dependency on the other is a candidate
architectural smell. See [insight.md](guides/insight.md).

### Ownership

An insight lens: author concentration - the primary author and their share, the
distinct-author count (the bus factor), and abandonment. See
[insight.md](guides/insight.md).

### Trend

An insight lens: the recent half of the window against the earlier half. A
positive delta is a rising hotspot; a negative one is cooling. See
[insight.md](guides/insight.md).

### Diagnostic code

A stable `MGSxxxx` identifier attached to a magus warning or error, so it can be
referenced and looked up; some are guardrails (see [wards](concepts/wards.md)), others hard
errors.

## Console

The vocabulary of the browser app. These terms name things you only meet in the
console's UI, so they are defined here rather than left to be inferred from it.

### Console

The browser app that reads a magus workspace: a tabbed, tiling page hosting the
log viewer, graph explorer, dashboard, and activity trail. It is a separate
static app, not something the daemon serves - the daemon exposes a loopback API
it calls: read-only views plus one bearer-gated job-control service for
maintenance jobs. See [reference/console.md](reference/console.md).

### Surface

One of the console's apps (Log Viewer, Graph Explorer, Dashboard, Activity
Trail, Settings). "Surface" rather than "page" because one is never a document
you navigate to: it is mounted into a tab, or into a pane beside another one.
Each is single-instance - opening one you already have focuses it instead of
duplicating it. See [reference/console.md](reference/console.md).

### Pane

A split within a tab. Splitting divides the focused pane along its longer side,
so the same action tiles side-by-side on a desktop and stacks on a phone; a tab
with no split is a single pane. Drag the divider to re-weight the split. See
[reference/console.md](reference/console.md).

### Chord

A key combination bound to a console command, written `mod+k` - where `mod` is
Cmd on macOS and Ctrl elsewhere, so one binding fits both. Every chord is
rebindable (Settings > Keybindings), and a command remains reachable from the
command bar whether or not it has one. See [reference/console.md](reference/console.md).

### Command bar

The console's runner: one searchable list of every command and its chord,
opened with `mod+k`. It is the discoverable route to any action - the menus and
chords dispatch the same commands it does. See [reference/console.md](reference/console.md).

### Live link

The URL that points a surface at a running daemon. The daemon serves the console
from its own loopback origin, so the link is that origin plus the surface path and
a bearer token in the fragment (`http://127.0.0.1:7391/console/graph/#token=...`).
The daemon prints it; the console consumes the token, stores it, and strips it from
the URL, so the secret never lingers in history or a copied link. The origin must be
literal loopback - `localhost` and hostnames are rejected before any request.
Without one, a surface
reads only what rides in the link itself. See [reference/console.md](reference/console.md).

## See also

- [Documentation conventions](conventions.md) - how to read the placeholders,
  shell commands, and admonitions used across these pages.
- [Targets](concepts/targets.md) - the fuller Target-struct glossary (Path, Name, Files)
for magusfile authors.
</content>

</invoke>

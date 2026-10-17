---
title: Telemetry
description: Export magus metrics and traces to any OTLP collector you run, with head-based sampling, resource attributes, and full instrument reference.
tags:
  [
    telemetry,
    opentelemetry,
    otlp,
    observability,
    metrics,
    traces,
    logs,
    monitoring,
  ]
---

# Telemetry

magus can export **metrics** and **traces** to any OTLP collector you run.
Telemetry is **OFF by default**: there is no magus-operated backend. The
collector is yours, and magus connects only to the endpoint you configure.

Instrument definitions live in
[`internal/observability/otlp/provider.go`](../../internal/observability/otlp/provider.go)
and
[`internal/observability/otlp/families.go`](../../internal/observability/otlp/families.go);
the interface every producer records through is
[`internal/observability/provider.go`](../../internal/observability/provider.go),
and config is in [`internal/config/config.go`](../../internal/config/config.go).

## Enabling

In `magus.yaml`:

```yaml
telemetry:
  enabled: true
  endpoint: "localhost:4318" # host:port, no scheme
  protocol: "http" # "grpc" (default) or "http"
  insecure: true # plaintext for local collectors
  service_name: "magus" # resource attribute service.name
  sample_ratio: 1.0 # head-based trace sampling, [0,1]
  headers: # static headers on every OTLP request
    x-api-key: "..."
```

Or via the environment:

| Env var                        | YAML key                 | Default | Purpose                                                        |
| ------------------------------ | ------------------------ | ------- | -------------------------------------------------------------- |
| `MAGUS_TELEMETRY_ENABLED`      | `telemetry.enabled`      | `false` | Turn OTLP export on; magus connects to the endpoint when true  |
| `MAGUS_TELEMETRY_ENDPOINT`     | `telemetry.endpoint`     | -       | OTLP collector address as `host:port` (no scheme); required    |
| `MAGUS_TELEMETRY_PROTOCOL`     | `telemetry.protocol`     | `grpc`  | OTLP wire protocol: `grpc` or `http`                           |
| `MAGUS_TELEMETRY_INSECURE`     | `telemetry.insecure`     | `false` | Disable TLS for the OTLP exporter (plaintext local collectors) |
| `MAGUS_TELEMETRY_SERVICE_NAME` | `telemetry.service_name` | `magus` | Value of the resource attribute `service.name`                 |
| `MAGUS_TELEMETRY_SAMPLE_RATIO` | `telemetry.sample_ratio` | `1.0`   | Head-based trace sampling ratio in `[0,1]`                     |

**Export details.** magus pushes metrics over OTLP on a periodic reader every
**30s**. It batches traces and samples them head-based by `sample_ratio`
(`1.0` = every trace). Metrics are unsampled: every recorded point is exported.

## Resource attributes

Every metric and span carries these resource attributes:

| Attribute              | Source                                        |
| ---------------------- | --------------------------------------------- |
| `service.name`         | `telemetry.service_name` (default `magus`)    |
| `service.version`      | the magus build version                       |
| `magus.workspace.root` | the workspace root, when set                  |
| `process.*`            | detected process metadata (pid, runtime, ...) |
| `host.*`               | detected host metadata                        |

## Metrics

### Cache (local)

Low-cardinality aggregate view of the on-disk content-addressed cache; no
per-project attribute.

| Metric                       | Instrument | Unit     | Attributes             | Meaning                                                                     |
| ---------------------------- | ---------- | -------- | ---------------------- | --------------------------------------------------------------------------- |
| `magus.cache.reads`          | counter    | `{call}` | `result`               | One per `Cache.Run`, by how it ended                                        |
| `magus.cache.duration`       | histogram  | `s`      | `result ∈ {hit, miss}` | Wall-clock of a single `Cache.Run`                                          |
| `magus.cache.saved.duration` | histogram  | `s`      | -                      | What a hit **avoided**: the duration the entry recorded when it was written |

`result ∈ {hit, miss, error}`: `hit` is a replay from disk, `miss` a genuine build
(no entry found) that succeeded, and `error` a step that failed or was refused
before it started. A failed step records no duration.

`magus.cache.duration` says what a hit **cost**; `magus.cache.saved.duration` says
what it **saved**, one observation per hit. Its sum is the wall-clock the cache
has given back, and its distribution says which hits are worth having. An entry
written before the manifest carried a duration reports nothing rather than zero,
so the total understates and never overstates: the same measured-not-modeled
rule the `cache_saved_ms` field in `magus status` follows.

### Remote cache

The [shared backend](cache/remote.md) (S3, GitHub Actions, ...), exported only
when one is wired via `magus\cache.remote(...)`. These live under a `.remote`
prefix so a remote hit is **never** folded into `magus.cache.reads`: a remote hit
is still a _local_ miss, because the remote fetch only runs after the local store
misses. Instrumentation wraps the backend interface, so it applies to every
backend (including ones you write) with no backend changes.

| Metric                        | Instrument | Unit     | Attributes                       | Meaning                                                                 |
| ----------------------------- | ---------- | -------- | -------------------------------- | ----------------------------------------------------------------------- |
| `magus.cache.remote.ops`      | counter    | `{call}` | `method ∈ {get, put}`, `outcome` | One per remote operation, by what the backend answered                  |
| `magus.cache.remote.duration` | histogram  | `s`      | `method ∈ {get, put}`, `outcome` | Wall-clock of a single remote operation                                 |
| `magus.cache.remote.io.size`  | histogram  | `By`     | `method ∈ {get, put}`            | Bytes transferred, recorded on hits and puts only (egress/ingress cost) |

`outcome ∈ {hit, miss, stored, exists, error}`; `stored` is a successful `put`, and
`exists` a `put` the store answered with "already stored" (another writer holds the
key). A `get` answers `hit`, `miss` or `error`; a `put` answers `stored`, `exists` or
`error`. **Remote hit-rate** is `outcome=hit / (outcome=hit + outcome=miss)` over
`method=get`, and **put success** is `stored + exists` over `method=put`.

These metrics meter the **transport**: what the backend answered. The end-of-run
`remote:` line (the `run.remote` record) meters **outcomes**, so the two differ by
design. An artifact the backend returned but that failed verification or replay is a
`hit` here and a failure there; an `exists` put is neither an upload nor a failure
there; and a run the remote tier already failed stops calling the backend, so later
lookups appear in neither.

### Graph

| Metric                       | Instrument | Unit | Attributes       | Meaning                            |
| ---------------------------- | ---------- | ---- | ---------------- | ---------------------------------- |
| `magus.graph.query.duration` | histogram  | `s`  | `op`, `strategy` | Wall-clock of a single graph query |

`strategy` is present only when the query reports one. `op=build` is the full
graph build. The histogram's count is the number of queries.

### Target runs

Per-project, per-spell. `magus.project` has unbounded cardinality; see
[Cardinality](#cardinality).

| Metric                  | Instrument | Unit | Attributes                                                             | Meaning                                                          |
| ----------------------- | ---------- | ---- | ---------------------------------------------------------------------- | ---------------------------------------------------------------- |
| `magus.target.duration` | histogram  | `s`  | `magus.project`, `magus.spell`, `magus.target`, `outcome`, `cache.hit` | Wall-clock of a single target execution, including cache replays |

`outcome ∈ {ok, error}` · `cache.hit ∈ {true, false}` · one row is emitted per
resolved spell per project. The histogram's count is the number of target runs.

### Concurrency pool

| Metric                     | Instrument      | Unit     | Attributes | Meaning                                          |
| -------------------------- | --------------- | -------- | ---------- | ------------------------------------------------ |
| `magus.pool.wait.duration` | histogram       | `s`      | -          | Time a target waited for a slot                  |
| `magus.pool.slots.running` | up-down counter | `{slot}` | -          | Concurrency slots currently running (gauge-like) |
| `magus.pool.slots.queued`  | up-down counter | `{slot}` | -          | Callers currently queued for a slot (gauge-like) |

`magus.pool.slots.running` is an up-down counter: it rises as targets acquire
slots and falls as they release, so its value reads as the live running depth.

### Agents and review

Leases, attention requests and paired review: the three places a fleet of
agents and the people working with them meet. **Every producer here runs in the
daemon**, which is what makes them collectable at all: a magus CLI invocation is
a one-shot process, and the CLI halves of these same features (raising an
attention request, the agent guard grading a write) reach the
[activity trail](../guides/integrations/server.md) instead. Read each row for
what it counts, not for all three.

| Metric                                       | Instrument | Unit             | Attributes                              | Meaning                                             |
| -------------------------------------------- | ---------- | ---------------- | --------------------------------------- | --------------------------------------------------- |
| `magus.lease.registrations`                  | counter    | `{registration}` | `verdict`                               | A worker registered the base it actually landed on  |
| `magus.attention.disposition.duration`       | histogram  | `s`              | `severity`                              | How long a request waited, from raised to disposed  |
| `magus.review.remarks`                       | counter    | `{remark}`       | `author ∈ {unattributed, agent}`        | A remark drafted on a change                        |
| `magus.review.publishes`                     | counter    | `{publish}`      | `verdict`, `downgraded ∈ {true, false}` | A review published, by the verdict that landed      |
| `magus.review.merged_while_reading.duration` | histogram  | `s`              | none                                    | Time from the reader's mark to the merge            |
| `magus.agent.context.tokens`                 | histogram  | `{token}`        | `thread`, `model`                       | Context size a caller reported on a tool call       |
| `magus.agent.delegable.chars`                | counter    | `{char}`         | `class`                                 | Result characters a subagent could have fetched     |
| `magus.agent.billing`                        | counter    | `{call}`         | `mode`                                  | Tool calls, by the billing mode in effect           |
| `magus.agent.shadow.verdicts`                | counter    | `{call}`         | `profile`, `decision`                   | Tool calls, by what each guard profile would decide |

`verdict` on registrations is `match`, `revision-match`, `diverged` or `unknown`;
a rising `diverged` share says a fleet's workers are building on trees their
orchestrator never handed them. `verdict` on publishes is `comment`, `approve` or
`request_changes`, and it is the verdict that **landed**: `downgraded=true` means
the asserting verdict the person asked for was refused (a review cannot approve a
change its own credential opened), which is invisible in the forge's record and
known only here.

`magus.attention.disposition.duration` counts the disposals made through the
console route. `magus session dispose` closes a request from a one-shot process
with no collector, so those are absent: the instrument is a wait-time
distribution rather than a queue depth, which would read as the whole queue and
be neither. `severity` is re-read from the store and clamped to the declared
tiers (`info`, `notice`, `warning`, `critical`, plus `unset`), so a record written
by another build can never mint a series.

The `magus.agent.*` metrics are fed from the tool calls of an agent session.
`thread` is `main`, `sub` or `unknown` (a host that reports nothing to tell
them apart), and `model` falls back to `unknown`. `class` names the kind of call
a subagent could have made in the caller's place, and the counter adds the
characters its result put into the caller's context. `mode` is the billing mode
in effect. `profile` is a guard profile and `decision` is what that profile
would have decided; the row records the verdict without enforcing it.

**Metrics observe, they never gate.** `magus.review.remarks` is attributed by
`author` because that says which **door** a remark came through, and both doors
write to one store. `magus.review.publishes` is deliberately **not**: how a change
was judged, split by who wrote it, is the first half of a threshold that blocks
work by author kind, and magus does not build the instrument that invites one.

`magus.review.merged_while_reading.duration` measures review time lost to a merge;
its count is the merges. It is recorded by the `check-review` job, so a merge is
counted on the first tick after it lands, and only when the reader had marked
**that** review as being read (the `reading` op on the console's review route). The
mark is local and cleared once reported, so a merge is counted once. It carries no
attribute: a review id or repository name is unbounded, and the duration is the
whole fact. Like every metric here it reaches a backend only when telemetry
export is on.

## Traces (spans)

Spans are sampled head-based by `telemetry.sample_ratio`.

| Span                       | Attributes                      | When                                                        |
| -------------------------- | ------------------------------- | ----------------------------------------------------------- |
| `magus.target.run`         | `magus.project`, `magus.target` | One per target execution (miss path)                        |
| `magus.cache.hash`         | -                               | Hashing a target's inputs (every `Cache.Run`)               |
| `magus.cache.replay`       | -                               | Restoring outputs from a cache hit                          |
| `magus.cache.snapshot`     | -                               | Capturing outputs after a build (miss path, writable cache) |
| `magus.cache.remote.get`   | `magus.project`                 | A remote fetch; spans the network round-trip and the import |
| `magus.cache.remote.put`   | `magus.project`                 | A remote upload                                             |
| `magus.cache.remote.prune` | -                               | A retention sweep (`magus config cache prune --remote`)     |

The `magus.cache.hash` / `replay` / `snapshot` spans break a target's latency
down by phase: hashing vs. building vs. I/O. The remote `get`/`put` spans put a
network fetch or upload inline in the build trace with its own latency, so a slow
remote round-trip is visible instead of opaque time inside the target.

## Cardinality

`magus.target.duration` includes `magus.project`, which is unbounded in large
monorepos. If your setup has thousands of projects, drop or relabel the
attribute at the collector:

```yaml
# OpenTelemetry Collector - attributes processor
processors:
  attributes/drop_project:
    actions:
      - key: magus.project
        action: delete
```

Alternatively, use an SDK View at startup to drop the attribute before it leaves
the process. Four other instruments carry a workspace-sized attribute and the
same recipe applies: `magus.sandbox.checks` and `magus.sandbox.env.dropped`
carry `magus.project`, and `magus.buzz.spell.resolve.duration` and
`magus.buzz.spell.builtins.warm` carry `spell`, a workspace-authored name.
Every other metric omits them. The remote
`get`/`put` **spans** carry `magus.project`, but spans are sampled and not
aggregated into time series, so they don't create cardinality the way a metric
attribute would.

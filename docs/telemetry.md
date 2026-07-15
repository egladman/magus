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

# Telemetry (OpenTelemetry)

magus can export **metrics** and **traces** to any OTLP collector you run.
Telemetry is **OFF by default**: there is no magus-operated backend. The
collector is yours, and magus connects only to the endpoint you configure.

This page is the complete reference for everything magus emits. Instrument
definitions live in
[`internal/observability/provider_otel.go`](../internal/observability/provider_otel.go);
config in [`internal/config/config.go`](../internal/config/config.go).

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

| Metric                 | Instrument | Unit     | Attributes              | Meaning                                        |
| ---------------------- | ---------- | -------- | ----------------------- | ---------------------------------------------- |
| `magus.cache.hits`     | counter    | `{call}` | `outcome=hit`           | Cache replays (a `Cache.Run` served from disk) |
| `magus.cache.misses`   | counter    | `{call}` | `outcome=miss`          | Genuine builds (no entry found)                |
| `magus.cache.errors`   | counter    | `{call}` | `outcome=error`         | Build steps that failed                        |
| `magus.cache.duration` | histogram  | `s`      | `outcome ∈ {hit, miss}` | Wall-clock of a single `Cache.Run`             |

### Remote cache

The [shared backend](remote-cache.md) (S3, GitHub Actions, ...), exported only
when one is wired via `magus.cache.remote(...)`. These mirror the local-cache
vocabulary under a `.remote` prefix so a remote hit is **never** folded into the
local counters: a remote hit is still a _local_ miss, because the remote fetch
only runs after the local store misses. Instrumentation wraps the backend
interface, so it applies to every backend (including ones you write) with no
backend changes.

| Metric                        | Instrument | Unit     | Attributes                         | Meaning                                                                 |
| ----------------------------- | ---------- | -------- | ---------------------------------- | ----------------------------------------------------------------------- |
| `magus.cache.remote.hits`     | counter    | `{call}` | `op=get`, `outcome=hit`            | Remote `get` returned an entry                                          |
| `magus.cache.remote.misses`   | counter    | `{call}` | `op=get`, `outcome=miss`           | Remote `get` found nothing                                              |
| `magus.cache.remote.errors`   | counter    | `{call}` | `op ∈ {get, put}`, `outcome=error` | A remote operation failed                                               |
| `magus.cache.remote.duration` | histogram  | `s`      | `op ∈ {get, put}`, `outcome`       | Wall-clock of a single remote operation                                 |
| `magus.cache.remote.io.size`  | histogram  | `By`     | `op ∈ {get, put}`                  | Bytes transferred, recorded on hits and puts only (egress/ingress cost) |

`outcome ∈ {hit, miss, stored, error}`; `stored` is a successful `put`.
**Remote hit-rate** is `hits / (hits + misses)`. **Put success** is the
`magus.cache.remote.duration` count for `op=put` minus
`magus.cache.remote.errors` for `op=put`.

### Graph

| Metric                       | Instrument | Unit     | Attributes       | Meaning                            |
| ---------------------------- | ---------- | -------- | ---------------- | ---------------------------------- |
| `magus.graph.queries`        | counter    | `{call}` | `op`, `strategy` | Number of graph query operations   |
| `magus.graph.query.duration` | histogram  | `s`      | `op`, `strategy` | Wall-clock of a single graph query |

`strategy` is present only when the query reports one. `op=build` is the full
graph build.

### Target runs

Per-project, per-spell. `magus.project` has unbounded cardinality; see
[Cardinality](#cardinality).

| Metric                  | Instrument | Unit     | Attributes                                                             | Meaning                                    |
| ----------------------- | ---------- | -------- | ---------------------------------------------------------------------- | ------------------------------------------ |
| `magus.target.runs`     | counter    | `{call}` | `magus.project`, `magus.spell`, `magus.target`, `outcome`, `cache.hit` | Target executions, including cache replays |
| `magus.target.duration` | histogram  | `s`      | same                                                                   | Wall-clock of a single target execution    |

`outcome ∈ {success, error}` · `cache.hit ∈ {true, false}` · one row is emitted
per resolved spell per project.

### Concurrency pool

| Metric                     | Instrument      | Unit     | Attributes | Meaning                                          |
| -------------------------- | --------------- | -------- | ---------- | ------------------------------------------------ |
| `magus.pool.wait.duration` | histogram       | `s`      | -          | Time a target waited for a slot                  |
| `magus.pool.slots.running` | up-down counter | `{slot}` | -          | Concurrency slots currently running (gauge-like) |
| `magus.pool.slots.queued`  | up-down counter | `{slot}` | -          | Callers currently queued for a slot (gauge-like) |

`magus.pool.slots.running` is an up-down counter: it rises as targets acquire
slots and falls as they release, so its value reads as the live running depth.

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

`magus.target.runs` and `magus.target.duration` include `magus.project`, which is
unbounded in large monorepos. If your setup has thousands of projects, drop or
relabel the attribute at the collector:

```yaml
# OpenTelemetry Collector - attributes processor
processors:
  attributes/drop_project:
    actions:
      - key: magus.project
        action: delete
```

Alternatively, use an SDK View at startup to drop the attribute before it leaves
the process. Every other metric intentionally omits `magus.project`. The remote
`get`/`put` **spans** carry `magus.project`, but spans are sampled and not
aggregated into time series, so they don't create cardinality the way a metric
attribute would.

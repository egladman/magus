---
title: MetricsService
generated_from: reference/api/
description: MetricsService serves the derived dashboard metrics.
tags: [api, proto, connect, grpc, metricsservice]
---

# MetricsService

MetricsService serves the derived dashboard metrics. Served over ConnectRPC, so one endpoint speaks Connect (browser-native HTTP), gRPC, and gRPC-Web from this one contract.

Package `magus.metrics.v1alpha1`, defined in `proto/magus/metrics/v1alpha1/metrics.proto`. Source: [metrics.proto:17](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L17). Part of the [daemon API](../../index.md).

## Methods

### GetMetrics

GetMetrics returns the current derived snapshot.

`POST /magus.metrics.v1alpha1.MetricsService/GetMetrics`: unary. Source: [metrics.proto:19](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L19).

Takes [GetMetricsRequest](#getmetricsrequest), returns [Snapshot](#snapshot).

### StreamMetrics

StreamMetrics pushes the rolling history first (one Backfill), then a fresh Snapshot on each tick, so the dashboard's charts and utilization grid start populated and stay live.

`POST /magus.metrics.v1alpha1.MetricsService/StreamMetrics`: server streaming. Source: [metrics.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L22).

Takes [StreamMetricsRequest](#streammetricsrequest), returns [StreamMetricsResponse](#streammetricsresponse).

## Messages

### Backfill

Backfill is the ring-buffer history the daemon sends once, right after a dashboard connects, so the utilization grid and cache-rate trend start populated instead of empty.

Source: [metrics.proto:155](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L155).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `samples` | [repeated Sample](#sample) | 1 | oldest-first |

Used by: [StreamMetrics (response)](metrics.md#streammetrics).

### Buzz

Buzz rolls up the magus.buzz.* families: script exec/compile latency, the native-boundary host-call family, session-pool health, import and spell resolution, and VM-level counters.

Source: [metrics.proto:114](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L114).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `exec_count` | int64 | 1 |  |
| `exec_p50_seconds` | double | 2 |  |
| `exec_p95_seconds` | double | 3 |  |
| `compile_count` | int64 | 4 |  |
| `compile_p50_seconds` | double | 5 |  |
| `compile_p95_seconds` | double | 6 |  |
| `host_call_count` | int64 | 7 |  |
| `host_call_p50_seconds` | double | 8 |  |
| `host_call_p95_seconds` | double | 9 |  |
| `session_pool_reuse` | int64 | 10 | acquires served from an idle session |
| `session_pool_idle` | int64 | 11 | current idle sessions (gauge) |
| `session_pool_evictions` | int64 | 12 |  |
| `session_warm_p50_seconds` | double | 13 |  |
| `session_warm_p95_seconds` | double | 14 |  |
| `import_count` | int64 | 15 |  |
| `import_p50_seconds` | double | 16 |  |
| `import_p95_seconds` | double | 17 |  |
| `spell_resolve_count` | int64 | 18 |  |
| `spell_resolve_p50_seconds` | double | 19 |  |
| `spell_resolve_p95_seconds` | double | 20 |  |
| `jit_runs` | int64 | 21 |  |
| `vm_faults` | int64 | 22 |  |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### GetMetricsRequest

Source: [metrics.proto:25](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L25).

No fields.

Used by: [GetMetrics (request)](metrics.md#getmetrics).

### Latency

Latency is an operation-family rollup: how many happened and how long they took. The percentiles are interpolated from the OTel histogram's buckets server-side, so the dashboard never re-derives them from raw buckets. Every quantity carries its unit in its NAME (AIP-141): a bare p95 cannot tell a reader whether it is seconds or milliseconds, and the dashboard does arithmetic on these.

Source: [metrics.proto:59](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L59).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `count` | int64 | 1 | number of observations (the operation count) |
| `p50_seconds` | double | 2 |  |
| `p95_seconds` | double | 3 |  |
| `p99_seconds` | double | 4 |  |
| `max_seconds` | double | 5 | upper bound of the largest populated bucket |
| `sum_seconds` | double | 6 | total observed, for averages / throughput |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### MCPToolStat

MCPToolStat is one per-tool rollup of the magus.mcp.tool.* families: call/error tallies, input/output payload sizes, and call duration percentiles.

Source: [metrics.proto:98](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L98).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `tool` | string | 1 |  |
| `calls` | int64 | 2 |  |
| `errors` | int64 | 3 |  |
| `input_p50_bytes` | double | 4 |  |
| `input_p95_bytes` | double | 5 |  |
| `input_total` | int64 | 6 | total input bytes observed |
| `output_p50_bytes` | double | 7 |  |
| `output_p95_bytes` | double | 8 |  |
| `output_total` | int64 | 9 | total output bytes observed |
| `duration_p50_seconds` | double | 10 |  |
| `duration_p95_seconds` | double | 11 |  |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### Remote

Remote is the remote-cache instrument family: outcome tallies plus transfer latency and volume.

Source: [metrics.proto:70](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L70).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `hits` | int64 | 1 |  |
| `misses` | int64 | 2 |  |
| `errors` | int64 | 3 |  |
| `duration_p50_seconds` | double | 4 |  |
| `duration_p95_seconds` | double | 5 |  |
| `io_count` | int64 | 6 | number of get/put operations observed |
| `transferred_bytes` | int64 | 7 | total bytes transferred (sum of the io.size histogram) |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### Sample

Sample is one point in the rolling utilization/activity history. The daemon appends one per tick; the dashboard diffs adjacent samples for per-interval rates and colors one grid square per sample by utilization.

Every field carries EXPLICIT PRESENCE, and that is the whole point of the message: a tick whose pool read or metric collection failed must record that it did not measure, never a zero. Zero is a measurement. An unset counter in a CUMULATIVE series is the dangerous case - a reader diffing adjacent samples sees one large negative step (indistinguishable from a counter reset) followed by one enormous positive step, so a single failed read corrupts the rate on both sides of it. capacity makes the same point in miniature: 0 already means "unlimited", so a zero written for "we could not read the pool" is not merely imprecise, it asserts the opposite of what happened.

This is the rule STALENESS\_UNMEASURED, VERDICT\_UNKNOWN and ANCHOR\_STATUS\_UNVERIFIED state in the sibling contracts: an unmeasured value must never render as a measured one. A client MUST render an unset field as a break in the series, never as zero and never by carrying the previous value forward.

Source: [metrics.proto:176](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L176).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `sample_time` | Timestamp | 1 |  |
| `running` | int32 | 2 | _optional_ pool slots running at this tick; unset = pool unreadable |
| `capacity` | int32 | 3 | _optional_ pool capacity (0 = unlimited); unset = pool unreadable |
| `queued` | int32 | 4 | _optional_ tasks queued for a slot; unset = pool unreadable |
| `cache_hits` | int64 | 5 | _optional_ cumulative; diff adjacent samples for a hit rate |
| `cache_misses` | int64 | 6 | _optional_ cumulative |
| `target_runs` | int64 | 7 | _optional_ cumulative target executions |
| `observe_start_time` | Timestamp | 8 | _optional_ observe\_start\_time identifies the GENERATION the cumulative counters above belong to: the instant the daemon began observing. The counters restart at zero when the daemon does, so two samples from different generations cannot be differenced - their difference is not a rate, it is the new process's total minus the old one's.  Carrying the identity is what lets a consumer BREAK the series at a restart instead of guessing. Do not infer a restart by comparing values: a decrease is also what an unreadable collection looks like, and clamping the negative to zero (which is what a consumer naturally reaches for) reports a restart as "no activity this minute" - a wrong answer that looks like a real one. |

Used by: [StreamMetrics (response)](metrics.md#streammetrics).

### Sandbox

Sandbox rolls up the magus.sandbox.* filesystem families: apply latency, the rule counts a sandbox was built from, allow/deny check tallies, and dropped environment variables.

Source: [metrics.proto:141](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L141).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `apply_p50_seconds` | double | 1 |  |
| `apply_p95_seconds` | double | 2 |  |
| `rules_read` | int64 | 3 |  |
| `rules_write` | int64 | 4 |  |
| `rules_exec` | int64 | 5 |  |
| `env_rules` | int64 | 6 | exact + glob env rules |
| `checks_allow` | int64 | 7 |  |
| `checks_deny` | int64 | 8 |  |
| `env_dropped` | int64 | 9 |  |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### Snapshot

Snapshot is the derived-metrics view at one instant: each OTel instrument family aggregated for the dashboard.

Source: [metrics.proto:38](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L38).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `capture_time` | Timestamp | 1 |  |
| `target` | [Latency](#latency) | 2 | magus.target.duration + magus.target.runs |
| `cache` | [Latency](#latency) | 3 | cache is the local Cache.Run family (magus.cache.duration + magus.cache.{hits,misses,errors}). Named "cache" (not "cache\_op") because "op" collides with the Operation glossary term and this family measures a Cache.Run, not a resolved op. |
| `pool_wait` | [Latency](#latency) | 4 | magus.pool.wait.duration |
| `graph_query` | [Latency](#latency) | 5 | magus.graph.query.duration + magus.graph.queries |
| `remote` | [Remote](#remote) | 6 | magus.cache.remote.* |
| `target_stats` | [repeated TargetStat](#targetstat) | 7 | per-target rollup of magus.target.{duration,runs} |
| `mcp_tools` | [repeated MCPToolStat](#mcptoolstat) | 8 | per-tool rollup of magus.mcp.tool.* |
| `buzz` | [Buzz](#buzz) | 9 | magus.buzz.* families |
| `sandbox` | [Sandbox](#sandbox) | 10 | magus.sandbox.* filesystem families |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### StreamMetricsRequest

Source: [metrics.proto:26](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L26).

No fields.

Used by: [StreamMetrics (request)](metrics.md#streammetrics).

### StreamMetricsResponse

Source: [metrics.proto:27](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L27).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `backfill` | [Backfill](#backfill) | 1 | _one of `of`_ |
| `snapshot` | [Snapshot](#snapshot) | 2 | _one of `of`_ |

Used by: [StreamMetrics (response)](metrics.md#streammetrics).

### TargetStat

TargetStat is one per-target rollup: how often a (project, target, spell) ran, its latency percentiles, cache hit-rate, and success/error split. Grouped from the magus.target.duration histogram's per-(project,spell,target,outcome,cache.hit) data points.

Source: [metrics.proto:83](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1alpha1/metrics.proto#L83).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `project` | string | 1 | magus.project attribute |
| `target` | string | 2 | magus.target attribute |
| `spell` | string | 3 | magus.spell attribute ("" when the project declares none) |
| `count` | int64 | 4 | total executions (including cache replays) |
| `p50_seconds` | double | 5 |  |
| `p95_seconds` | double | 6 |  |
| `p99_seconds` | double | 7 |  |
| `cache_hit_rate` | double | 8 | [0,1]; fraction of runs served from cache |
| `success` | int64 | 9 | runs with outcome=success |
| `errors` | int64 | 10 | runs with outcome=error |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).


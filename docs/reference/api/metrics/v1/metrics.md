---
title: MetricsService
generated_from: reference/api/
description: MetricsService serves the derived dashboard metrics.
tags: [api, proto, connect, grpc, metricsservice]
---

# MetricsService

MetricsService serves the derived dashboard metrics. Served over ConnectRPC, so one endpoint speaks Connect (browser-native HTTP), gRPC, and gRPC-Web from this one contract.

Package `magus.metrics.v1`, defined in `proto/magus/metrics/v1/metrics.proto`. Source: [metrics.proto:17](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L17). Part of the [daemon API](../../index.md).

## Methods

### GetMetrics

GetMetrics returns the current derived snapshot.

`POST /magus.metrics.v1.MetricsService/GetMetrics`: unary. Source: [metrics.proto:19](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L19).

Takes [GetMetricsRequest](#getmetricsrequest), returns [GetMetricsResponse](#getmetricsresponse).

### StreamMetrics

StreamMetrics pushes the rolling history first (one Backfill), then a fresh Snapshot on each tick, so the dashboard's charts and utilization grid start populated and stay live.

`POST /magus.metrics.v1.MetricsService/StreamMetrics`: server streaming. Source: [metrics.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L22).

Takes [StreamMetricsRequest](#streammetricsrequest), returns [StreamMetricsResponse](#streammetricsresponse).

## Messages

### Backfill

Backfill is the ring-buffer history the daemon sends once, right after a dashboard connects, so the utilization grid and cache-rate trend start populated instead of empty.

Source: [metrics.proto:157](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L157).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `samples` | [repeated Sample](#sample) | 1 | oldest-first |

Used by: [StreamMetrics (response)](metrics.md#streammetrics).

### Buzz

Buzz rolls up the magus.buzz.* families: script exec/compile latency, the native-boundary host-call family, session-pool health, import and spell resolution, and VM-level counters.

Source: [metrics.proto:116](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L116).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `exec_count` | int64 | 1 |  |
| `exec_p50` | double | 2 | seconds |
| `exec_p95` | double | 3 | seconds |
| `compile_count` | int64 | 4 |  |
| `compile_p50` | double | 5 | seconds |
| `compile_p95` | double | 6 | seconds |
| `host_call_count` | int64 | 7 |  |
| `host_call_p50` | double | 8 | seconds |
| `host_call_p95` | double | 9 | seconds |
| `session_pool_reuse` | int64 | 10 | acquires served from an idle session |
| `session_pool_idle` | int64 | 11 | current idle sessions (gauge) |
| `session_pool_evictions` | int64 | 12 |  |
| `session_warm_p50` | double | 13 | seconds |
| `session_warm_p95` | double | 14 | seconds |
| `import_count` | int64 | 15 |  |
| `import_p50` | double | 16 | seconds |
| `import_p95` | double | 17 | seconds |
| `spell_resolve_count` | int64 | 18 |  |
| `spell_resolve_p50` | double | 19 | seconds |
| `spell_resolve_p95` | double | 20 | seconds |
| `jit_runs` | int64 | 21 |  |
| `vm_faults` | int64 | 22 |  |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### GetMetricsRequest

Source: [metrics.proto:25](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L25).

No fields.

Used by: [GetMetrics (request)](metrics.md#getmetrics).

### GetMetricsResponse

Source: [metrics.proto:26](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L26).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `snapshot` | [Snapshot](#snapshot) | 1 |  |

Used by: [GetMetrics (response)](metrics.md#getmetrics).

### Latency

Latency is an operation-family rollup: how many happened and how long they took. The percentiles are interpolated from the OTel histogram's buckets server-side, so the dashboard never re-derives them from raw buckets.

Source: [metrics.proto:61](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L61).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `count` | int64 | 1 | number of observations (the operation count) |
| `p50` | double | 2 | seconds |
| `p95` | double | 3 | seconds |
| `p99` | double | 4 | seconds |
| `max` | double | 5 | seconds (upper bound of the largest populated bucket) |
| `sum` | double | 6 | total seconds observed (for averages / throughput) |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### MCPToolStat

MCPToolStat is one per-tool rollup of the magus.mcp.tool.* families: call/error tallies, input/output payload sizes, and call duration percentiles.

Source: [metrics.proto:100](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L100).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `tool` | string | 1 |  |
| `calls` | int64 | 2 |  |
| `errors` | int64 | 3 |  |
| `input_p50` | double | 4 | bytes |
| `input_p95` | double | 5 | bytes |
| `input_total` | int64 | 6 | total input bytes observed |
| `output_p50` | double | 7 | bytes |
| `output_p95` | double | 8 | bytes |
| `output_total` | int64 | 9 | total output bytes observed |
| `duration_p50` | double | 10 | seconds |
| `duration_p95` | double | 11 | seconds |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### Remote

Remote is the remote-cache instrument family: outcome tallies plus transfer latency and volume.

Source: [metrics.proto:72](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L72).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `hits` | int64 | 1 |  |
| `misses` | int64 | 2 |  |
| `errors` | int64 | 3 |  |
| `duration_p50` | double | 4 | seconds |
| `duration_p95` | double | 5 | seconds |
| `io_count` | int64 | 6 | number of get/put operations observed |
| `bytes_total` | int64 | 7 | total bytes transferred (sum of the io.size histogram) |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).

### Sample

Sample is one point in the rolling utilization/activity history. The daemon appends one per tick; the dashboard diffs adjacent samples for per-interval rates and colors one grid square per sample by utilization.

Source: [metrics.proto:164](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L164).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `at` | Timestamp | 1 |  |
| `running` | int32 | 2 | pool slots running at this tick |
| `capacity` | int32 | 3 | pool capacity (0 = unlimited) |
| `queued` | int32 | 4 | tasks queued for a slot |
| `cache_hits` | int64 | 5 | cumulative; diff adjacent samples for a hit rate |
| `cache_misses` | int64 | 6 | cumulative |
| `target_runs` | int64 | 7 | cumulative target executions |

Used by: [StreamMetrics (response)](metrics.md#streammetrics).

### Sandbox

Sandbox rolls up the magus.sandbox.* filesystem families: apply latency, the rule counts a sandbox was built from, allow/deny check tallies, and dropped environment variables.

Source: [metrics.proto:143](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L143).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `apply_p50` | double | 1 | seconds |
| `apply_p95` | double | 2 | seconds |
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

Source: [metrics.proto:42](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L42).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `captured_at` | Timestamp | 1 |  |
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

Source: [metrics.proto:30](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L30).

No fields.

Used by: [StreamMetrics (request)](metrics.md#streammetrics).

### StreamMetricsResponse

Source: [metrics.proto:31](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L31).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `backfill` | [Backfill](#backfill) | 1 | _one of `of`_ |
| `snapshot` | [Snapshot](#snapshot) | 2 | _one of `of`_ |

Used by: [StreamMetrics (response)](metrics.md#streammetrics).

### TargetStat

TargetStat is one per-target rollup: how often a (project, target, spell) ran, its latency percentiles, cache hit-rate, and success/error split. Grouped from the magus.target.duration histogram's per-(project,spell,target,outcome,cache.hit) data points.

Source: [metrics.proto:85](https://github.com/egladman/magus/blob/main/proto/magus/metrics/v1/metrics.proto#L85).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `project` | string | 1 | magus.project attribute |
| `target` | string | 2 | magus.target attribute |
| `spell` | string | 3 | magus.spell attribute ("" when the project declares none) |
| `count` | int64 | 4 | total executions (including cache replays) |
| `p50` | double | 5 | seconds |
| `p95` | double | 6 | seconds |
| `p99` | double | 7 | seconds |
| `cache_hit_rate` | double | 8 | [0,1]; fraction of runs served from cache |
| `success` | int64 | 9 | runs with outcome=success |
| `errors` | int64 | 10 | runs with outcome=error |

Used by: [GetMetrics (response)](metrics.md#getmetrics), [StreamMetrics (response)](metrics.md#streammetrics).


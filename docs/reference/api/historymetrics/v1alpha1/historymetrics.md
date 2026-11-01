---
title: HistoryMetricsService
generated_from: reference/api/
description: HistoryMetricsService serves the assembled history metrics to the console.
tags: [api, proto, connect, grpc, historymetricsservice]
---

# HistoryMetricsService

HistoryMetricsService serves the assembled history metrics to the console.

Package `magus.historymetrics.v1alpha1`, defined in `proto/magus/historymetrics/v1alpha1/historymetrics.proto`. Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto). Part of the [daemon API](../../index.md).

## Methods

### GetHistoryMetrics

GetHistoryMetrics returns every metric field in one response.

`POST /magus.historymetrics.v1alpha1.HistoryMetricsService/GetHistoryMetrics`: unary. Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

Takes [GetHistoryMetricsRequest](#gethistorymetricsrequest), returns [GetHistoryMetricsResponse](#gethistorymetricsresponse).

## Messages

### Affinity

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                           | # | Description |
| ------------ | ------------------------------ | - | ----------- |
| `definition` | string                         | 1 |             |
| `commits`    | int32                          | 2 |             |
| `since`      | string                         | 3 |             |
| `pairs`      | [repeated CoChange](#cochange) | 4 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### CoChange

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field    | Type   | # | Description |
| -------- | ------ | - | ----------- |
| `a`      | string | 1 |             |
| `a_name` | string | 2 |             |
| `b`      | string | 3 |             |
| `b_name` | string | 4 |             |
| `count`  | int32  | 5 |             |
| `hidden` | bool   | 6 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### Conformance

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                                           | # | Description |
| ------------ | ---------------------------------------------- | - | ----------- |
| `definition` | string                                         | 1 |             |
| `rows`       | [repeated ConformanceRow](#conformancerow)     | 2 |             |
| `excluded`   | int32                                          | 4 |             |
| `answer`     | [Answer](../../graph/v1alpha1/graph.md#answer) | 5 |             |

_Reserved: 3; `stale`._

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### ConformanceItem

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field     | Type   | # | Description |
| --------- | ------ | - | ----------- |
| `id`      | string | 1 |             |
| `source`  | string | 2 |             |
| `measure` | int32  | 3 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### ConformanceKey

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type   | # | Description |
| ------------ | ------ | - | ----------- |
| `metric`     | string | 1 |             |
| `population` | string | 2 |             |
| `scope`      | string | 3 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### ConformanceRow

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                                         | #  | Description |
| ------------ | -------------------------------------------- | -- | ----------- |
| `key`        | [ConformanceKey](#conformancekey)            | 1  |             |
| `n`          | int32                                        | 2  |             |
| `p50`        | int32                                        | 3  |             |
| `p90`        | int32                                        | 4  |             |
| `p95`        | int32                                        | 5  |             |
| `p99`        | int32                                        | 6  |             |
| `live`       | int32                                        | 7  |             |
| `value`      | int32                                        | 8  |             |
| `propose`    | int32                                        | 10 |             |
| `violations` | [repeated ConformanceItem](#conformanceitem) | 13 |             |

_Reserved: 9, 11, 12; `anchor`, `known`, `source`._

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### FileHotspot

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field              | Type      | # | Description |
| ------------------ | --------- | - | ----------- |
| `path`             | string    | 1 |             |
| `commits`          | int32     | 2 |             |
| `complexity`       | int32     | 3 |             |
| `score`            | int32     | 4 |             |
| `authors`          | int32     | 5 |             |
| `last_commit_time` | Timestamp | 6 |             |
| `moves`            | int32     | 7 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### GetHistoryMetricsRequest

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

No fields.

Used by: [GetHistoryMetrics (request)](historymetrics.md#gethistorymetrics).

### GetHistoryMetricsResponse

GetHistoryMetricsResponse is the RPC envelope only: each field is a domain metric. volatility / model\_tiers / conformance stay absent (not empty) when their source was never recorded or the server held no workspace.

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field         | Type                        | # | Description |
| ------------- | --------------------------- | - | ----------- |
| `hotspots`    | [Hotspots](#hotspots)       | 1 |             |
| `affinity`    | [Affinity](#affinity)       | 2 |             |
| `ownership`   | [Ownership](#ownership)     | 3 |             |
| `trend`       | [Trend](#trend)             | 4 |             |
| `volatility`  | [Volatility](#volatility)   | 5 |             |
| `model_tiers` | [ModelTiers](#modeltiers)   | 6 |             |
| `conformance` | [Conformance](#conformance) | 7 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### Hotspots

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                                 | # | Description |
| ------------ | ------------------------------------ | - | ----------- |
| `definition` | string                               | 1 |             |
| `commits`    | int32                                | 2 |             |
| `since`      | string                               | 3 |             |
| `nodes`      | [repeated ProjectNode](#projectnode) | 4 |             |
| `files`      | [repeated FileHotspot](#filehotspot) | 5 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### ModelTierRecord

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field               | Type   | #  | Description |
| ------------------- | ------ | -- | ----------- |
| `rank`              | int32  | 1  |             |
| `model_tier`        | string | 2  |             |
| `kind`              | string | 3  |             |
| `jobs`              | int32  | 4  |             |
| `pass`              | int32  | 5  |             |
| `fail`              | int32  | 6  |             |
| `no_return`         | int32  | 7  |             |
| `pass_lower`        | double | 8  |             |
| `checks_per_job`    | double | 9  |             |
| `forks_per_id`      | double | 10 |             |
| `rework_per_job`    | double | 11 |             |
| `widenings_per_job` | double | 12 |             |
| `mean_cost_usd`     | double | 13 | _optional_  |
| `median_tokens`     | int64  | 14 | _optional_  |
| `first_attempts`    | int32  | 15 |             |
| `first_pass`        | int32  | 16 |             |
| `first_pass_lower`  | double | 17 |             |
| `not_retryable`     | int32  | 18 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### ModelTiers

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                                         | # | Description |
| ------------ | -------------------------------------------- | - | ----------- |
| `definition` | string                                       | 1 |             |
| `rows`       | [repeated ModelTierRecord](#modeltierrecord) | 2 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### Ownership

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                                       | # | Description |
| ------------ | ------------------------------------------ | - | ----------- |
| `definition` | string                                     | 1 |             |
| `commits`    | int32                                      | 2 |             |
| `since`      | string                                     | 3 |             |
| `projects`   | [repeated OwnershipEntry](#ownershipentry) | 4 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### OwnershipEntry

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field              | Type      | # | Description |
| ------------------ | --------- | - | ----------- |
| `path`             | string    | 1 |             |
| `name`             | string    | 2 |             |
| `commits`          | int32     | 3 |             |
| `authors`          | int32     | 4 |             |
| `primary`          | string    | 5 |             |
| `primary_share`    | int32     | 6 |             |
| `bus_factor_1`     | bool      | 7 |             |
| `stale`            | bool      | 8 |             |
| `last_commit_time` | Timestamp | 9 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### ProjectNode

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field              | Type            | #  | Description |
| ------------------ | --------------- | -- | ----------- |
| `path`             | string          | 1  |             |
| `name`             | string          | 2  |             |
| `spell_name`       | string          | 3  |             |
| `children`         | repeated string | 4  |             |
| `dir`              | string          | 5  |             |
| `blast_radius`     | int32           | 7  |             |
| `duration_ms`      | int64           | 8  |             |
| `churn`            | int32           | 9  |             |
| `authors`          | int32           | 10 |             |
| `last_commit_time` | Timestamp       | 11 |             |

_Reserved: 6; `exclusive`._

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### Trend

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field        | Type                               | # | Description |
| ------------ | ---------------------------------- | - | ----------- |
| `definition` | string                             | 1 |             |
| `commits`    | int32                              | 2 |             |
| `since`      | string                             | 3 |             |
| `projects`   | [repeated TrendEntry](#trendentry) | 4 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### TrendEntry

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field     | Type   | # | Description |
| --------- | ------ | - | ----------- |
| `path`    | string | 1 |             |
| `name`    | string | 2 |             |
| `recent`  | int32  | 3 |             |
| `earlier` | int32  | 4 |             |
| `delta`   | int32  | 5 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### Volatility

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field       | Type                                           | # | Description |
| ----------- | ---------------------------------------------- | - | ----------- |
| `threshold` | double                                         | 1 |             |
| `targets`   | [repeated VolatilityTarget](#volatilitytarget) | 2 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).

### VolatilityTarget

Source: [historymetrics.proto](https://github.com/egladman/magus/blob/main/proto/magus/historymetrics/v1alpha1/historymetrics.proto).

| Field            | Type      | # | Description |
| ---------------- | --------- | - | ----------- |
| `project`        | string    | 1 |             |
| `target`         | string    | 2 |             |
| `score`          | double    | 3 |             |
| `volatile`       | bool      | 4 |             |
| `pass`           | int32     | 5 |             |
| `fail`           | int32     | 6 |             |
| `volatile_count` | int32     | 7 |             |
| `samples`        | int32     | 8 |             |
| `last_pass_time` | Timestamp | 9 |             |

Used by: [GetHistoryMetrics (response)](historymetrics.md#gethistorymetrics).


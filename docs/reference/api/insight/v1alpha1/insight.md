---
title: InsightService
generated_from: reference/api/
description: InsightService serves the assembled lenses to the console dashboard.
tags: [api, proto, connect, grpc, insightservice]
---

# InsightService

InsightService serves the assembled lenses to the console dashboard. One read-only unary RPC: the four git lenses come from a server-cached scan (~10s TTL) and volatility from a fresh file read, so the client polls rather than subscribes.

Package `magus.insight.v1alpha1`, defined in `proto/magus/insight/v1alpha1/insight.proto`. Source: [insight.proto:28](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L28). Part of the [daemon API](../../index.md).

## Methods

### GetInsight

GetInsight returns every lens in one message.

`POST /magus.insight.v1alpha1.InsightService/GetInsight`: unary. Source: [insight.proto:30](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L30).

Takes [GetInsightRequest](#getinsightrequest), returns [Insight](#insight).

## Messages

### AffinityOutput

AffinityOutput reports projects that change together (temporal coupling).

Source: [insight.proto:113](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L113).

| Field        | Type                           | # | Description |
| ------------ | ------------------------------ | - | ----------- |
| `definition` | string                         | 1 |             |
| `commits`    | int32                          | 2 |             |
| `since`      | string                         | 3 |             |
| `pairs`      | [repeated CoChange](#cochange) | 4 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### CoChange

CoChange is a pair of projects that changed in the same commit, how often, and whether the coupling is hidden - no dependency edge connects them, which is the candidate architectural smell the lens exists to surface. a/b are the stable project paths; a\_name/b\_name are the declared display names, carried alongside rather than resolved into the path so a reader can label the pair without a second lookup.

Source: [insight.proto:125](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L125).

| Field    | Type   | # | Description |
| -------- | ------ | - | ----------- |
| `a`      | string | 1 |             |
| `a_name` | string | 2 |             |
| `b`      | string | 3 |             |
| `b_name` | string | 4 |             |
| `count`  | int32  | 5 |             |
| `hidden` | bool   | 6 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ConformItem

ConformItem is one file, symbol, package or clone group past its row's value and not known. source is its path:line.

Source: [insight.proto:280](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L280).

| Field     | Type   | # | Description |
| --------- | ------ | - | ----------- |
| `id`      | string | 1 |             |
| `source`  | string | 2 |             |
| `measure` | int32  | 3 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ConformKey

ConformKey names one threshold: metric (file-lines, func-lines, type-methods, file-symbols, dep-fanout, clone-tokens or export-scope), population (source, main or test) and, for dep-fanout, the layer or package directory it covers.

Source: [insight.proto:251](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L251).

| Field        | Type   | # | Description |
| ------------ | ------ | - | ----------- |
| `metric`     | string | 1 |             |
| `population` | string | 2 |             |
| `scope`      | string | 3 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ConformKnown

ConformKnown is a list of items already past a metric's value and accepted for a reason. In ConformOutput.stale it names the entries that no longer violate and should be deleted.

Source: [insight.proto:288](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L288).

| Field    | Type            | # | Description |
| -------- | --------------- | - | ----------- |
| `metric` | string          | 1 |             |
| `items`  | repeated string | 2 |             |
| `reason` | string          | 3 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ConformOutput

ConformOutput is the code-shape lens: one row per metric and population, judged against the value a check enforces. It reads the symbol graph, not git. It reports and proposes; a person commits a proposed value to the ratchet record, and nothing here sets one. answer is unknown, never a pass, when a symbol index is missing. excluded counts the files the configured exclusions hid, so a widened exclusion stays visible.

Source: [insight.proto:240](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L240).

| Field        | Type                                           | # | Description |
| ------------ | ---------------------------------------------- | - | ----------- |
| `definition` | string                                         | 1 |             |
| `rows`       | [repeated ConformRow](#conformrow)             | 2 |             |
| `stale`      | [repeated ConformKnown](#conformknown)         | 3 |             |
| `excluded`   | int32                                          | 4 |             |
| `answer`     | [Answer](../../graph/v1alpha1/graph.md#answer) | 5 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ConformRow

ConformRow is one key's distribution and verdict. live is the configured percentile today; value and source (ratchet or anchor) are what a check enforces, and anchor is the shipped bound no committed value may exceed. propose is zero, or a tighter value to commit; it is never looser than value. known counts the accepted items past value, and violations are the rest.

Source: [insight.proto:262](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L262).

| Field        | Type                                 | #  | Description |
| ------------ | ------------------------------------ | -- | ----------- |
| `key`        | [ConformKey](#conformkey)            | 1  |             |
| `n`          | int32                                | 2  |             |
| `p50`        | int32                                | 3  |             |
| `p90`        | int32                                | 4  |             |
| `p95`        | int32                                | 5  |             |
| `p99`        | int32                                | 6  |             |
| `live`       | int32                                | 7  |             |
| `value`      | int32                                | 8  |             |
| `anchor`     | int32                                | 9  |             |
| `propose`    | int32                                | 10 |             |
| `known`      | int32                                | 11 |             |
| `source`     | string                               | 12 |             |
| `violations` | [repeated ConformItem](#conformitem) | 13 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### FileHotspot

FileHotspot is one file's hotspot score: edit frequency weighted by complexity. score is commits x complexity, sent rather than derived so a reader ranks by the same number the CLI printed even if the weighting changes.

Source: [insight.proto:97](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L97).

| Field              | Type      | # | Description                                                                                                                                                                                                                                                                                                                                                                 |
| ------------------ | --------- | - | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `path`             | string    | 1 |                                                                                                                                                                                                                                                                                                                                                                             |
| `commits`          | int32     | 2 |                                                                                                                                                                                                                                                                                                                                                                             |
| `complexity`       | int32     | 3 |                                                                                                                                                                                                                                                                                                                                                                             |
| `score`            | int32     | 4 |                                                                                                                                                                                                                                                                                                                                                                             |
| `authors`          | int32     | 5 |                                                                                                                                                                                                                                                                                                                                                                             |
| `last_commit_time` | Timestamp | 6 |                                                                                                                                                                                                                                                                                                                                                                             |
| `moves`            | int32     | 7 | How many times the file changed path inside the window. commits and moves are different kinds of churn - one is the contents being rewritten, the other is the file being moved around - and a reader wants both, because a file doing both at once is a stronger signal than either count alone. Not derivable from path, which carries only the name the file ends under. |

Used by: [GetInsight (response)](insight.md#getinsight).

### GetInsightRequest

Source: [insight.proto:33](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L33).

No fields.

Used by: [GetInsight (request)](insight.md#getinsight).

### HotspotOutput

HotspotOutput ranks where churn meets complexity - the canonical "fix this first" view. nodes is the project-level heatmap; files is the per-file ranking, populated only when the scan ran at file granularity.

Source: [insight.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L62).

| Field        | Type                                 | # | Description |
| ------------ | ------------------------------------ | - | ----------- |
| `definition` | string                               | 1 |             |
| `commits`    | int32                                | 2 |             |
| `since`      | string                               | 3 |             |
| `nodes`      | [repeated ProjectNode](#projectnode) | 4 |             |
| `files`      | [repeated FileHotspot](#filehotspot) | 5 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### Insight

Insight bundles the six lenses. volatility is absent (not an empty report) when the workspace has no run-outcome history to score - the distinction matters to a reader, which renders "no runs recorded yet" rather than "no volatile targets". model\_tiers is absent for the same reason when no job outcome was ever recorded. conform is absent when the server holds no workspace to read; a workspace without a symbol index sends it with an unknown answer.

Source: [insight.proto:41](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L41).

| Field         | Type                                  | # | Description |
| ------------- | ------------------------------------- | - | ----------- |
| `hotspots`    | [HotspotOutput](#hotspotoutput)       | 1 |             |
| `affinity`    | [AffinityOutput](#affinityoutput)     | 2 |             |
| `ownership`   | [OwnershipOutput](#ownershipoutput)   | 3 |             |
| `trend`       | [TrendOutput](#trendoutput)           | 4 |             |
| `volatility`  | [VolatilityReport](#volatilityreport) | 5 |             |
| `model_tiers` | [ModelTierOutput](#modeltieroutput)   | 6 |             |
| `conform`     | [ConformOutput](#conformoutput)       | 7 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ModelTierOutput

ModelTierOutput is the job-history lens: what each model tier achieved on each kind of job. It reads the outcomes the job store recorded, not git, and it reports only: changing a tier is a person editing the declaration or matrix the doctor check names.

Source: [insight.proto:207](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L207).

| Field        | Type                                         | # | Description |
| ------------ | -------------------------------------------- | - | ----------- |
| `definition` | string                                       | 1 |             |
| `rows`       | [repeated ModelTierRecord](#modeltierrecord) | 2 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ModelTierRecord

ModelTierRecord is the recorded outcome of the jobs that ran at one tier and job kind. rank is the tier's place in the current declaration, -1 for one it no longer names. pass\_lower is the Wilson lower bound of the pass rate; the per-job fields are means, so a reader sets pass rate beside the work a pass cost: checks run, re-forks of one id, rework rounds (a worker sent more work after handing back) and widenings (write paths added to a taken job).

Source: [insight.proto:218](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L218).

| Field               | Type   | #  | Description                           |
| ------------------- | ------ | -- | ------------------------------------- |
| `rank`              | int32  | 1  |                                       |
| `model_tier`        | string | 2  |                                       |
| `kind`              | string | 3  | check, symbol, paths, script or prose |
| `jobs`              | int32  | 4  |                                       |
| `pass`              | int32  | 5  |                                       |
| `fail`              | int32  | 6  |                                       |
| `no_return`         | int32  | 7  |                                       |
| `pass_lower`        | double | 8  |                                       |
| `checks_per_job`    | double | 9  |                                       |
| `forks_per_id`      | double | 10 |                                       |
| `rework_per_job`    | double | 11 |                                       |
| `widenings_per_job` | double | 12 |                                       |
| `mean_cost_usd`     | double | 13 |                                       |
| `median_tokens`     | int64  | 14 |                                       |

Used by: [GetInsight (response)](insight.md#getinsight).

### Ownership

Ownership is one project's authorship. bus\_factor\_1 and stale are the two risk flags the server decides (single author; no commits in the recent half of the window) - they ride the wire rather than being recomputed by a reader so the console and the CLI cannot disagree about what counts as abandoned.

Source: [insight.proto:146](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L146).

| Field              | Type      | # | Description                      |
| ------------------ | --------- | - | -------------------------------- |
| `path`             | string    | 1 |                                  |
| `name`             | string    | 2 |                                  |
| `commits`          | int32     | 3 |                                  |
| `authors`          | int32     | 4 |                                  |
| `primary`          | string    | 5 | the author with the most commits |
| `primary_share`    | int32     | 6 | that author's share, in percent  |
| `bus_factor_1`     | bool      | 7 |                                  |
| `stale`            | bool      | 8 |                                  |
| `last_commit_time` | Timestamp | 9 |                                  |

Used by: [GetInsight (response)](insight.md#getinsight).

### OwnershipOutput

OwnershipOutput reports author concentration per project - the knowledge-risk view.

Source: [insight.proto:135](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L135).

| Field        | Type                             | # | Description |
| ------------ | -------------------------------- | - | ----------- |
| `definition` | string                           | 1 |             |
| `commits`    | int32                            | 2 |             |
| `since`      | string                           | 3 |             |
| `projects`   | [repeated Ownership](#ownership) | 4 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### ProjectNode

ProjectNode is one project in the heatmap. It mirrors types.Node, the dependency-graph node the hotspots lens reuses, which is why it carries graph shape (children, spell\_name) alongside the churn fields: the same message serves a reader that wants to draw the dependency edges under the heat. It is NOT magus.graph.v1alpha1.Node: that one is a knowledge-graph node (id/kind/relation), this one is a project in the build graph.

churn, authors and last\_commit\_time are the heatmap overlay and are absent on a plain dependency graph; blast\_radius and duration\_ms come from the graph itself.

Source: [insight.proto:78](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L78).

| Field              | Type            | #  | Description                                                     |
| ------------------ | --------------- | -- | --------------------------------------------------------------- |
| `path`             | string          | 1  | the stable machine key                                          |
| `name`             | string          | 2  | the declared display name, empty when the project never set one |
| `spell_name`       | string          | 3  |                                                                 |
| `children`         | repeated string | 4  |                                                                 |
| `dir`              | string          | 5  |                                                                 |
| `blast_radius`     | int32           | 7  |                                                                 |
| `duration_ms`      | int64           | 8  |                                                                 |
| `churn`            | int32           | 9  | recent commits touching the project                             |
| `authors`          | int32           | 10 | distinct authors behind them                                    |
| `last_commit_time` | Timestamp       | 11 |                                                                 |

_Reserved: 6; `exclusive`._

Used by: [GetInsight (response)](insight.md#getinsight).

### Trend

Trend is one project's churn across the window's two halves. delta is recent - earlier, sent explicitly because it is the sort key and a reader should not have to know the sign convention (positive is rising).

Source: [insight.proto:170](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L170).

| Field     | Type   | # | Description |
| --------- | ------ | - | ----------- |
| `path`    | string | 1 |             |
| `name`    | string | 2 |             |
| `recent`  | int32  | 3 |             |
| `earlier` | int32  | 4 |             |
| `delta`   | int32  | 5 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### TrendOutput

TrendOutput ranks projects by whether their activity is rising or cooling: the window is split at its midpoint and the halves compared.

Source: [insight.proto:160](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L160).

| Field        | Type                     | # | Description |
| ------------ | ------------------------ | - | ----------- |
| `definition` | string                   | 1 |             |
| `commits`    | int32                    | 2 |             |
| `since`      | string                   | 3 |             |
| `projects`   | [repeated Trend](#trend) | 4 |             |

Used by: [GetInsight (response)](insight.md#getinsight).

### VolatilityReport

VolatilityReport is the run-outcome lens: the one lens that does not read git. It is computed from the shared runtime-history file, so it is present even in a workspace with no VCS history, and absent in one that has never recorded a run.

Source: [insight.proto:181](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L181).

| Field       | Type                                           | # | Description                                                                                                                                                      |
| ----------- | ---------------------------------------------- | - | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `threshold` | double                                         | 1 | The configured Wilson lower bound at or above which a target is treated as volatile. Sent so a reader renders the same threshold line the server scored against. |
| `targets`   | [repeated VolatilityTarget](#volatilitytarget) | 2 |                                                                                                                                                                  |

Used by: [GetInsight (response)](insight.md#getinsight).

### VolatilityTarget

VolatilityTarget is one (project, target) pair's recorded flakiness: the Wilson lower-bound score against the report's threshold, plus the tallies it was computed from. pass/fail/ volatile\_count count the retained window and samples is how many outcomes that window holds, so a reader can tell a genuinely stable target from one with two runs on record.

Source: [insight.proto:192](https://github.com/egladman/magus/blob/main/proto/magus/insight/v1alpha1/insight.proto#L192).

| Field            | Type      | # | Description                                   |
| ---------------- | --------- | - | --------------------------------------------- |
| `project`        | string    | 1 |                                               |
| `target`         | string    | 2 |                                               |
| `score`          | double    | 3 |                                               |
| `volatile`       | bool      | 4 | score >= the report's threshold               |
| `pass`           | int32     | 5 |                                               |
| `fail`           | int32     | 6 |                                               |
| `volatile_count` | int32     | 7 |                                               |
| `samples`        | int32     | 8 |                                               |
| `last_pass_time` | Timestamp | 9 | the most recent passing run, unset when never |

Used by: [GetInsight (response)](insight.md#getinsight).


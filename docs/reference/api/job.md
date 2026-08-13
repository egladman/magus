---
title: JobService
description: "JobService is the daemon's control surface for background maintenance jobs."
tags: [api, proto, connect, grpc, jobservice]
---

# JobService

JobService is the daemon's control surface for background maintenance jobs. Trigger RPCs submit a job and return immediately; ListJobs reports every job's state. Read surfaces stay on the per-domain services - this one only mutates.

Package `magus.job.v1`, defined in `proto/magus/job/v1/job.proto`. Part of the [daemon API](index.md).

## Methods

### SyncGraph

SyncGraph reconciles the knowledge graph to current source (rebuild and reindex).

`POST /magus.job.v1.JobService/SyncGraph`: unary.

Takes `SyncGraphRequest`, returns `SubmitJobResponse`.

### RotateActivities

RotateActivities trims the activity trail to its cap and drops orphaned payload blobs.

`POST /magus.job.v1.JobService/RotateActivities`: unary.

Takes `RotateActivitiesRequest`, returns `SubmitJobResponse`.

### ClearCache

ClearCache invalidates cached build entries for the workspace.

`POST /magus.job.v1.JobService/ClearCache`: unary.

Takes `ClearCacheRequest`, returns `SubmitJobResponse`.

### RotateLogs

RotateLogs trims the invocation run-log journals back to their cap.

`POST /magus.job.v1.JobService/RotateLogs`: unary.

Takes `RotateLogsRequest`, returns `SubmitJobResponse`.

### ListJobs

ListJobs returns every registered job with its running state, last run, and target size.

`POST /magus.job.v1.JobService/ListJobs`: unary.

Takes `ListJobsRequest`, returns `ListJobsResponse`.

## Messages

### ClearCacheRequest

No fields.

### JobInfo

JobInfo is the full picture of one job: what it is, whether an instance is running now, its most recent completed run, and the current magnitude of the resource it maintains.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | stable job name, e.g. "rotate-activities" |
| `description` | string | 2 |  |
| `running` | bool | 3 | an instance is in flight right now |
| `last_run` | JobRun | 4 | most recent completed run; unset if the job has not run this daemon session |
| `target` | ResourceSize | 5 | current size of what the job operates on (trail, cache, or logs) |

### JobRun

JobRun is one completed execution of a job.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation_id` | string | 1 |  |
| `finished_at` | Timestamp | 2 |  |
| `duration` | Duration | 3 |  |
| `ok` | bool | 4 | false when the run errored |
| `error` | string | 5 | error text when ok is false |
| `items_removed` | int64 | 6 | Per-run deltas, populated only by jobs that measure them (a rotate reports what it dropped). Zero when the job does not report a delta yet - additive, so a job starts reporting later without a contract change. e.g. trail events pruned, cache entries invalidated |
| `bytes_reclaimed` | int64 | 7 | on-disk bytes freed |

### ListJobsRequest

No fields.

### ListJobsResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `jobs` | repeated JobInfo | 1 | every registered job, in a stable order |

### ResourceSize

ResourceSize is the current magnitude of a job's target resource, for a caller to show how much there is to maintain (and to judge whether a rotate/clear is worth running).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `bytes` | int64 | 1 | total on-disk bytes |
| `count` | int64 | 2 | logical item count (trail events, cached entries, run logs) |

### RotateActivitiesRequest

No fields.

### RotateLogsRequest

No fields.

### SubmitJobResponse

SubmitJobResponse is returned by every trigger RPC: whether the job started or coalesced, the invocation id and console deep-link for its live log, and the job's full metadata snapshot so a caller can render "last rotated 3m ago, trail 2.1 MB" without a follow-up call.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `state` | SubmitState | 1 |  |
| `invocation_id` | string | 2 | the running job's invocation id (the new one, or the coalesced one) |
| `console_url` | string | 3 | deep-link to this invocation's live log; empty when no console is mounted |
| `job` | JobInfo | 4 | the job's descriptor plus its last-run and current-size metadata |

### SyncGraphRequest

No fields.

## Enums

### SubmitState

SubmitState is the disposition of a trigger RPC. Both values are SUCCESS outcomes returned in a normal response (not an error): coalescing an identical job is expected, not a failure. Real failures (unknown job, missing token, internal error) use the transport's error codes instead.

| Value | # | Description |
|-------|---|-------------|
| `SUBMIT_STATE_UNSPECIFIED` | 0 |  |
| `SUBMIT_STATE_SUBMITTED` | 1 | a new background job was started |
| `SUBMIT_STATE_ALREADY_RUNNING` | 2 | an identical job was already in flight; coalesced, not restarted |


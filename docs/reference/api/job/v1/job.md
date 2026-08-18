---
title: JobService
generated_from: reference/api/
description: "JobService is the daemon's control surface for background maintenance jobs."
tags: [api, proto, connect, grpc, jobservice]
---

# JobService

JobService is the daemon's control surface for background maintenance jobs. Trigger RPCs submit a job and return immediately; ListJobs reports every job's state. Read surfaces stay on the per-domain services - this one only mutates.

Package `magus.job.v1`, defined in `proto/magus/job/v1/job.proto`. Source: [job.proto:21](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L21). Part of the [daemon API](../../index.md).

## Methods

### SyncGraph

SyncGraph reconciles the knowledge graph to current source (rebuild and reindex).

`POST /magus.job.v1.JobService/SyncGraph`: unary. Source: [job.proto:23](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L23).

Takes [SyncGraphRequest](#syncgraphrequest), returns [SubmitJobResponse](#submitjobresponse).

### RotateActivities

RotateActivities trims the activity trail to its cap and drops orphaned payload blobs.

`POST /magus.job.v1.JobService/RotateActivities`: unary. Source: [job.proto:25](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L25).

Takes [RotateActivitiesRequest](#rotateactivitiesrequest), returns [SubmitJobResponse](#submitjobresponse).

### ClearCache

ClearCache invalidates cached build entries for the workspace.

`POST /magus.job.v1.JobService/ClearCache`: unary. Source: [job.proto:27](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L27).

Takes [ClearCacheRequest](#clearcacherequest), returns [SubmitJobResponse](#submitjobresponse).

### RotateLogs

RotateLogs trims the invocation run-log journals back to their cap.

`POST /magus.job.v1.JobService/RotateLogs`: unary. Source: [job.proto:29](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L29).

Takes [RotateLogsRequest](#rotatelogsrequest), returns [SubmitJobResponse](#submitjobresponse).

### ListJobs

ListJobs returns every registered job with its running state, last run, and target size.

`POST /magus.job.v1.JobService/ListJobs`: unary. Source: [job.proto:31](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L31).

Takes [ListJobsRequest](#listjobsrequest), returns [ListJobsResponse](#listjobsresponse).

## Messages

### ClearCacheRequest

Source: [job.proto:87](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L87).

No fields.

Used by: [ClearCache (request)](job.md#clearcache).

### JobInfo

JobInfo is the full picture of one job: what it is, whether an instance is running now, its most recent completed run, and the current magnitude of the resource it maintains.

Source: [job.proto:55](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L55).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | stable job name, e.g. "rotate-activities" |
| `description` | string | 2 |  |
| `running` | bool | 3 | an instance is in flight right now |
| `last_run` | [JobRun](#jobrun) | 4 | most recent completed run; unset if the job has not run this daemon session |
| `target` | [ResourceSize](#resourcesize) | 5 | current size of what the job operates on (trail, cache, or logs) |

Used by: [ClearCache (response)](job.md#clearcache), [ListJobs (response)](job.md#listjobs), [RotateActivities (response)](job.md#rotateactivities), [RotateLogs (response)](job.md#rotatelogs), [SyncGraph (response)](job.md#syncgraph).

### JobRun

JobRun is one completed execution of a job.

Source: [job.proto:64](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L64).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation_id` | string | 1 |  |
| `finished_at` | Timestamp | 2 |  |
| `duration` | Duration | 3 |  |
| `ok` | bool | 4 | false when the run errored |
| `error` | string | 5 | error text when ok is false |
| `items_removed` | int64 | 6 | Per-run deltas, populated only by jobs that measure them (a rotate reports what it dropped). Zero when the job does not report a delta yet - additive, so a job starts reporting later without a contract change. e.g. trail events pruned, cache entries invalidated |
| `bytes_reclaimed` | int64 | 7 | on-disk bytes freed |

Used by: [ClearCache (response)](job.md#clearcache), [ListJobs (response)](job.md#listjobs), [RotateActivities (response)](job.md#rotateactivities), [RotateLogs (response)](job.md#rotatelogs), [SyncGraph (response)](job.md#syncgraph).

### ListJobsRequest

Source: [job.proto:90](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L90).

No fields.

Used by: [ListJobs (request)](job.md#listjobs).

### ListJobsResponse

Source: [job.proto:91](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L91).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `jobs` | [repeated JobInfo](#jobinfo) | 1 | every registered job, in a stable order |

Used by: [ListJobs (response)](job.md#listjobs).

### ResourceSize

ResourceSize is the current magnitude of a job's target resource, for a caller to show how much there is to maintain (and to judge whether a rotate/clear is worth running).

Source: [job.proto:80](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L80).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `bytes` | int64 | 1 | total on-disk bytes |
| `count` | int64 | 2 | logical item count (trail events, cached entries, run logs) |

Used by: [ClearCache (response)](job.md#clearcache), [ListJobs (response)](job.md#listjobs), [RotateActivities (response)](job.md#rotateactivities), [RotateLogs (response)](job.md#rotatelogs), [SyncGraph (response)](job.md#syncgraph).

### RotateActivitiesRequest

Source: [job.proto:86](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L86).

No fields.

Used by: [RotateActivities (request)](job.md#rotateactivities).

### RotateLogsRequest

Source: [job.proto:88](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L88).

No fields.

Used by: [RotateLogs (request)](job.md#rotatelogs).

### SubmitJobResponse

SubmitJobResponse is returned by every trigger RPC: whether the job started or coalesced, the invocation id and console deep-link for its live log, and the job's full metadata snapshot so a caller can render "last rotated 3m ago, trail 2.1 MB" without a follow-up call.

Source: [job.proto:46](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L46).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `state` | [SubmitState](#submitstate) | 1 |  |
| `invocation_id` | string | 2 | the running job's invocation id (the new one, or the coalesced one) |
| `console_url` | string | 3 | deep-link to this invocation's live log; empty when no console is mounted |
| `job` | [JobInfo](#jobinfo) | 4 | the job's descriptor plus its last-run and current-size metadata |

Used by: [ClearCache (response)](job.md#clearcache), [RotateActivities (response)](job.md#rotateactivities), [RotateLogs (response)](job.md#rotatelogs), [SyncGraph (response)](job.md#syncgraph).

### SyncGraphRequest

Source: [job.proto:85](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L85).

No fields.

Used by: [SyncGraph (request)](job.md#syncgraph).

## Enums

### SubmitState

SubmitState is the disposition of a trigger RPC. Both values are SUCCESS outcomes returned in a normal response (not an error): coalescing an identical job is expected, not a failure. Real failures (unknown job, missing token, internal error) use the transport's error codes instead.

Source: [job.proto:37](https://github.com/egladman/magus/blob/main/proto/magus/job/v1/job.proto#L37).

| Value | # | Description |
|-------|---|-------------|
| `SUBMIT_STATE_UNSPECIFIED` | 0 |  |
| `SUBMIT_STATE_SUBMITTED` | 1 | a new background job was started |
| `SUBMIT_STATE_ALREADY_RUNNING` | 2 | an identical job was already in flight; coalesced, not restarted |

Used by: [ClearCache (response)](job.md#clearcache), [RotateActivities (response)](job.md#rotateactivities), [RotateLogs (response)](job.md#rotatelogs), [SyncGraph (response)](job.md#syncgraph).


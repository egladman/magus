---
title: JobService
generated_from: reference/api/
description: "JobService is the daemon's control surface for background maintenance jobs."
tags: [api, proto, connect, grpc, jobservice]
---

# JobService

JobService is the daemon's control surface for background maintenance jobs. Trigger RPCs submit a job and return immediately; ListJobs reports every job's state. Read surfaces stay on the per-domain services - this one only mutates.

Package `magus.job.v1alpha1`, defined in `proto/magus/job/v1alpha1/job.proto`. Source: [job.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L22). Part of the [daemon API](../../index.md).

## Methods

### ListJobs

ListJobs returns every registered job with its running state, last run, and target size.

`POST /magus.job.v1alpha1.JobService/ListJobs`: unary. Source: [job.proto:24](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L24).

Takes [ListJobsRequest](#listjobsrequest), returns [ListJobsResponse](#listjobsresponse).

### RunJob

RunJob submits the named job and returns immediately: whether it started or coalesced onto an identical in-flight run, where to watch it, and the job's fresh metadata.

One RPC over N job resources rather than one RPC per job. The four verbs this replaced were the same operation four times, which is why they had to share a response type and why buf.yaml had to waive RPC\_REQUEST\_RESPONSE\_UNIQUE and RPC\_RESPONSE\_STANDARD\_NAME to let them. Both waivers are gone with them. The property that argued for sharing the response - that adding a job must not touch this file in four places - is stronger here: a new job is a registry entry and NO schema change at all.

`POST /magus.job.v1alpha1.JobService/RunJob`: unary. Source: [job.proto:34](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L34).

Takes [RunJobRequest](#runjobrequest), returns [RunJobResponse](#runjobresponse).

## Messages

### Job

Job is the full picture of one job: what it is, whether an instance is running now, its most recent completed run, and the current magnitude of the resource it maintains.

Source: [job.proto:58](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L58).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | name is the resource name, "jobs/{job}" - e.g. "jobs/rotate-activities". The bare job id is the last segment, and is what the CLI's `server job <name>` leaf takes. |
| `description` | string | 2 |  |
| `running` | bool | 3 | an instance is in flight right now |
| `last_run` | [JobRun](#jobrun) | 4 | most recent completed run; unset if the job has not run this daemon session |
| `target` | [ResourceSize](#resourcesize) | 5 | current size of what the job operates on (trail, cache, or logs) |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobRun

JobRun is one completed execution of a job.

Source: [job.proto:69](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L69).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation_id` | string | 1 |  |
| `end_time` | Timestamp | 2 |  |
| `duration` | Duration | 3 |  |
| `ok` | bool | 4 | false when the run errored |
| `error` | string | 5 | error text when ok is false |
| `items_removed` | int64 | 6 | Per-run deltas, populated only by jobs that measure them (a rotate reports what it dropped). Zero when the job does not report a delta yet - additive, so a job starts reporting later without a contract change. e.g. trail events pruned, cache entries invalidated |
| `bytes_reclaimed` | int64 | 7 | on-disk bytes freed |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### ListJobsRequest

Paginated by contract so growth never forces a breaking change, though the registry is a fixed handful today and one page always holds it.

Source: [job.proto:99](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L99).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | _int32.lte: 1000; int32.gte: 0_ |
| `page_token` | string | 2 |  |

Used by: [ListJobs (request)](job.md#listjobs).

### ListJobsResponse

Source: [job.proto:103](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L103).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `jobs` | [repeated Job](#job) | 1 | every registered job, in a stable order |
| `next_page_token` | string | 2 | empty while one page holds the registry |

Used by: [ListJobs (response)](job.md#listjobs).

### ResourceSize

ResourceSize is the current magnitude of a job's target resource, for a caller to show how much there is to maintain (and to judge whether a rotate/clear is worth running).

Source: [job.proto:85](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L85).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `size_bytes` | int64 | 1 | total on-disk bytes |
| `item_count` | int64 | 2 | logical item count (trail events, cached entries, run logs) |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### RunJobRequest

Source: [job.proto:90](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L90).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | _string.pattern: `^jobs/[a-z][a-z0-9-]*$`_ name is the job's resource name, "jobs/{job}". An unregistered name is a NotFound error, not a SubmitState - the enum reports how a VALID submission resolved, and a name nobody registered never became a submission. |

Used by: [RunJob (request)](job.md#runjob).

### RunJobResponse

RunJobResponse reports what the submission did: whether the job started or coalesced, the invocation id and console deep-link for its live log, and the job's fresh metadata snapshot so a caller can render "last rotated 3m ago, trail 2.1 MB" without a follow-up call.

Source: [job.proto:49](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L49).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `state` | [SubmitState](#submitstate) | 1 |  |
| `invocation_id` | string | 2 | the running job's invocation id (the new one, or the coalesced one) |
| `console_url` | string | 3 | deep-link to this invocation's live log; empty when no console is mounted |
| `job` | [Job](#job) | 4 | the job's descriptor plus its last-run and current-size metadata |

Used by: [RunJob (response)](job.md#runjob).

## Enums

### SubmitState

SubmitState is the disposition of a trigger RPC. Both values are SUCCESS outcomes returned in a normal response (not an error): coalescing an identical job is expected, not a failure. Real failures (unknown job, missing token, internal error) use the transport's error codes instead.

Source: [job.proto:40](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L40).

| Value | # | Description |
|-------|---|-------------|
| `SUBMIT_STATE_UNSPECIFIED` | 0 |  |
| `SUBMIT_STATE_SUBMITTED` | 1 | a new background job was started |
| `SUBMIT_STATE_ALREADY_RUNNING` | 2 | an identical job was already in flight; coalesced, not restarted |

Used by: [RunJob (response)](job.md#runjob).


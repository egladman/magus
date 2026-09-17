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

### CompletionGate

CompletionGate is one machine-verifiable condition a job's completion is checked against, projected from types.CompletionGate. kind names WHAT it examines and expect names what must be true of it; check/paths/symbols carry whichever subject that kind actually uses. check is rendered as the command that runs it, the same way Job.check is - the wire never carries the unrendered form, so a client needs no second parser for it.

Source: [job.proto:123](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L123).

| Field         | Type            | # | Description |
| ------------- | --------------- | - | ----------- |
| `id`          | string          | 1 |             |
| `description` | string          | 2 |             |
| `kind`        | string          | 3 |             |
| `expect`      | string          | 4 |             |
| `check`       | string          | 5 |             |
| `paths`       | repeated string | 6 |             |
| `symbols`     | repeated string | 7 |             |
| `depends_on`  | repeated string | 8 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### Job

Job is the full picture of one job: what it is, who holds it, whether an instance is running now, its most recent run, and the current magnitude of the resource it maintains.

ONE message for both kinds. A catalog job fills the description and target; a delegated one fills the lanes and the check it was given; both carry id, holder and state, which is what lets a client render the two in one list without branching on which it has.

Source: [job.proto:74](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L74).

| Field              | Type                                       | #  | Description                                                                                                                                                                                                                                                         |
| ------------------ | ------------------------------------------ | -- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`             | string                                     | 1  | name is the resource name, "jobs/{job}" - e.g. "jobs/rotate-activities". The bare job id is the last segment, and is what the CLI's `job run <name>` leaf takes.                                                                                                    |
| `description`      | string                                     | 2  |                                                                                                                                                                                                                                                                     |
| `running`          | bool                                       | 3  | an instance is in flight right now                                                                                                                                                                                                                                  |
| `last_run`         | [JobRun](#jobrun)                          | 4  | most recent run; unset if the job has never been submitted                                                                                                                                                                                                          |
| `target`           | [ResourceSize](#resourcesize)              | 5  | current size of what the job operates on (trail, cache, or logs)                                                                                                                                                                                                    |
| `id`               | string                                     | 6  | the bare row id, without the "jobs/" collection segment                                                                                                                                                                                                             |
| `holder`           | [JobHolder](#jobholder)                    | 7  | who runs it                                                                                                                                                                                                                                                         |
| `state`            | string                                     | 8  | state is the row's lifecycle position: declared, running, exited, pass, fail or no\_return. A string rather than an enum because the vocabulary is the job store's and a second closed set here would be a second place to add a value to.                          |
| `criteria`         | string                                     | 9  | The facts a DELEGATED job carries, empty on a catalog job. These are what an orchestrator declared, never a verdict magus reached. Renamed from `goal`; the field NUMBER is the wire identity, so a peer built before the rename still reads and writes this field. |
| `parent`           | string                                     | 10 | the job this one was handed out under, empty for a root                                                                                                                                                                                                             |
| `model`            | string                                     | 11 |                                                                                                                                                                                                                                                                     |
| `check`            | string                                     | 12 | the one check this job runs, rendered as the command that runs it                                                                                                                                                                                                   |
| `write_paths`      | repeated string                            | 13 |                                                                                                                                                                                                                                                                     |
| `deny_paths`       | repeated string                            | 14 |                                                                                                                                                                                                                                                                     |
| `read_paths`       | repeated string                            | 15 |                                                                                                                                                                                                                                                                     |
| `depends_on`       | repeated string                            | 16 |                                                                                                                                                                                                                                                                     |
| `read_only`        | bool                                       | 17 |                                                                                                                                                                                                                                                                     |
| `checkpoint`       | string                                     | 18 |                                                                                                                                                                                                                                                                     |
| `releases`         | [repeated JobRelease](#jobrelease)         | 19 |                                                                                                                                                                                                                                                                     |
| `created`          | int64                                      | 20 | unix SECONDS, as the store records them                                                                                                                                                                                                                             |
| `updated`          | int64                                      | 21 |                                                                                                                                                                                                                                                                     |
| `completion_gates` | [repeated CompletionGate](#completiongate) | 22 | CompletionGates are the declared machine-verifiable acceptance conditions, empty on a job that named none beyond its primary check. See CompletionGate.                                                                                                             |
| `result`           | [JobResult](#jobresult)                    | 23 | Result is what the holder filed on exit, unset until it has.                                                                                                                                                                                                        |
| `deadline`         | int64                                      | 24 | Deadline is unix seconds past which the guard denies this job's writes; 0 when the fork set no timeout.                                                                                                                                                             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobOverlap

JobOverlap is one pair of jobs whose declared write paths intersect. Derived on every read and stored nowhere, and never a verdict: a client draws it and the reader decides whether their plan meant it.

Each side's intersecting declarations come separately, because the two are rarely the same string ("internal/job" and "internal/job/store.go" intersect) and a reader who cannot tell which job claimed which has nothing to act on.

Source: [job.proto:159](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L159).

| Field     | Type            | # | Description |
| --------- | --------------- | - | ----------- |
| `job_a`   | string          | 1 |             |
| `job_b`   | string          | 2 |             |
| `paths_a` | repeated string | 3 |             |
| `paths_b` | repeated string | 4 |             |

Used by: [ListJobs (response)](job.md#listjobs).

### JobRelease

JobRelease is a path a job gave up, and the version of it the next one inherits. The digest is the file's sha256 when there was a file; "absent" and "dir" are carried through as they are rather than turned into a hash-shaped lie.

Source: [job.proto:146](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L146).

| Field         | Type   | # | Description  |
| ------------- | ------ | - | ------------ |
| `path`        | string | 1 |              |
| `digest`      | string | 2 |              |
| `released_at` | int64  | 3 | unix seconds |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobResult

JobResult is what a holder filed when it exited: the paths it changed, the risks it left unresolved, and the jobs it spawned. A projection of types.JobResult's console-facing half; the schema version and per-gate evidence stay server-side, since nothing renders them yet.

Source: [job.proto:137](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L137).

| Field              | Type            | # | Description |
| ------------------ | --------------- | - | ----------- |
| `changed_paths`    | repeated string | 1 |             |
| `unresolved_risks` | repeated string | 2 |             |
| `descendants`      | repeated string | 3 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobRun

JobRun is one completed execution of a job.

Source: [job.proto:167](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L167).

| Field             | Type      | # | Description                                                                                                                                                                                                                                                           |
| ----------------- | --------- | - | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `invocation_id`   | string    | 1 |                                                                                                                                                                                                                                                                       |
| `end_time`        | Timestamp | 2 |                                                                                                                                                                                                                                                                       |
| `duration`        | Duration  | 3 |                                                                                                                                                                                                                                                                       |
| `ok`              | bool      | 4 | false when the run errored                                                                                                                                                                                                                                            |
| `error`           | string    | 5 | error text when ok is false                                                                                                                                                                                                                                           |
| `items_removed`   | int64     | 6 | Per-run deltas, populated only by jobs that measure them (a rotate reports what it dropped). Zero when the job does not report a delta yet - additive, so a job starts reporting later without a contract change. e.g. trail events pruned, cache entries invalidated |
| `bytes_reclaimed` | int64     | 7 | on-disk bytes freed                                                                                                                                                                                                                                                   |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### ListJobsRequest

Paginated by contract so growth never forces a breaking change, though the registry is a fixed handful today and one page always holds it.

Source: [job.proto:197](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L197).

| Field        | Type   | # | Description                     |
| ------------ | ------ | - | ------------------------------- |
| `page_size`  | int32  | 1 | _int32.lte: 1000; int32.gte: 0_ |
| `page_token` | string | 2 |                                 |

Used by: [ListJobs (request)](job.md#listjobs).

### ListJobsResponse

Source: [job.proto:201](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L201).

| Field             | Type                               | # | Description                                                                             |
| ----------------- | ---------------------------------- | - | --------------------------------------------------------------------------------------- |
| `jobs`            | [repeated Job](#job)               | 1 | every job, catalog and delegated, in a stable order                                     |
| `next_page_token` | string                             | 2 | empty while one page holds the registry                                                 |
| `overlaps`        | [repeated JobOverlap](#joboverlap) | 3 | overlaps are the pairs of jobs claiming common ground, derived from jobs on every read. |

Used by: [ListJobs (response)](job.md#listjobs).

### ResourceSize

ResourceSize is the current magnitude of a job's target resource, for a caller to show how much there is to maintain (and to judge whether a rotate/clear is worth running).

Source: [job.proto:183](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L183).

| Field        | Type  | # | Description                                                 |
| ------------ | ----- | - | ----------------------------------------------------------- |
| `size_bytes` | int64 | 1 | total on-disk bytes                                         |
| `item_count` | int64 | 2 | logical item count (trail events, cached entries, run logs) |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### RunJobRequest

Source: [job.proto:188](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L188).

| Field  | Type   | # | Description                                                                                                                                                                                                                                                       |
| ------ | ------ | - | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name` | string | 1 | _string.pattern: `^jobs/[a-z][a-z0-9-]*$`_ name is the job's resource name, "jobs/{job}". An unregistered name is a NotFound error, not a SubmitState - the enum reports how a VALID submission resolved, and a name nobody registered never became a submission. |

Used by: [RunJob (request)](job.md#runjob).

### RunJobResponse

RunJobResponse reports what the submission did: whether the job started or coalesced, the invocation id, and the job's fresh metadata snapshot so a caller can render "last rotated 3m ago, trail 2.1 MB" without a follow-up call.

Source: [job.proto:49](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L49).

| Field           | Type                        | # | Description                                                                                                                                                                                                                                                                                                         |
| --------------- | --------------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `state`         | [SubmitState](#submitstate) | 1 |                                                                                                                                                                                                                                                                                                                     |
| `invocation_id` | string                      | 2 | the running job's invocation id (the new one, or the coalesced one)                                                                                                                                                                                                                                                 |
| `console_url`   | string                      | 3 | Where to watch this job: the console's runs surface scoped to invocation\_id. A PATH, not an absolute URL, because the reader is the console itself and resolves it against its own origin. Empty only when the daemon coalesced a submit it could not name, since a run with no invocation has nothing to link to. |
| `job`           | [Job](#job)                 | 4 | the job's descriptor plus its last-run and current-size metadata                                                                                                                                                                                                                                                    |

Used by: [RunJob (response)](job.md#runjob).

## Enums

### JobHolder

JobHolder is who runs a job. One listing carries both kinds, so a reader can tell the daemon's own housekeeping from work a session was handed without asking a second door.

Source: [job.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L62).

| Value                    | # | Description                                             |
| ------------------------ | - | ------------------------------------------------------- |
| `JOB_HOLDER_UNSPECIFIED` | 0 |                                                         |
| `JOB_HOLDER_DAEMON`      | 1 | the daemon's own maintenance catalog                    |
| `JOB_HOLDER_SESSION`     | 2 | work an orchestrator declared for somebody else to hold |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### SubmitState

SubmitState is the disposition of a trigger RPC. Both values are SUCCESS outcomes returned in a normal response (not an error): coalescing an identical job is expected, not a failure. Real failures (unknown job, missing token, internal error) use the transport's error codes instead.

Source: [job.proto:40](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L40).

| Value                          | # | Description                                                      |
| ------------------------------ | - | ---------------------------------------------------------------- |
| `SUBMIT_STATE_UNSPECIFIED`     | 0 |                                                                  |
| `SUBMIT_STATE_SUBMITTED`       | 1 | a new background job was started                                 |
| `SUBMIT_STATE_ALREADY_RUNNING` | 2 | an identical job was already in flight; coalesced, not restarted |

Used by: [RunJob (response)](job.md#runjob).


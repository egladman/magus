---
title: JobService
generated_from: reference/api/
description: "JobService is the daemon's control service for background maintenance jobs."
tags: [api, proto, connect, grpc, jobservice]
---

# JobService

JobService is the daemon's control service for background maintenance jobs. Trigger RPCs submit a job and return immediately; ListJobs reports every job's state. Reads stay on the per-domain services - this one only mutates.

Package `magus.job.v1alpha1`, defined in `proto/magus/job/v1alpha1/job.proto`. Source: [job.proto:23](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L23). Part of the [daemon API](../../index.md).

## Methods

### ListJobs

ListJobs returns every registered job with its running state, last run, and target size.

`POST /magus.job.v1alpha1.JobService/ListJobs`: unary. Source: [job.proto:25](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L25).

Takes [ListJobsRequest](#listjobsrequest), returns [ListJobsResponse](#listjobsresponse).

### RunJob

RunJob submits the named job and returns immediately: whether it started or coalesced onto an identical in-flight run, where to watch it, and the job's fresh metadata.

One RPC over N job resources rather than one RPC per job. The four verbs this replaced were the same operation four times, which is why they had to share a response type and why buf.yaml had to waive RPC\_REQUEST\_RESPONSE\_UNIQUE and RPC\_RESPONSE\_STANDARD\_NAME to let them. Both waivers are gone with them. The property that argued for sharing the response - that adding a job must not touch this file in four places - is stronger here: a new job is a registry entry and NO schema change at all.

`POST /magus.job.v1alpha1.JobService/RunJob`: unary. Source: [job.proto:35](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L35).

Takes [RunJobRequest](#runjobrequest), returns [RunJobResponse](#runjobresponse).

## Messages

### CompletionGate

CompletionGate is one machine-verifiable condition a job's completion is checked against, projected from types.Goal. kind names WHAT it examines and expect names what must be true of it; check/paths/symbols carry whichever subject that kind actually uses. check is rendered as the command that runs it, the same way Job.check is - the wire never carries the unrendered form, so a client needs no second parser for it.

Source: [job.proto:231](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L231).

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

ONE message for both kinds. A catalog job fills the description and target; a delegated one fills the write paths and the check it was given; both carry id, holder and state, which is what lets a client render the two in one list without branching on which it has.

Source: [job.proto:77](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L77).

| Field            | Type                                                   | #  | Description                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------------- | ------------------------------------------------------ | -- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`           | string                                                 | 1  | name is the resource name, "jobs/{job}" - e.g. "jobs/rotate-activities". The bare job id is the last segment, and is what the CLI's `job run <name>` leaf takes.                                                                                                                                                                                                                                                                   |
| `description`    | string                                                 | 2  |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `running`        | bool                                                   | 3  | an instance is in flight right now                                                                                                                                                                                                                                                                                                                                                                                                 |
| `last_run`       | [JobRun](#jobrun)                                      | 4  | most recent run; unset if the job has never been submitted                                                                                                                                                                                                                                                                                                                                                                         |
| `target`         | [ResourceSize](#resourcesize)                          | 5  | current size of what the job operates on (trail, cache, or logs)                                                                                                                                                                                                                                                                                                                                                                   |
| `id`             | string                                                 | 6  | the bare row id, without the "jobs/" collection segment                                                                                                                                                                                                                                                                                                                                                                            |
| `holder`         | [JobHolder](#jobholder)                                | 7  | who runs it                                                                                                                                                                                                                                                                                                                                                                                                                        |
| `state`          | string                                                 | 8  | state is the row's lifecycle position: declared, running, exited, pass, fail or no\_return. A string rather than an enum because the vocabulary is the job store's and a second closed set here would be a second place to add a value to.                                                                                                                                                                                         |
| `criteria`       | string                                                 | 9  | The facts a DELEGATED job carries, empty on a catalog job. These are what an orchestrator declared, never a verdict magus reached. Renamed from `goal`; the field NUMBER is the wire identity, so a peer built before the rename still reads and writes this field.                                                                                                                                                                |
| `parent`         | string                                                 | 10 | the job this one was handed out under, empty for a root                                                                                                                                                                                                                                                                                                                                                                            |
| `model`          | string                                                 | 11 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `check`          | string                                                 | 12 | the one check this job runs, rendered as the command that runs it                                                                                                                                                                                                                                                                                                                                                                  |
| `write_paths`    | repeated string                                        | 13 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `deny_paths`     | repeated string                                        | 14 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `read_paths`     | repeated string                                        | 15 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `depends_on`     | repeated string                                        | 16 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `read_only`      | bool                                                   | 17 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `checkpoint`     | string                                                 | 18 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `releases`       | [repeated JobRelease](#jobrelease)                     | 19 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `created`        | int64                                                  | 20 | unix SECONDS, as the store records them                                                                                                                                                                                                                                                                                                                                                                                            |
| `updated`        | int64                                                  | 21 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `goals`          | [repeated CompletionGate](#completiongate)             | 22 | Goals are the job's declared definition of done, graded by `magus job wait`, empty on a job that named none beyond its primary check. See CompletionGate.                                                                                                                                                                                                                                                                          |
| `result`         | [JobResult](#jobresult)                                | 23 | Result is what the holder filed on exit, unset until it has.                                                                                                                                                                                                                                                                                                                                                                       |
| `deadline`       | int64                                                  | 24 | Deadline is unix seconds past which the guard denies this job's writes; 0 when the fork set no timeout.                                                                                                                                                                                                                                                                                                                            |
| `end_reason`     | string                                                 | 25 | The facts the store computes about a delegated row, the same ones `magus ls jobs` prints. end\_reason is why magus ended the row itself, empty when a holder or a person did. write\_proof is alone, disjoint or overlapping, what fork could prove about the write paths. reported\_base and base\_verdict are the base the worker landed on and how it compares to checkpoint. checkout\_root is the checkout that took the job. |
| `write_proof`    | string                                                 | 26 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `reported_base`  | string                                                 | 27 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `base_verdict`   | string                                                 | 28 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `checkout_root`  | string                                                 | 29 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `registered`     | int64                                                  | 30 | unix seconds the holder took the job, 0 while untaken                                                                                                                                                                                                                                                                                                                                                                              |
| `registered_by`  | [JobOrigin](#joborigin)                                | 31 | who declared the row                                                                                                                                                                                                                                                                                                                                                                                                               |
| `attempt`        | [JobAttempt](#jobattempt)                              | 32 | the newest recorded run of the job's own check                                                                                                                                                                                                                                                                                                                                                                                     |
| `gate_attempts`  | [repeated JobGateAttempt](#jobgateattempt)             | 33 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `unattributed`   | [repeated JobUnattributedWrite](#jobunattributedwrite) | 34 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `entries`        | [repeated JobEntry](#jobentry)                         | 35 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `integration`    | [JobIntegration](#jobintegration)                      | 36 | unset until the integrator verified the job                                                                                                                                                                                                                                                                                                                                                                                        |
| `schema_version` | int32                                                  | 38 | The row's schema envelope: the newest store schema any writer of it used, and the features a reader must implement to act on it (see ListJobsResponse.read\_only).                                                                                                                                                                                                                                                                 |
| `requires`       | repeated string                                        | 37 |                                                                                                                                                                                                                                                                                                                                                                                                                                    |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobAttempt

JobAttempt is a recorded run of a check, as `magus job wait` found it.

Source: [job.proto:156](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L156).

| Field          | Type   | # | Description |
| -------------- | ------ | - | ----------- |
| `found`        | bool   | 1 |             |
| `ref`          | string | 2 |             |
| `timestamp_ms` | int64  | 3 |             |
| `project`      | string | 4 |             |
| `target`       | string | 5 |             |
| `spell`        | string | 6 |             |
| `failed`       | bool   | 7 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobBlock

JobBlock is a live job waiting on a dependency that has not passed. state is empty when no row declares the dependency.

Source: [job.proto:205](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L205).

| Field   | Type   | # | Description |
| ------- | ------ | - | ----------- |
| `job`   | string | 1 |             |
| `on`    | string | 2 |             |
| `state` | string | 3 |             |

Used by: [ListJobs (response)](job.md#listjobs).

### JobEntry

JobEntry is an acknowledged write into the job's paths by somebody other than its holder.

Source: [job.proto:180](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L180).

| Field      | Type                    | # | Description                                            |
| ---------- | ----------------------- | - | ------------------------------------------------------ |
| `path`     | string                  | 1 |                                                        |
| `by`       | [JobOrigin](#joborigin) | 2 |                                                        |
| `at`       | int64                   | 3 | unix seconds                                           |
| `consumed` | int64                   | 4 | unix seconds the entry was consumed, 0 while it stands |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobGateAttempt

JobGateAttempt is the newest recorded run of one goal's check.

Source: [job.proto:167](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L167).

| Field     | Type                      | # | Description |
| --------- | ------------------------- | - | ----------- |
| `gate_id` | string                    | 1 |             |
| `attempt` | [JobAttempt](#jobattempt) | 2 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobGateEvidence

JobGateEvidence is the run a holder offered for one goal.

Source: [job.proto:254](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L254).

| Field        | Type   | # | Description |
| ------------ | ------ | - | ----------- |
| `gate_id`    | string | 1 |             |
| `output_ref` | string | 2 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobGateStatus

JobGateStatus is one gate's verdict at integration.

Source: [job.proto:196](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L196).

| Field        | Type            | # | Description |
| ------------ | --------------- | - | ----------- |
| `id`         | string          | 1 |             |
| `verified`   | bool            | 2 |             |
| `output_ref` | string          | 3 |             |
| `violations` | repeated string | 4 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobIntegration

JobIntegration is the integrator's verification of the job in its own checkout.

Source: [job.proto:188](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L188).

| Field      | Type                                     | # | Description  |
| ---------- | ---------------------------------------- | - | ------------ |
| `checkout` | string                                   | 1 |              |
| `at`       | int64                                    | 2 | unix seconds |
| `verified` | bool                                     | 3 |              |
| `gates`    | [repeated JobGateStatus](#jobgatestatus) | 4 |              |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobOrigin

JobOrigin is who did something to a row, one field per channel, as the activity trail records an action's origin.

Source: [job.proto:145](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L145).

| Field         | Type                                                         | # | Description |
| ------------- | ------------------------------------------------------------ | - | ----------- |
| `user`        | string                                                       | 1 |             |
| `uid`         | string                                                       | 2 |             |
| `entry_point` | string                                                       | 3 |             |
| `host`        | string                                                       | 4 |             |
| `session`     | string                                                       | 5 |             |
| `agent`       | string                                                       | 6 |             |
| `credential`  | [Credential](../../activity/v1alpha1/activity.md#credential) | 7 |             |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobOverlap

JobOverlap is one pair of jobs whose declared write paths intersect. Derived on every read and stored nowhere, and never a verdict: a client draws it and the reader decides whether their plan meant it.

Each side's intersecting declarations come separately, because the two are rarely the same string ("internal/job" and "internal/job/store.go" intersect) and a reader who cannot tell which job claimed which has nothing to act on.

Source: [job.proto:275](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L275).

| Field       | Type                                        | # | Description                                                                                                                                                                                                                                                                                      |
| ----------- | ------------------------------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `job_a`     | string                                      | 1 |                                                                                                                                                                                                                                                                                                  |
| `job_b`     | string                                      | 2 |                                                                                                                                                                                                                                                                                                  |
| `paths_a`   | repeated string                             | 3 |                                                                                                                                                                                                                                                                                                  |
| `paths_b`   | repeated string                             | 4 |                                                                                                                                                                                                                                                                                                  |
| `claims`    | string                                      | 5 | claims is disjoint when the two sides claim different declarations of the files they share, shared when one covers a declaration the other holds, and empty when neither claims below the file. footprint compares what the two jobs have actually changed, unset when it could not be measured. |
| `footprint` | [JobOverlapFootprint](#joboverlapfootprint) | 6 |                                                                                                                                                                                                                                                                                                  |

Used by: [ListJobs (response)](job.md#listjobs).

### JobOverlapFootprint

JobOverlapFootprint is whether two overlapping jobs' diffs touch the same declaration. verdict is disjoint, shared or unknown; shared lists the colliding locations; reason says why when it is unknown.

Source: [job.proto:220](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L220).

| Field     | Type            | # | Description |
| --------- | --------------- | - | ----------- |
| `verdict` | string          | 1 |             |
| `shared`  | repeated string | 2 |             |
| `reason`  | string          | 3 |             |

Used by: [ListJobs (response)](job.md#listjobs).

### JobReadOnly

JobReadOnly is a row this binary can read but not write: the store features it lacks.

Source: [job.proto:212](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L212).

| Field   | Type            | # | Description |
| ------- | --------------- | - | ----------- |
| `job`   | string          | 1 |             |
| `lacks` | repeated string | 2 |             |

Used by: [ListJobs (response)](job.md#listjobs).

### JobRelease

JobRelease is a path a job gave up, and the version of it the next one inherits. The digest is the file's sha256 when there was a file; "absent" and "dir" are carried through as they are rather than turned into a hash-shaped lie.

Source: [job.proto:262](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L262).

| Field         | Type   | # | Description  |
| ------------- | ------ | - | ------------ |
| `path`        | string | 1 |              |
| `digest`      | string | 2 |              |
| `released_at` | int64  | 3 | unix seconds |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobResult

JobResult is what a holder filed when it exited: the paths it changed, the risks it left unresolved, the jobs it spawned, and the runs it offered as evidence.

Source: [job.proto:244](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L244).

| Field                   | Type                                         | # | Description                             |
| ----------------------- | -------------------------------------------- | - | --------------------------------------- |
| `changed_paths`         | repeated string                              | 1 |                                         |
| `unresolved_risks`      | repeated string                              | 2 |                                         |
| `descendants`           | repeated string                              | 3 |                                         |
| `validation_command`    | string                                       | 4 | the command the holder ran as its check |
| `validation_output_ref` | string                                       | 5 | that run's output ref                   |
| `gate_evidence`         | [repeated JobGateEvidence](#jobgateevidence) | 6 |                                         |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### JobRun

JobRun is one completed execution of a job.

Source: [job.proto:289](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L289).

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

### JobUnattributedWrite

JobUnattributedWrite is a write inside the job's paths that no lease claimed.

Source: [job.proto:173](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L173).

| Field    | Type   | # | Description  |
| -------- | ------ | - | ------------ |
| `path`   | string | 1 |              |
| `digest` | string | 2 |              |
| `at`     | int64  | 3 | unix seconds |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### ListJobsRequest

Paginated by contract so growth never forces a breaking change, though the registry is a fixed handful today and one page always holds it.

Source: [job.proto:319](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L319).

| Field        | Type   | # | Description                     |
| ------------ | ------ | - | ------------------------------- |
| `page_size`  | int32  | 1 | _int32.lte: 1000; int32.gte: 0_ |
| `page_token` | string | 2 |                                 |

Used by: [ListJobs (request)](job.md#listjobs).

### ListJobsResponse

Source: [job.proto:323](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L323).

| Field             | Type                                 | # | Description                                                                                                                                            |
| ----------------- | ------------------------------------ | - | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `jobs`            | [repeated Job](#job)                 | 1 | every job, catalog and delegated, in a stable order                                                                                                    |
| `next_page_token` | string                               | 2 | empty while one page holds the registry                                                                                                                |
| `overlaps`        | [repeated JobOverlap](#joboverlap)   | 3 | overlaps are the pairs of jobs claiming common ground, derived from jobs on every read.                                                                |
| `overdue`         | repeated string                      | 4 | The live rows the store flags on every read, by id: past their deadline, left with an ended ancestor, and untouched for longer than jobs.stale\_after. |
| `orphans`         | repeated string                      | 5 |                                                                                                                                                        |
| `stale`           | repeated string                      | 6 |                                                                                                                                                        |
| `blocked`         | [repeated JobBlock](#jobblock)       | 7 | live jobs waiting on a dependency that has not passed                                                                                                  |
| `read_only`       | [repeated JobReadOnly](#jobreadonly) | 8 | rows this daemon's binary can read but not write                                                                                                       |

Used by: [ListJobs (response)](job.md#listjobs).

### ResourceSize

ResourceSize is the current magnitude of a job's target resource, for a caller to show how much there is to maintain (and to judge whether a rotate/clear is worth running).

Source: [job.proto:305](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L305).

| Field        | Type  | # | Description                                                 |
| ------------ | ----- | - | ----------------------------------------------------------- |
| `size_bytes` | int64 | 1 | total on-disk bytes                                         |
| `item_count` | int64 | 2 | logical item count (trail events, cached entries, run logs) |

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### RunJobRequest

Source: [job.proto:310](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L310).

| Field  | Type   | # | Description                                                                                                                                                                                                                                                       |
| ------ | ------ | - | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name` | string | 1 | _string.pattern: `^jobs/[a-z][a-z0-9-]*$`_ name is the job's resource name, "jobs/{job}". An unregistered name is a NotFound error, not a SubmitState - the enum reports how a VALID submission resolved, and a name nobody registered never became a submission. |

Used by: [RunJob (request)](job.md#runjob).

### RunJobResponse

RunJobResponse reports what the submission did: whether the job started or coalesced, the invocation id, and the job's fresh metadata snapshot so a caller can render "last rotated 3m ago, trail 2.1 MB" without a follow-up call.

Source: [job.proto:50](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L50).

| Field           | Type                        | # | Description                                                                                                                                                                                                                                                                                                     |
| --------------- | --------------------------- | - | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `state`         | [SubmitState](#submitstate) | 1 |                                                                                                                                                                                                                                                                                                                 |
| `invocation_id` | string                      | 2 | the running job's invocation id (the new one, or the coalesced one)                                                                                                                                                                                                                                             |
| `console_url`   | string                      | 3 | Where to watch this job: the console's runs app scoped to invocation\_id. A PATH, not an absolute URL, because the reader is the console itself and resolves it against its own origin. Empty only when the daemon coalesced a submit it could not name, since a run with no invocation has nothing to link to. |
| `job`           | [Job](#job)                 | 4 | the job's descriptor plus its last-run and current-size metadata                                                                                                                                                                                                                                                |

Used by: [RunJob (response)](job.md#runjob).

## Enums

### JobHolder

JobHolder is who runs a job. One listing carries both kinds, so a reader can tell the daemon's own housekeeping from work a session was handed without asking a second door.

Source: [job.proto:63](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L63).

| Value                    | # | Description                                             |
| ------------------------ | - | ------------------------------------------------------- |
| `JOB_HOLDER_UNSPECIFIED` | 0 |                                                         |
| `JOB_HOLDER_SESSION`     | 2 | work an orchestrator declared for somebody else to hold |
| `JOB_HOLDER_SERVER`      | 3 | the daemon's own maintenance catalog                    |

_Reserved: 1; `JOB_HOLDER_DAEMON`._

Used by: [ListJobs (response)](job.md#listjobs), [RunJob (response)](job.md#runjob).

### SubmitState

SubmitState is the disposition of a trigger RPC. Both values are SUCCESS outcomes returned in a normal response (not an error): coalescing an identical job is expected, not a failure. Real failures (unknown job, missing token, internal error) use the transport's error codes instead.

Source: [job.proto:41](https://github.com/egladman/magus/blob/main/proto/magus/job/v1alpha1/job.proto#L41).

| Value                          | # | Description                                                      |
| ------------------------------ | - | ---------------------------------------------------------------- |
| `SUBMIT_STATE_UNSPECIFIED`     | 0 |                                                                  |
| `SUBMIT_STATE_SUBMITTED`       | 1 | a new background job was started                                 |
| `SUBMIT_STATE_ALREADY_RUNNING` | 2 | an identical job was already in flight; coalesced, not restarted |

Used by: [RunJob (response)](job.md#runjob).


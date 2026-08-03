---
title: StatusService
description: StatusService serves the snapshot, and streams it for a live dashboard.
tags: [api, proto, connect, grpc, statusservice]
---

# StatusService

StatusService serves the snapshot, and streams it for a live dashboard.

Package `magus.status.v1`, defined in `proto/magus/status/v1/status.proto`. Part of the [daemon API](index.md).

## Methods

### GetStatus

GetStatus returns the current snapshot.

`POST /magus.status.v1.StatusService/GetStatus` - unary.

Takes `GetStatusRequest`, returns `GetStatusResponse`.

### StreamStatus

StreamStatus pushes a fresh snapshot whenever the pool changes (or on a heartbeat), so a dashboard reflects what is running without polling.

`POST /magus.status.v1.StatusService/StreamStatus` - server streaming.

Takes `StreamStatusRequest`, returns `StreamStatusResponse`.

## Messages

### BuildInfo

BuildInfo identifies the running magus binary: the version tag, the commit it was built from, the build date, and the full human fingerprint (what `magus --version` prints). Reported so a dashboard shows exactly which daemon it is talking to. All fields are "unknown" for an unstamped dev build.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `version` | string | 1 | git describe, e.g. "v0.1.0-3-gabc1234" |
| `commit` | string | 2 | short commit hash |
| `date` | string | 3 | build date, RFC3339 |
| `fingerprint` | string | 4 | full identity: "magus <version> (<commit>) built <date>" |

### Cache

Cache is live cache ACTIVITY: the hit/miss/error tallies a warm cache has served this session plus its real on-disk size. These are running counters (not static config like the cap or immutability), so a dashboard plots hit-rate over time by sampling the stream.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `hits` | int64 | 1 |  |
| `misses` | int64 | 2 |  |
| `errors` | int64 | 3 |  |
| `size_bytes` | int64 | 4 | real on-disk size of the cache dir (0 = unknown/not computed) |
| `size_cap_mb` | int32 | 5 | configured cap (MAGUS\_CACHE\_SIZE\_MB; 0 = unlimited) |

### Config

Config is the daemon's resolved, read-only configuration a dashboard shows so an operator can see what the daemon is set to do without a terminal round-trip. Static per session, so it rides GetStatusResponse (the one-shot), never the live Status frame.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `default_charms` | repeated string | 1 | the charms applied to every run by default |
| `concurrency` | int32 | 2 | the concurrency cap (0 = unlimited) |
| `sandbox` | bool | 3 | whether filesystem sandboxing is on |

### GetStatusRequest

No fields.

### GetStatusResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `status` | Status | 1 |  |
| `observing_since` | Timestamp | 2 | observing\_since and config ride the ONE-SHOT response envelope, NOT the streamed Status frame: they are static per daemon session (Status stays "what is happening right now"), so a dashboard reads them once via GetStatus rather than on every StreamStatus push. This is the typed home for the two fields the deprecated JSON /api/v1/status route used to carry. when this daemon began observing (its start) |
| `config` | Config | 3 | the daemon's resolved, read-only configuration |

### Lock

Lock is one held per-project workspace lock and the process holding it.  A held lock is the NORMAL state of a mutating run, so this is state and never a fault: it must not fail a readiness or liveness check, because a run queued behind a peer is waiting correctly and restarting it only sends it to the back of the queue. It is on the wire because an OS file lock carries no identity of its own, so without the holder a blocked run is indistinguishable from a hung one - and a lock is held for exactly as long as its holder lives, which means one held by a process nobody remembers starting blocks everything else silently and forever.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `project` | string | 1 | workspace-relative path; "." is the root |
| `pid` | int32 | 2 | holder's process id |
| `command` | string | 3 | holder's argv, for recognizing what it is |
| `dir` | string | 4 | holder's working directory; a path that no longer exists means abandoned |
| `acquire_time` | Timestamp | 5 | when the holder took it; age is the signal a human reads |
| `waiters` | repeated LockWaiter | 6 | processes blocked on this lock right now |
| `stale_after_seconds` | int32 | 7 | when to read this holder as possibly abandoned rather than busy |

### LockWaiter

LockWaiter is one process blocked on a lock. A holder answers "who is working"; a waiter answers "who is stalled because of it", which is the question anyone looking at a queue that is not moving is actually asking. Transient by nature, so a snapshot.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `project` | string | 1 | reserved for a flattened view; empty inside Lock |
| `pid` | int32 | 2 |  |
| `command` | string | 3 |  |
| `dir` | string | 4 |  |
| `wait_time` | Timestamp | 5 | when it began waiting |

### Pool

Pool is the live concurrency pool - the slots and the work occupying them.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `parent_pid` | int32 | 1 |  |
| `daemon_version` | string | 2 |  |
| `mode` | string | 3 | "daemon" \| "proc" \| "" |
| `capacity` | int32 | 4 | total concurrency slots (0 = unlimited) |
| `running` | int32 | 5 | slots currently running |
| `queued` | int32 | 6 | tasks queued for a slot |
| `running_targets` | repeated RunningTarget | 7 | what is running right now |
| `workspaces` | repeated Workspace | 8 |  |
| `affected` | repeated string | 9 |  |
| `cache` | Cache | 10 | aggregate cache activity across the warm workspaces |

### Run

Run is one in-flight invocation the daemon has adopted - a `magus run`/`affected` dispatch, keyed by its invocation id. It carries the per-target execution state a dashboard renders as a live run row, so the SAME status stream that shows the pool also shows what each run's targets are doing.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `inv` | string | 1 | invocation id (inv...); deep-links to the run's live log |
| `trigger` | string | 2 | how the run was spawned: run \| affected \| ci \| ... |
| `started_at` | Timestamp | 3 | when the invocation opened |
| `targets` | repeated TargetRun | 4 | per-target execution state within this run |

### RunningTarget

RunningTarget is one running unit of work in the pool.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `args` | repeated string | 1 | the argument vector (carries the target/project) |
| `workspace` | string | 2 |  |
| `start_time` | Timestamp | 3 | when the running target started |
| `step` | string | 4 | the cache step currently executing |
| `invocation` | string | 5 | the invocation id (inv...) this running target belongs to; deep-links to its live log |

### Service

Service is one long-running shared service the daemon is hosting right now, kept warm across invocations. It carries the derived identity (id/label/command/ports), the live state a dashboard renders, and how many targets currently depend on it.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 | short service id (fingerprint prefix) |
| `label` | string | 2 | human name: image[:tag] or the binary basename |
| `command` | string | 3 | full process command, space-joined |
| `port` | repeated string | 4 | container-side published ports (empty if unknown) |
| `state` | string | 5 | starting \| running \| idle \| failed |
| `dependents` | int32 | 6 | targets currently depending on this service |
| `started_at` | Timestamp | 7 | when the registry began starting this instance |

### Status

Status is the live snapshot.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `health` | Health | 1 |  |
| `pool` | Pool | 2 | live concurrency; absent when no daemon/pool is running |
| `runs` | repeated Run | 4 | runs the daemon is executing right now (adopted dispatches) |
| `services` | repeated Service | 5 | long-running shared services the daemon is hosting right now |
| `build` | BuildInfo | 6 | the running daemon's build identity |
| `locks` | repeated Lock | 7 | per-project workspace locks held right now |

### StreamStatusRequest

No fields.

### StreamStatusResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `status` | Status | 1 |  |

### TargetRun

TargetRun is the execution state of one target within a Run. It advances QUEUED -> RUNNING -> PASSED\|FAILED\|CACHED as the run emits journal events; a finished target carries its output reference and wall-clock duration.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `project` | string | 1 | repo-relative project path |
| `target` | string | 2 | target name (as the CLI spells it) |
| `state` | State | 3 |  |
| `started_at` | Timestamp | 4 | when the target began running (unset while QUEUED) |
| `ended_at` | Timestamp | 5 | when the target finished (unset while active) |
| `output_ref` | string | 6 | output reference, once finished |
| `duration_ms` | int64 | 7 | wall-clock duration in ms, once finished |

### Workspace

Workspace is one workspace the daemon has loaded.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `root` | string | 1 |  |
| `load_time` | Timestamp | 2 |  |
| `last_access_time` | Timestamp | 3 |  |
| `cache` | Cache | 4 | this workspace's cache activity |

## Enums

### Health

Health is the at-a-glance rollup a dashboard shows.

| Value | # | Description |
|-------|---|-------------|
| `HEALTH_UNSPECIFIED` | 0 |  |
| `HEALTH_HEALTHY` | 1 | daemon reachable, pool nominal |
| `HEALTH_DEGRADED` | 2 | reachable but something is off (pool error, saturation) |
| `HEALTH_DOWN` | 3 | no daemon / pool |

### State

State is where a target sits in its lifecycle. Values carry the STATE\_ prefix because protobuf enum values share their PARENT's scope, so an unprefixed CACHED would collide with any other enum declaring the same name in this package. STATE\_UNSPECIFIED always followed the convention; the rest did not, which buf's ENUM\_VALUE\_PREFIX rule caught once proto's lint target started running. Renaming a value leaves the wire untouched - encoding is by number, and these are unchanged.

| Value | # | Description |
|-------|---|-------------|
| `STATE_UNSPECIFIED` | 0 |  |
| `STATE_QUEUED` | 1 | scheduled, not yet started |
| `STATE_RUNNING` | 2 | a subprocess is executing |
| `STATE_PASSED` | 3 | finished successfully |
| `STATE_FAILED` | 4 | finished with an error |
| `STATE_CACHED` | 5 | satisfied from cache (no work run) |


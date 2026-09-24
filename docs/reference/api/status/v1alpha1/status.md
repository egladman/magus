---
title: StatusService
generated_from: reference/api/
description: StatusService serves the snapshot, and streams it for a live dashboard.
tags: [api, proto, connect, grpc, statusservice]
---

# StatusService

StatusService serves the snapshot, and streams it for a live dashboard.

Package `magus.status.v1alpha1`, defined in `proto/magus/status/v1alpha1/status.proto`. Source: [status.proto:245](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L245). Part of the [server API](../../index.md).

## Methods

### GetStatus

GetStatus returns the current snapshot.

`POST /magus.status.v1alpha1.StatusService/GetStatus`: unary. Source: [status.proto:247](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L247).

Takes [GetStatusRequest](#getstatusrequest), returns [GetStatusResponse](#getstatusresponse).

### StreamStatus

StreamStatus pushes a fresh snapshot whenever the pool changes (or on a heartbeat), so a dashboard reflects what is running without polling.

`POST /magus.status.v1alpha1.StatusService/StreamStatus`: server streaming. Source: [status.proto:250](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L250).

Takes [StreamStatusRequest](#streamstatusrequest), returns [StreamStatusResponse](#streamstatusresponse).

## Messages

### Broker

Broker is the per-user process holding this host's capacity (concurrency slots and declared memory) and the services every magus on it shares. It listens on a unix socket only and exits once it has held nothing for its idle window.

Source: [status.proto:41](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L41).

| Field               | Type                  | # | Description                                      |
| ------------------- | --------------------- | - | ------------------------------------------------ |
| `pid`               | int32                 | 1 |                                                  |
| `version`           | string                | 2 |                                                  |
| `protocol`          | int32                 | 3 | the broker wire version it speaks                |
| `socket`            | string                | 4 |                                                  |
| `executable`        | string                | 5 | the binary it runs from                          |
| `start_time`        | Timestamp             | 6 |                                                  |
| `capacity`          | [Capacity](#capacity) | 7 |                                                  |
| `idle_exit_seconds` | int32                 | 8 | how long it stays up once it holds nothing       |
| `draining`          | bool                  | 9 | stopping: seats nothing new while holders finish |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### BuildInfo

BuildInfo identifies the running magus binary: the version tag, the commit it was built from, the build date, and the full human fingerprint (what `magus --version` prints). Reported so a dashboard shows exactly which server it is talking to. All fields are "unknown" for an unstamped dev build.

Source: [status.proto:114](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L114).

| Field         | Type   | # | Description                                              |
| ------------- | ------ | - | -------------------------------------------------------- |
| `version`     | string | 1 | git describe, e.g. "v0.1.0-3-gabc1234"                   |
| `commit`      | string | 2 | short commit hash                                        |
| `date`        | string | 3 | build date, RFC3339                                      |
| `fingerprint` | string | 4 | full identity: "magus <version> (<commit>) built <date>" |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Cache

Cache is live cache ACTIVITY: the hit/miss/error tallies a warm cache has served this session plus its real on-disk size. These are running counters (not static config like the cap or immutability), so a dashboard plots hit-rate over time by sampling the stream.

Source: [status.proto:228](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L228).

| Field         | Type  | # | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ------------- | ----- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `hits`        | int64 | 1 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `misses`      | int64 | 2 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `errors`      | int64 | 3 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `size_bytes`  | int64 | 4 | real on-disk size of the cache dir (0 = unknown/not computed)                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `size_cap_mb` | int32 | 5 | configured cap (MAGUS\_CACHE\_SIZE\_MB; 0 = unlimited)                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `saved_ms`    | int64 | 6 | saved\_ms is the summed recorded duration of the runs those hits replayed - the work the cache avoided, measured rather than modeled: each figure is how long that exact target took on this machine when it last ran, carried on the cache entry.  It UNDERSTATES and never overstates. A hit on an entry written before the duration was recorded counts toward hits and adds nothing here, so a reader must not present this as the cache's lifetime saving - it is what this server has saved since it started. |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Capacity

Capacity is the host's whole budget, what is held, and every claim holding it.

Source: [status.proto:54](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L54).

| Field          | Type                     | # | Description    |
| -------------- | ------------------------ | - | -------------- |
| `budget_mb`    | int32                    | 1 | 0 = unmeasured |
| `held_mb`      | int32                    | 2 |                |
| `budget_slots` | int32                    | 3 | 0 = unmeasured |
| `held_slots`   | int32                    | 4 |                |
| `holders`      | [repeated Claim](#claim) | 5 | oldest first   |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Claim

Claim is one step holding capacity, and who is running it.

Source: [status.proto:63](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L63).

| Field        | Type      | # | Description                       |
| ------------ | --------- | - | --------------------------------- |
| `project`    | string    | 1 |                                   |
| `target`     | string    | 2 |                                   |
| `pid`        | int32     | 3 |                                   |
| `memory_mb`  | int32     | 4 |                                   |
| `slots`      | int32     | 5 |                                   |
| `dir`        | string    | 6 | where the holding run was started |
| `command`    | string    | 7 | the holder's argv                 |
| `start_time` | Timestamp | 8 | when the claim was granted        |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Config

Config is the server's resolved, read-only configuration a dashboard shows so an operator can see what the server is set to do without a terminal round-trip. Static per session, so it rides GetStatusResponse (the one-shot), never the live Status frame.

Source: [status.proto:267](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L267).

| Field            | Type            | # | Description                                |
| ---------------- | --------------- | - | ------------------------------------------ |
| `default_charms` | repeated string | 1 | the charms applied to every run by default |
| `concurrency`    | int32           | 2 | the concurrency cap (0 = unlimited)        |
| `sandbox`        | bool            | 3 | whether filesystem sandboxing is on        |

Used by: [GetStatus (response)](status.md#getstatus).

### GetStatusRequest

Source: [status.proto:253](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L253).

No fields.

Used by: [GetStatus (request)](status.md#getstatus).

### GetStatusResponse

Source: [status.proto:254](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L254).

| Field                | Type              | # | Description                                                                                                                                                                                                                                                                                                                                                                                                              |
| -------------------- | ----------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `status`             | [Status](#status) | 1 |                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `observe_start_time` | Timestamp         | 2 | observe\_start\_time and config ride the ONE-SHOT response envelope, NOT the streamed Status frame: they are static per server session (Status stays "what is happening right now"), so a dashboard reads them once via GetStatus rather than on every StreamStatus push. This is the typed home for the two fields the deprecated JSON /api/v1/status route used to carry. when this server began observing (its start) |
| `config`             | [Config](#config) | 3 | the server's resolved, read-only configuration                                                                                                                                                                                                                                                                                                                                                                           |

Used by: [GetStatus (response)](status.md#getstatus).

### Listener

Listener is one address the server accepts connections on.

Source: [status.proto:86](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L86).

| Field     | Type   | # | Description    |
| --------- | ------ | - | -------------- |
| `kind`    | string | 1 | socket \| http |
| `address` | string | 2 |                |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Lock

Lock is one held per-project workspace lock and the process holding it.

A held lock is the NORMAL state of a mutating run, so this is state and never a fault: it must not fail a readiness or liveness check. It is on the wire because an OS file lock carries no identity of its own, so without the holder a refused run cannot say who refused it - and a lock is held for exactly as long as its holder lives, which means one held by a process nobody remembers starting refuses every other run until someone finds it.

Source: [status.proto:99](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L99).

| Field                 | Type      | # | Description                                                              |
| --------------------- | --------- | - | ------------------------------------------------------------------------ |
| `project`             | string    | 1 | workspace-relative path; "." is the root                                 |
| `pid`                 | int32     | 2 | holder's process id                                                      |
| `command`             | string    | 3 | holder's argv, for recognizing what it is                                |
| `dir`                 | string    | 4 | holder's working directory; a path that no longer exists means abandoned |
| `acquire_time`        | Timestamp | 5 | when the holder took it; age is the signal a human reads                 |
| `stale_after_seconds` | int32     | 7 | when to read this holder as possibly abandoned rather than busy          |

_Reserved: 6; `waiters`._

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Pool

Pool is the live concurrency pool - the slots and the work occupying them.

Source: [status.proto:174](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L174).

| Field             | Type                                     | #  | Description                                         |
| ----------------- | ---------------------------------------- | -- | --------------------------------------------------- |
| `parent_pid`      | int32                                    | 1  |                                                     |
| `owner_version`   | string                                   | 11 | the build of the process that owns the pool         |
| `capacity`        | int32                                    | 4  | total concurrency slots (0 = unlimited)             |
| `running`         | int32                                    | 5  | slots currently running                             |
| `queued`          | int32                                    | 6  | targets queued for a slot                           |
| `running_targets` | [repeated RunningTarget](#runningtarget) | 7  | what is running right now                           |
| `workspaces`      | [repeated Workspace](#workspace)         | 8  |                                                     |
| `affected`        | repeated string                          | 9  |                                                     |
| `cache`           | [Cache](#cache)                          | 10 | aggregate cache activity across the warm workspaces |

_Reserved: 2, 3; `daemon_version`, `mode`._

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Run

Run is one in-flight invocation the server has adopted - a `magus run`/`affected` dispatch, keyed by its invocation id. It carries the per-target execution state a dashboard renders as a live run row, so the SAME status stream that shows the pool also shows what each run's targets are doing.

Source: [status.proto:125](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L125).

| Field        | Type                             | # | Description                                              |
| ------------ | -------------------------------- | - | -------------------------------------------------------- |
| `inv`        | string                           | 1 | invocation id (inv...); deep-links to the run's live log |
| `trigger`    | string                           | 2 | how the run was spawned: run \| affected \| ci \| ...    |
| `start_time` | Timestamp                        | 3 | when the invocation opened                               |
| `targets`    | [repeated TargetRun](#targetrun) | 4 | per-target execution state within this run               |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### RunningTarget

RunningTarget is one running unit of work in the pool.

Source: [status.proto:189](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L189).

| Field        | Type            | # | Description                                                                           |
| ------------ | --------------- | - | ------------------------------------------------------------------------------------- |
| `args`       | repeated string | 1 | the argument vector (carries the target/project)                                      |
| `workspace`  | string          | 2 |                                                                                       |
| `start_time` | Timestamp       | 3 | when the running target started                                                       |
| `step`       | string          | 4 | the cache step currently executing                                                    |
| `invocation` | string          | 5 | the invocation id (inv...) this running target belongs to; deep-links to its live log |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Server

Server is the person-started process serving MCP, the console, the APIs and jobs.

Source: [status.proto:75](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L75).

| Field        | Type                           | # | Description                                              |
| ------------ | ------------------------------ | - | -------------------------------------------------------- |
| `pid`        | int32                          | 1 |                                                          |
| `version`    | string                         | 2 |                                                          |
| `socket`     | string                         | 3 |                                                          |
| `executable` | string                         | 4 |                                                          |
| `start_time` | Timestamp                      | 5 |                                                          |
| `listeners`  | [repeated Listener](#listener) | 6 |                                                          |
| `watch`      | repeated string                | 7 | workspace roots whose graph and symbols it keeps current |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Service

Service is one long-running shared service the server is hosting right now, kept warm across invocations. It carries the derived identity (id/label/command/ports), the live state a dashboard renders, and how many targets currently depend on it.

Source: [status.proto:163](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L163).

| Field        | Type            | # | Description                                       |
| ------------ | --------------- | - | ------------------------------------------------- |
| `id`         | string          | 1 | short service id (fingerprint prefix)             |
| `label`      | string          | 2 | human name: image[:tag] or the binary basename    |
| `command`    | string          | 3 | full process command, space-joined                |
| `ports`      | repeated string | 4 | container-side published ports (empty if unknown) |
| `state`      | string          | 5 | starting \| running \| idle \| failed             |
| `dependents` | int32           | 6 | targets currently depending on this service       |
| `start_time` | Timestamp       | 7 | when the registry began starting this instance    |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Status

Status is the live snapshot.

Source: [status.proto:24](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L24).

| Field           | Type                         | #  | Description                                                                       |
| --------------- | ---------------------------- | -- | --------------------------------------------------------------------------------- |
| `health`        | [Health](#health)            | 1  |                                                                                   |
| `pool`          | [Pool](#pool)                | 2  | live concurrency; absent when no server/pool is running                           |
| `runs`          | [repeated Run](#run)         | 4  | runs the server is executing right now (adopted dispatches)                       |
| `services`      | [repeated Service](#service) | 5  | shared services the broker is hosting right now; the same list as broker.services |
| `build`         | [BuildInfo](#buildinfo)      | 6  | the running server's build identity                                               |
| `locks`         | [repeated Lock](#lock)       | 7  | per-project workspace locks held right now                                        |
| `broker`        | [Broker](#broker)            | 8  | the broker holding this host's capacity; absent when none is running              |
| `server`        | [Server](#server)            | 9  | the server itself; absent when none is running                                    |
| `broker_policy` | string                       | 10 | required \| best-effort \| off: what a missing broker means                       |

_Reserved: 3; `magus_version`._

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### StreamStatusRequest

Source: [status.proto:272](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L272).

No fields.

Used by: [StreamStatus (request)](status.md#streamstatus).

### StreamStatusResponse

Source: [status.proto:273](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L273).

| Field    | Type              | # | Description |
| -------- | ----------------- | - | ----------- |
| `status` | [Status](#status) | 1 |             |

Used by: [StreamStatus (response)](status.md#streamstatus).

### TargetRun

TargetRun is the execution state of one target within a Run. It advances QUEUED -> RUNNING -> PASSED\|FAILED\|CACHED as the run emits journal events; a finished target carries its output reference and wall-clock duration.

Source: [status.proto:135](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L135).

| Field         | Type                               | # | Description                                        |
| ------------- | ---------------------------------- | - | -------------------------------------------------- |
| `project`     | string                             | 1 | repo-relative project path                         |
| `target`      | string                             | 2 | target name (as the CLI spells it)                 |
| `state`       | [TargetRun.State](#targetrunstate) | 3 |                                                    |
| `start_time`  | Timestamp                          | 4 | when the target began running (unset while QUEUED) |
| `end_time`    | Timestamp                          | 5 | when the target finished (unset while active)      |
| `output_ref`  | string                             | 6 | output reference, once finished                    |
| `duration_ms` | int64                              | 7 | wall-clock duration in ms, once finished           |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Workspace

Workspace is one workspace the server holds: loading, loaded, or failed to load.

Source: [status.proto:198](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L198).

| Field              | Type                               | # | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ------------------ | ---------------------------------- | - | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `root`             | string                             | 1 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `load_time`        | Timestamp                          | 2 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `last_access_time` | Timestamp                          | 3 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `cache`            | [Cache](#cache)                    | 4 | this workspace's cache activity                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `secret_provider`  | string                             | 5 | secret\_provider is the NAME of the provider spell this workspace's magusfile selected; empty means no declaration and the built-in environment provider applies. It exists so a reader can see that credential resolution is wired up and through what - the same config visibility the cache cap gets.  The name and nothing else. No reference list, no value: magus does not store secrets, it reads them through a provider, and publishing what a build CAN reach would be a map of what to go after. |
| `state`            | [Workspace.State](#workspacestate) | 6 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `error`            | Status                             | 7 | Why the workspace is FAILED; unset in every other state. The same Status a call against it returns. Spelled out because a bare Status here resolves to this package's message.                                                                                                                                                                                                                                                                                                                              |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

## Enums

### Health

Health is the at-a-glance rollup a dashboard shows.

Source: [status.proto:16](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L16).

| Value                | # | Description                                             |
| -------------------- | - | ------------------------------------------------------- |
| `HEALTH_UNSPECIFIED` | 0 |                                                         |
| `HEALTH_HEALTHY`     | 1 | server reachable, pool nominal                          |
| `HEALTH_DEGRADED`    | 2 | reachable but something is off (pool error, saturation) |
| `HEALTH_DOWN`        | 3 | no server / pool                                        |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### TargetRun.State

State is where a target sits in its lifecycle. Values carry the STATE\_ prefix because protobuf enum values share their PARENT's scope, so an unprefixed CACHED would collide with any other enum declaring the same name in this package. STATE\_UNSPECIFIED always followed the convention; the rest did not, which buf's ENUM\_VALUE\_PREFIX rule caught once proto's lint target started running. Renaming a value leaves the wire untouched - encoding is by number, and these are unchanged.

Source: [status.proto:143](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L143).

| Value               | # | Description                        |
| ------------------- | - | ---------------------------------- |
| `STATE_UNSPECIFIED` | 0 |                                    |
| `STATE_QUEUED`      | 1 | scheduled, not yet started         |
| `STATE_RUNNING`     | 2 | a subprocess is executing          |
| `STATE_PASSED`      | 3 | finished successfully              |
| `STATE_FAILED`      | 4 | finished with an error             |
| `STATE_CACHED`      | 5 | satisfied from cache (no work run) |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).

### Workspace.State

State is where the server's copy of this workspace sits. Output only; values may be added.

Source: [status.proto:200](https://github.com/egladman/magus/blob/main/proto/magus/status/v1alpha1/status.proto#L200).

| Value               | # | Description                                                   |
| ------------------- | - | ------------------------------------------------------------- |
| `STATE_UNSPECIFIED` | 0 |                                                               |
| `STATE_LOADING`     | 1 | evaluating magusfiles; resolves on its own                    |
| `STATE_ACTIVE`      | 2 | loaded; workspace calls are served                            |
| `STATE_FAILED`      | 3 | load failed, see error; held until a workspace source changes |

Used by: [GetStatus (response)](status.md#getstatus), [StreamStatus (response)](status.md#streamstatus).


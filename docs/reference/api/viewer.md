---
title: ViewerService
description: "ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live)."
tags: [api, proto, connect, grpc, viewerservice]
---

# ViewerService

ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live). The offline URL-fragment path instead carries a whole Journal directly (no server).

Package `magus.viewer.v1alpha1`, defined in `proto/magus/viewer/v1alpha1/viewer.proto`. Part of the [daemon API](index.md).

## Methods

### GetInvocation

GetInvocation returns an invocation's header: its command, lineage, and timing - what a viewer shows on top. Selected by a ref (one target) or an invocation id (a run).

`POST /magus.viewer.v1alpha1.ViewerService/GetInvocation`: unary.

Takes `GetInvocationRequest`, returns `GetInvocationResponse`.

### ListEvents

ListEvents returns a page of an invocation's events; page through with page\_token until next\_page\_token is empty. filter narrows them server-side (large logs).

`POST /magus.viewer.v1alpha1.ViewerService/ListEvents`: unary.

Takes `ListEventsRequest`, returns `ListEventsResponse`.

### StreamEvents

StreamEvents streams a running invocation's events as they are produced. Reconnect with start\_time set to the last seen time to resume.

`POST /magus.viewer.v1alpha1.ViewerService/StreamEvents`: server streaming.

Takes `StreamEventsRequest`, returns `StreamEventsResponse`.

## Messages

### StringMatch

StringMatch is one negatable string comparison against whatever field the composing message names it for. negate inverts the match (a leading "-" in the DSL). Matching is case-insensitive; multiple matches on one field AND together.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `value` | string | 1 |  |
| `negate` | bool | 2 |  |

### TimeRange

TimeRange bounds a query to items between since and until (inclusive); either bound may be unset for an open-ended range. since doubles as a live-stream resume cursor.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `since` | Timestamp | 1 |  |
| `until` | Timestamp | 2 |  |

### Command

Command is the invoking command line and context - what was asked of magus.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `arguments` | repeated string | 1 | the full argument vector, subcommand included (e.g. ["run", "build", "api"]) |
| `cwd` | string | 3 | directory the command was invoked in |
| `trigger` | Trigger | 4 |  |

### Event

Event is one line of a structured invocation log - the atom of the stream. Most events are output or result; the first event of an invocation is KIND\_STARTED and carries the command + magus\_version (the run's identity), which every other event leaves unset.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `time` | Timestamp | 1 | when the event occurred |
| `project` | string | 2 | repo-relative project path |
| `target` | string | 3 | target name, as the CLI spells it (with charms) |
| `kind` | Kind | 4 |  |
| `stream` | Stream | 5 | output events only |
| `level` | string | 6 | info\|warn\|error, for magus events |
| `status` | Status | 7 | result events only |
| `ref` | string | 8 | target-output ref, on result events |
| `duration` | Duration | 9 | how long the target ran, on result events |
| `text` | string | 10 | output line or message (raw; may contain ANSI) |
| `command` | Command | 11 | set only on the KIND\_STARTED event |
| `magus_version` | string | 12 | set only on the KIND\_STARTED event |

### EventQuery

EventQuery filters an invocation's events server-side (for a large log). It is the viewer's OWN typed query, composed from the shared query primitives plus the viewer's event fields - log fields (target/stream/level) are not graph fields, so there is no generic shared Query. Set fields AND together; repeated values within a field OR; matching is case-insensitive. The time window (including its since resume cursor) lives here too, so one message carries the whole filter.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `projects` | repeated string | 1 | repo-relative project paths |
| `targets` | repeated string | 2 | target names |
| `kinds` | repeated string | 3 | event kinds: output\|result\|exec\|scope\|warn\|... |
| `streams` | repeated string | 4 | stdout\|stderr, for output events |
| `levels` | repeated string | 5 | info\|warn\|error |
| `status` | string | 6 | pass\|fail\|cached, for result events |
| `text` | repeated StringMatch | 7 | free-text matches against an event's text |
| `time` | TimeRange | 8 | event time window; since doubles as stream resume |

### GetInvocationRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `selector` | Selector | 1 |  |

### GetInvocationResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation` | Invocation | 1 |  |

### Invocation

Invocation is one `magus` command, launch to exit - the thing that produces a Journal of Events. It is a projection of the stream's lifecycle events (KIND\_STARTED supplies the command + start; KIND\_FINISHED supplies the end), offered as a parsed header so a viewer need not dig through the events for the command.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `command` | Command | 2 |  |
| `start_time` | Timestamp | 3 |  |
| `end_time` | Timestamp | 4 | unset while still running |
| `magus_version` | string | 5 |  |

### ListEventsRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `selector` | Selector | 1 |  |
| `page_size` | int32 | 2 |  |
| `page_token` | string | 3 |  |
| `filter` | EventQuery | 4 | viewer-typed content + time filter |

### ListEventsResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `events` | repeated Event | 1 |  |
| `next_page_token` | string | 2 | set when more events remain |

### Selector

Selector picks a run: one target's execution (ref) or a whole invocation.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `ref` | string | 1 | _one of `of`_ |
| `invocation` | string | 2 | _one of `of`_ |

### StreamEventsRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation` | string | 1 |  |
| `filter` | EventQuery | 2 | viewer-typed content filter; filter.time.since resumes the stream |

### StreamEventsResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `event` | Event | 1 |  |

## Enums

### Kind

Kind classifies an Event. Output events carry subprocess text; the rest carry magus's own structural events.

| Value | # | Description |
|-------|---|-------------|
| `KIND_UNSPECIFIED` | 0 |  |
| `KIND_STARTED` | 7 | Lifecycle events bracket the invocation: STARTED opens it (carries the command lineage + version), FINISHED closes it (carries the overall pass/fail outcome). |
| `KIND_FINISHED` | 8 |  |
| `KIND_EXEC` | 9 | Content events, produced between the lifecycle pair. a subprocess is about to run: the command line (groups the output below it) |
| `KIND_OUTPUT` | 1 | a subprocess stdout/stderr line |
| `KIND_RESULT` | 2 | a target finished (pass/fail/cached), with its ref + duration |
| `KIND_SCOPE` | 4 | the run's project scope header |
| `KIND_WARN` | 6 | a magus warning |
| `KIND_SECRET` | 10 | A credential was READ: the reference and the provider that served it, never the value. Distinct from WARN because it is not a problem - it is the record that a build reached for something privileged, which is what an audit answers for. |

### Status

Status is a result event's outcome.

| Value | # | Description |
|-------|---|-------------|
| `STATUS_UNSPECIFIED` | 0 |  |
| `STATUS_PASS` | 1 |  |
| `STATUS_FAIL` | 2 |  |
| `STATUS_CACHED` | 3 |  |

### Stream

Stream identifies which pipe an output event came from.

| Value | # | Description |
|-------|---|-------------|
| `STREAM_UNSPECIFIED` | 0 |  |
| `STREAM_STDOUT` | 1 |  |
| `STREAM_STDERR` | 2 |  |

### Trigger

Trigger is how an invocation was spawned - the lineage a viewer surfaces ("this failure came from `magus affected ci`").

| Value | # | Description |
|-------|---|-------------|
| `TRIGGER_UNSPECIFIED` | 0 |  |
| `TRIGGER_RUN` | 1 | magus run |
| `TRIGGER_AFFECTED` | 2 | magus affected |
| `TRIGGER_CI` | 3 | magus ci / affected ci |
| `TRIGGER_X` | 4 | magus x (interactive picker) |
| `TRIGGER_WATCH` | 5 | magus watch |
| `TRIGGER_DIRECT` | 6 | a directly invoked spell/op |


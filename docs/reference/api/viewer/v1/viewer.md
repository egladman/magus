---
title: ViewerService
generated_from: reference/api/
description: "ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live)."
tags: [api, proto, connect, grpc, viewerservice]
---

# ViewerService

ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live). The offline URL-fragment path instead carries a whole Journal directly (no server).

Package `magus.viewer.v1`, defined in `proto/magus/viewer/v1/viewer.proto`. Source: [viewer.proto:122](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L122). Part of the [daemon API](../../index.md).

## Methods

### GetInvocation

GetInvocation returns an invocation's header: its command, lineage, and timing - what a viewer shows on top. Selected by a ref (one target) or an invocation id (a run).

`POST /magus.viewer.v1.ViewerService/GetInvocation`: unary. Source: [viewer.proto:125](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L125).

Takes [GetInvocationRequest](#getinvocationrequest), returns [GetInvocationResponse](#getinvocationresponse).

### ListEvents

ListEvents returns a page of an invocation's events; page through with page\_token until next\_page\_token is empty. filter narrows them server-side (large logs).

`POST /magus.viewer.v1.ViewerService/ListEvents`: unary. Source: [viewer.proto:128](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L128).

Takes [ListEventsRequest](#listeventsrequest), returns [ListEventsResponse](#listeventsresponse).

### StreamEvents

StreamEvents streams a running invocation's events as they are produced. Reconnect with start\_time set to the last seen time to resume.

`POST /magus.viewer.v1.ViewerService/StreamEvents`: server streaming. Source: [viewer.proto:131](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L131).

Takes [StreamEventsRequest](#streameventsrequest), returns [StreamEventsResponse](#streameventsresponse).

## Messages

### Command

Command is the invoking command line and context - what was asked of magus.

Source: [viewer.proto:73](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L73).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `arguments` | repeated string | 1 | the full argument vector, subcommand included (e.g. ["run", "build", "api"]) |
| `cwd` | string | 3 | directory the command was invoked in |
| `trigger` | [Trigger](#trigger) | 4 |  |

_Reserved: 2._

Used by: [GetInvocation (response)](viewer.md#getinvocation), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Event

Event is one line of a structured invocation log - the atom of the stream. Most events are output or result; the first event of an invocation is KIND\_STARTED and carries the command + magus\_version (the run's identity), which every other event leaves unset.

Source: [viewer.proto:95](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L95).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `time` | Timestamp | 1 | when the event occurred |
| `project` | string | 2 | repo-relative project path |
| `target` | string | 3 | target name, as the CLI spells it (with charms) |
| `kind` | [Kind](#kind) | 4 |  |
| `stream` | [Stream](#stream) | 5 | output events only |
| `level` | string | 6 | info\|warn\|error, for magus events |
| `status` | [Status](#status) | 7 | result events only |
| `ref` | string | 8 | target-output ref, on result events |
| `duration` | Duration | 9 | how long the target ran, on result events |
| `text` | string | 10 | output line or message (raw; may contain ANSI) |
| `command` | [Command](#command) | 11 | set only on the KIND\_STARTED event |
| `magus_version` | string | 12 | set only on the KIND\_STARTED event |

Used by: [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### EventQuery

EventQuery filters an invocation's events server-side (for a large log). It is the viewer's OWN typed query, composed from the shared query primitives plus the viewer's event fields - log fields (target/stream/level) are not graph fields, so there is no generic shared Query. Set fields AND together; repeated values within a field OR; matching is case-insensitive. The time window (including its since resume cursor) lives here too, so one message carries the whole filter.

Source: [viewer.proto:156](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L156).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `projects` | repeated string | 1 | repo-relative project paths |
| `targets` | repeated string | 2 | target names |
| `kinds` | repeated string | 3 | event kinds: output\|result\|exec\|scope\|warn\|... |
| `streams` | repeated string | 4 | stdout\|stderr, for output events |
| `levels` | repeated string | 5 | info\|warn\|error |
| `status` | string | 6 | pass\|fail\|cached, for result events |
| `text` | [repeated StringMatch](../../query/v1/query.md#stringmatch) | 7 | free-text matches against an event's text |
| `time` | [TimeRange](../../query/v1/query.md#timerange) | 8 | event time window; since doubles as stream resume |

Used by: [ListEvents (request)](viewer.md#listevents), [StreamEvents (request)](viewer.md#streamevents).

### GetInvocationRequest

Source: [viewer.proto:143](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L143).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `selector` | [Selector](#selector) | 1 | _required_ |

Used by: [GetInvocation (request)](viewer.md#getinvocation).

### GetInvocationResponse

Source: [viewer.proto:146](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L146).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation` | [Invocation](#invocation) | 1 |  |

Used by: [GetInvocation (response)](viewer.md#getinvocation).

### Invocation

Invocation is one `magus` command, launch to exit - the thing that produces a Journal of Events. It is a projection of the stream's lifecycle events (KIND\_STARTED supplies the command + start; KIND\_FINISHED supplies the end), offered as a parsed header so a viewer need not dig through the events for the command.

Source: [viewer.proto:84](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L84).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `command` | [Command](#command) | 2 |  |
| `start_time` | Timestamp | 3 |  |
| `end_time` | Timestamp | 4 | unset while still running |
| `magus_version` | string | 5 |  |

Used by: [GetInvocation (response)](viewer.md#getinvocation).

### ListEventsRequest

Source: [viewer.proto:167](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L167).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `selector` | [Selector](#selector) | 1 | _required_ |
| `page_size` | int32 | 2 | _int32.lte: 5000; int32.gte: 0_ |
| `page_token` | string | 3 |  |
| `filter` | [EventQuery](#eventquery) | 4 | viewer-typed content + time filter |

Used by: [ListEvents (request)](viewer.md#listevents).

### ListEventsResponse

Source: [viewer.proto:173](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L173).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `events` | [repeated Event](#event) | 1 |  |
| `next_page_token` | string | 2 | set when more events remain |

Used by: [ListEvents (response)](viewer.md#listevents).

### Selector

Selector picks a run: one target's execution (ref) or a whole invocation.

Source: [viewer.proto:135](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L135).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `ref` | string | 1 | _one of `of`, required_ _string.pattern: `^out[0-9a-f]+$`_ |
| `invocation` | string | 2 | _one of `of`, required_ _string.pattern: `^inv[0-9a-z]+$`_ |

Used by: [GetInvocation (request)](viewer.md#getinvocation), [ListEvents (request)](viewer.md#listevents).

### StreamEventsRequest

Source: [viewer.proto:178](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L178).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation` | string | 1 | _string.pattern: `^inv[0-9a-z]+$`_ |
| `filter` | [EventQuery](#eventquery) | 2 | viewer-typed content filter; filter.time.since resumes the stream |

Used by: [StreamEvents (request)](viewer.md#streamevents).

### StreamEventsResponse

Source: [viewer.proto:182](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L182).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `event` | [Event](#event) | 1 |  |

Used by: [StreamEvents (response)](viewer.md#streamevents).

## Enums

### Kind

Kind classifies an Event. Output events carry subprocess text; the rest carry magus's own structural events.

Source: [viewer.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L22).

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

_Reserved: 3, 5._

Used by: [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Status

Status is a result event's outcome.

Source: [viewer.proto:53](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L53).

| Value | # | Description |
|-------|---|-------------|
| `STATUS_UNSPECIFIED` | 0 |  |
| `STATUS_PASS` | 1 |  |
| `STATUS_FAIL` | 2 |  |
| `STATUS_CACHED` | 3 |  |

Used by: [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Stream

Stream identifies which pipe an output event came from.

Source: [viewer.proto:46](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L46).

| Value | # | Description |
|-------|---|-------------|
| `STREAM_UNSPECIFIED` | 0 |  |
| `STREAM_STDOUT` | 1 |  |
| `STREAM_STDERR` | 2 |  |

Used by: [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Trigger

Trigger is how an invocation was spawned - the lineage a viewer surfaces ("this failure came from `magus affected ci`").

Source: [viewer.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1/viewer.proto#L62).

| Value | # | Description |
|-------|---|-------------|
| `TRIGGER_UNSPECIFIED` | 0 |  |
| `TRIGGER_RUN` | 1 | magus run |
| `TRIGGER_AFFECTED` | 2 | magus affected |
| `TRIGGER_CI` | 3 | magus ci / affected ci |
| `TRIGGER_X` | 4 | magus x (interactive picker) |
| `TRIGGER_WATCH` | 5 | magus watch |
| `TRIGGER_DIRECT` | 6 | a directly invoked spell/op |

Used by: [GetInvocation (response)](viewer.md#getinvocation), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).


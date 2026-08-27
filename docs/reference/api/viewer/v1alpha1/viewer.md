---
title: ViewerService
generated_from: reference/api/
description: "ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live)."
tags: [api, proto, connect, grpc, viewerservice]
---

# ViewerService

ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live). The offline URL-fragment path instead carries a whole Journal directly (no server).

Package `magus.viewer.v1alpha1`, defined in `proto/magus/viewer/v1alpha1/viewer.proto`. Source: [viewer.proto:122](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L122). Part of the [daemon API](../../index.md).

## Methods

### GetInvocation

GetInvocation returns an invocation's header: its command, lineage, and timing - what a viewer shows on top. Selected by a ref (one target) or an invocation id (a run).

`POST /magus.viewer.v1alpha1.ViewerService/GetInvocation`: unary. Source: [viewer.proto:125](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L125).

Takes [GetInvocationRequest](#getinvocationrequest), returns [Invocation](#invocation).

### ListEvents

ListEvents returns a page of an invocation's events; page through with page\_token until next\_page\_token is empty. filter narrows them server-side (large logs).

`POST /magus.viewer.v1alpha1.ViewerService/ListEvents`: unary. Source: [viewer.proto:128](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L128).

Takes [ListEventsRequest](#listeventsrequest), returns [ListEventsResponse](#listeventsresponse).

### StreamEvents

StreamEvents streams a running invocation's events as they are produced. Reconnect with start\_time set to the last seen time to resume.

`POST /magus.viewer.v1alpha1.ViewerService/StreamEvents`: server streaming. Source: [viewer.proto:131](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L131).

Takes [StreamEventsRequest](#streameventsrequest), returns [StreamEventsResponse](#streameventsresponse).

### ListOutputs

ListOutputs returns the stored runs' descriptors, newest first, so a viewer can browse recent runs grouped project -> target -> run.

`POST /magus.viewer.v1alpha1.ViewerService/ListOutputs`: unary. Source: [viewer.proto:134](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L134).

Takes [ListOutputsRequest](#listoutputsrequest), returns [ListOutputsResponse](#listoutputsresponse).

### GetOutput

GetOutput returns one stored run's captured output VERBATIM - the bytes the subprocess wrote, unparsed and unstyled. Bytes rather than string: a captured log is whatever the tool emitted, which is not guaranteed to be valid UTF-8.

`POST /magus.viewer.v1alpha1.ViewerService/GetOutput`: unary. Source: [viewer.proto:138](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L138).

Takes [GetOutputRequest](#getoutputrequest), returns [GetOutputResponse](#getoutputresponse).

### ListInvocations

ListInvocations returns the retained run journals by the command that produced them. The run browser's other axis: ListOutputs is per target, this is per `magus` command.

`POST /magus.viewer.v1alpha1.ViewerService/ListInvocations`: unary. Source: [viewer.proto:141](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L141).

Takes [ListInvocationsRequest](#listinvocationsrequest), returns [ListInvocationsResponse](#listinvocationsresponse).

### GetJournal

GetJournal returns one past invocation whole - header plus every event - which is the same message the offline `#data=` URL fragment carries, so a browsed run and a shared one render from identical bytes.

`POST /magus.viewer.v1alpha1.ViewerService/GetJournal`: unary. Source: [viewer.proto:145](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L145).

Takes [GetJournalRequest](#getjournalrequest), returns [Journal](#journal).

## Messages

### Command

Command is the invoking command line and context - what was asked of magus.

Source: [viewer.proto:73](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L73).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `arguments` | repeated string | 1 | the full argument vector, subcommand included (e.g. ["run", "build", "api"]) |
| `cwd` | string | 3 | directory the command was invoked in |
| `trigger` | [Trigger](#trigger) | 4 |  |

_Reserved: 2._

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [ListInvocations (response)](viewer.md#listinvocations), [StreamEvents (response)](viewer.md#streamevents).

### Event

Event is one line of a structured invocation log - the atom of the stream. Most events are output or result; the first event of an invocation is KIND\_STARTED and carries the command + magus\_version (the run's identity), which every other event leaves unset.

Source: [viewer.proto:95](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L95).

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

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### EventQuery

EventQuery filters an invocation's events server-side (for a large log). It is the viewer's OWN typed query, composed from the shared query primitives plus the viewer's event fields - log fields (target/stream/level) are not graph fields, so there is no generic shared Query. Set fields AND together; repeated values within a field OR; matching is case-insensitive. The time window (including its since resume cursor) lives here too, so one message carries the whole filter.

Source: [viewer.proto:162](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L162).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `projects` | repeated string | 1 | repo-relative project paths |
| `targets` | repeated string | 2 | target names |
| `kinds` | repeated string | 3 | event kinds: output\|result\|exec\|scope\|warn\|... |
| `streams` | repeated string | 4 | stdout\|stderr, for output events |
| `levels` | repeated string | 5 | info\|warn\|error |
| `status` | string | 6 | pass\|fail\|cached, for result events |
| `text` | [repeated StringMatch](../../query/v1alpha1/query.md#stringmatch) | 7 | free-text matches against an event's text |
| `time` | [TimeRange](../../query/v1alpha1/query.md#timerange) | 8 | event time window; since doubles as stream resume |

Used by: [ListEvents (request)](viewer.md#listevents), [StreamEvents (request)](viewer.md#streamevents).

### GetInvocationRequest

Source: [viewer.proto:148](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L148).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | _string.pattern: `^(out[0-9a-f]+\|inv[0-9a-z]+)$`_ A run's resource name: an output ref ("out<hex>") or an invocation id ("inv<base36>"). One field rather than a oneof because the two patterns are disjoint, so a single string still identifies exactly one run - and this service spelled the same identity three ways before (a oneof here, the same oneof on ListEvents, a bare string on StreamEvents). |

Used by: [GetInvocation (request)](viewer.md#getinvocation).

### GetJournalRequest

Source: [viewer.proto:238](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L238).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | _string.pattern: `^(out[0-9a-f]+\|inv[0-9a-z]+)$`_ The run to fetch whole: an output ref or an invocation id, the same identity GetInvocation takes. |

Used by: [GetJournal (request)](viewer.md#getjournal).

### GetOutputRequest

Source: [viewer.proto:220](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L220).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | _string.pattern: `^out[0-9a-f]+$`_ The output to read, by its ref. |

Used by: [GetOutput (request)](viewer.md#getoutput).

### GetOutputResponse

Source: [viewer.proto:224](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L224).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `body` | bytes | 1 | The captured bytes, exactly as the subprocess wrote them. |

Used by: [GetOutput (response)](viewer.md#getoutput).

### Invocation

Invocation is one `magus` command, launch to exit - the thing that produces a Journal of Events. It is a projection of the stream's lifecycle events (KIND\_STARTED supplies the command + start; KIND\_FINISHED supplies the end), offered as a parsed header so a viewer need not dig through the events for the command.

Source: [viewer.proto:84](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L84).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `command` | [Command](#command) | 2 |  |
| `start_time` | Timestamp | 3 |  |
| `end_time` | Timestamp | 4 | unset while still running |
| `magus_version` | string | 5 |  |

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListInvocations (response)](viewer.md#listinvocations).

### Journal

Journal bundles an invocation header with its events - the whole thing for the offline URL fragment, or a page of events from ListEvents.

Source: [viewer.proto:113](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L113).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocation` | [Invocation](#invocation) | 1 |  |
| `events` | [repeated Event](#event) | 2 |  |

Used by: [GetJournal (response)](viewer.md#getjournal).

### ListEventsRequest

Source: [viewer.proto:173](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L173).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `parent` | string | 1 | _string.pattern: `^(out[0-9a-f]+\|inv[0-9a-z]+)$`_ The run that owns these events - the collection's parent, per AIP-132. |
| `page_size` | int32 | 2 | _int32.lte: 5000; int32.gte: 0_ |
| `page_token` | string | 3 |  |
| `filter` | [EventQuery](#eventquery) | 4 | viewer-typed content + time filter |

Used by: [ListEvents (request)](viewer.md#listevents).

### ListEventsResponse

Source: [viewer.proto:180](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L180).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `events` | [repeated Event](#event) | 1 |  |
| `next_page_token` | string | 2 | set when more events remain |

Used by: [ListEvents (response)](viewer.md#listevents).

### ListInvocationsRequest

Source: [viewer.proto:229](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L229).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | _int32.lte: 5000; int32.gte: 0_ |
| `page_token` | string | 2 |  |

Used by: [ListInvocations (request)](viewer.md#listinvocations).

### ListInvocationsResponse

Source: [viewer.proto:233](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L233).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `invocations` | [repeated Invocation](#invocation) | 1 |  |
| `next_page_token` | string | 2 | set when more invocations remain |

Used by: [ListInvocations (response)](viewer.md#listinvocations).

### ListOutputsRequest

Source: [viewer.proto:211](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L211).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | _int32.lte: 5000; int32.gte: 0_ |
| `page_token` | string | 2 |  |

Used by: [ListOutputs (request)](viewer.md#listoutputs).

### ListOutputsResponse

Source: [viewer.proto:215](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L215).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `outputs` | [repeated Output](#output) | 1 |  |
| `next_page_token` | string | 2 | set when more outputs remain |

Used by: [ListOutputs (response)](viewer.md#listoutputs).

### Output

Output is one stored run's descriptor: what it was, how it went, and the ref that fetches its captured bytes. The wire twin of cache.OutputDescriptor.

Source: [viewer.proto:197](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L197).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `ref` | string | 1 | The key-derived portable id shared by every attempt of the step. |
| `project` | string | 2 |  |
| `target` | string | 3 |  |
| `invocation` | string | 4 | The invocation that produced this output, empty when the run predates journalling. |
| `failed` | bool | 5 |  |
| `error` | string | 6 | Failure message; empty on success. |
| `create_time` | Timestamp | 7 |  |
| `duration` | Duration | 8 |  |

Used by: [ListOutputs (response)](viewer.md#listoutputs).

### StreamEventsRequest

Source: [viewer.proto:185](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L185).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `parent` | string | 1 | _string.pattern: `^inv[0-9a-z]+$`_ The invocation whose events stream. Named parent to match ListEvents; only a whole invocation streams, so this one does not take an output ref. |
| `filter` | [EventQuery](#eventquery) | 2 | viewer-typed content filter; filter.time.since resumes the stream |

Used by: [StreamEvents (request)](viewer.md#streamevents).

### StreamEventsResponse

Source: [viewer.proto:191](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L191).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `event` | [Event](#event) | 1 |  |

Used by: [StreamEvents (response)](viewer.md#streamevents).

## Enums

### Kind

Kind classifies an Event. Output events carry subprocess text; the rest carry magus's own structural events.

Source: [viewer.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L22).

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

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Status

Status is a result event's outcome.

Source: [viewer.proto:53](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L53).

| Value | # | Description |
|-------|---|-------------|
| `STATUS_UNSPECIFIED` | 0 |  |
| `STATUS_PASS` | 1 |  |
| `STATUS_FAIL` | 2 |  |
| `STATUS_CACHED` | 3 |  |

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Stream

Stream identifies which pipe an output event came from.

Source: [viewer.proto:46](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L46).

| Value | # | Description |
|-------|---|-------------|
| `STREAM_UNSPECIFIED` | 0 |  |
| `STREAM_STDOUT` | 1 |  |
| `STREAM_STDERR` | 2 |  |

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Trigger

Trigger is how an invocation was spawned - the lineage a viewer surfaces ("this failure came from `magus affected ci`").

Source: [viewer.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L62).

| Value | # | Description |
|-------|---|-------------|
| `TRIGGER_UNSPECIFIED` | 0 |  |
| `TRIGGER_RUN` | 1 | magus run |
| `TRIGGER_AFFECTED` | 2 | magus affected |
| `TRIGGER_CI` | 3 | magus ci / affected ci |
| `TRIGGER_X` | 4 | magus x (interactive picker) |
| `TRIGGER_WATCH` | 5 | magus watch |
| `TRIGGER_DIRECT` | 6 | a directly invoked spell/op |

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [ListInvocations (response)](viewer.md#listinvocations), [StreamEvents (response)](viewer.md#streamevents).


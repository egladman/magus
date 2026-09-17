---
title: ViewerService
generated_from: reference/api/
description: "ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live)."
tags: [api, proto, connect, grpc, viewerservice]
---

# ViewerService

ViewerService serves an invocation's captured output to a log viewer, resource-oriented per AIP: Get the Invocation (the run header), List its Events (paginated), Stream them (live). The offline URL-fragment path instead carries a whole Journal directly (no server).

Package `magus.viewer.v1alpha1`, defined in `proto/magus/viewer/v1alpha1/viewer.proto`. Source: [viewer.proto:148](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L148). Part of the [daemon API](../../index.md).

## Methods

### GetInvocation

GetInvocation returns an invocation's header: its command, lineage, and timing - what a viewer shows on top. Selected by a ref (one target) or an invocation id (a run).

`POST /magus.viewer.v1alpha1.ViewerService/GetInvocation`: unary. Source: [viewer.proto:151](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L151).

Takes [GetInvocationRequest](#getinvocationrequest), returns [Invocation](#invocation).

### ListEvents

ListEvents returns a page of an invocation's events; page through with page\_token until next\_page\_token is empty. filter narrows them server-side (large logs).

`POST /magus.viewer.v1alpha1.ViewerService/ListEvents`: unary. Source: [viewer.proto:154](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L154).

Takes [ListEventsRequest](#listeventsrequest), returns [ListEventsResponse](#listeventsresponse).

### StreamEvents

StreamEvents replays an invocation's stored events, then streams what the run appends as it is produced, ending when the run finishes. Reconnect with filter.time.since set to the last event seen to resume; that boundary is inclusive, so the event resumed from arrives again rather than being lost.

`POST /magus.viewer.v1alpha1.ViewerService/StreamEvents`: server streaming. Source: [viewer.proto:159](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L159).

Takes [StreamEventsRequest](#streameventsrequest), returns [StreamEventsResponse](#streameventsresponse).

### ListOutputs

ListOutputs returns the stored runs' descriptors, newest first, so a viewer can browse recent runs grouped project -> target -> run.

`POST /magus.viewer.v1alpha1.ViewerService/ListOutputs`: unary. Source: [viewer.proto:162](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L162).

Takes [ListOutputsRequest](#listoutputsrequest), returns [ListOutputsResponse](#listoutputsresponse).

### GetOutput

GetOutput returns one stored run's captured output VERBATIM - the bytes the subprocess wrote, unparsed and unstyled. Bytes rather than string: a captured log is whatever the tool emitted, which is not guaranteed to be valid UTF-8.

`POST /magus.viewer.v1alpha1.ViewerService/GetOutput`: unary. Source: [viewer.proto:166](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L166).

Takes [GetOutputRequest](#getoutputrequest), returns [GetOutputResponse](#getoutputresponse).

### ListInvocations

ListInvocations returns the retained run journals by the command that produced them. The run browser's other axis: ListOutputs is per target, this is per `magus` command.

`POST /magus.viewer.v1alpha1.ViewerService/ListInvocations`: unary. Source: [viewer.proto:169](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L169).

Takes [ListInvocationsRequest](#listinvocationsrequest), returns [ListInvocationsResponse](#listinvocationsresponse).

### GetJournal

GetJournal returns one past invocation whole - header plus every event - which is the same message the offline `#data=` URL fragment carries, so a browsed run and a shared one render from identical bytes.

`POST /magus.viewer.v1alpha1.ViewerService/GetJournal`: unary. Source: [viewer.proto:173](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L173).

Takes [GetJournalRequest](#getjournalrequest), returns [Journal](#journal).

### GetSessionActivity

GetSessionActivity returns what one loaded agent session did in the run-up to its last write of one path: a bounded window of turns, never the whole session. Loopback peers only, unlike the rest of this service: a session's record names every path and skill it reached, which a share link has no business reading.

`POST /magus.viewer.v1alpha1.ViewerService/GetSessionActivity`: unary. Source: [viewer.proto:178](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L178).

Takes [GetSessionActivityRequest](#getsessionactivityrequest), returns [SessionActivity](#sessionactivity).

## Messages

### Command

Command is the invoking command line and context - what was asked of magus.

Source: [viewer.proto:73](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L73).

| Field       | Type                | # | Description                                                                  |
| ----------- | ------------------- | - | ---------------------------------------------------------------------------- |
| `arguments` | repeated string     | 1 | the full argument vector, subcommand included (e.g. ["run", "build", "api"]) |
| `cwd`       | string              | 3 | directory the command was invoked in                                         |
| `trigger`   | [Trigger](#trigger) | 4 |                                                                              |

_Reserved: 2._

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [ListInvocations (response)](viewer.md#listinvocations), [StreamEvents (response)](viewer.md#streamevents).

### Event

Source: [viewer.proto:115](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L115).

| Field           | Type                                       | #  | Description                                                                                                                                                                                                                                               |
| --------------- | ------------------------------------------ | -- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `time`          | Timestamp                                  | 1  | when the event occurred                                                                                                                                                                                                                                   |
| `project`       | string                                     | 2  | repo-relative project path                                                                                                                                                                                                                                |
| `target`        | string                                     | 3  | target name, as the CLI spells it (with charms)                                                                                                                                                                                                           |
| `kind`          | [Kind](#kind)                              | 4  |                                                                                                                                                                                                                                                           |
| `stream`        | [Stream](#stream)                          | 5  | output events only                                                                                                                                                                                                                                        |
| `level`         | string                                     | 6  | info\|warn\|error, for magus events                                                                                                                                                                                                                       |
| `status`        | [Status](#status)                          | 7  | result events only                                                                                                                                                                                                                                        |
| `ref`           | string                                     | 8  | target-output ref, on result events                                                                                                                                                                                                                       |
| `duration`      | Duration                                   | 9  | how long the target ran, on result events                                                                                                                                                                                                                 |
| `text`          | string                                     | 10 | output line or message (raw; may contain ANSI)                                                                                                                                                                                                            |
| `command`       | [Command](#command)                        | 11 | set only on the KIND\_STARTED event                                                                                                                                                                                                                       |
| `magus_version` | string                                     | 12 | set only on the KIND\_STARTED event                                                                                                                                                                                                                       |
| `undeclared`    | [repeated UndeclaredSeed](#undeclaredseed) | 13 | Set only on a KIND\_SCOPE event carrying no target: the projects this run selected on files nothing declares. It rides the run's own stream because it is a fact about this run's scope, and the readers that want it are already consuming these frames. |

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### EventQuery

EventQuery filters an invocation's events server-side (for a large log). It is the viewer's OWN typed query, composed from the shared query primitives plus the viewer's event fields - log fields (target/stream/level) are not graph fields, so there is no generic shared Query. Set fields AND together; repeated values within a field OR; matching is case-insensitive. The time window (including its since resume cursor) lives here too, so one message carries the whole filter.

Source: [viewer.proto:195](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L195).

| Field      | Type                                                              | # | Description                                         |
| ---------- | ----------------------------------------------------------------- | - | --------------------------------------------------- |
| `projects` | repeated string                                                   | 1 | repo-relative project paths                         |
| `targets`  | repeated string                                                   | 2 | target names                                        |
| `kinds`    | repeated string                                                   | 3 | event kinds: output\|result\|exec\|scope\|warn\|... |
| `streams`  | repeated string                                                   | 4 | stdout\|stderr, for output events                   |
| `levels`   | repeated string                                                   | 5 | info\|warn\|error                                   |
| `status`   | string                                                            | 6 | pass\|fail\|cached, for result events               |
| `text`     | [repeated StringMatch](../../query/v1alpha1/query.md#stringmatch) | 7 | free-text matches against an event's text           |
| `time`     | [TimeRange](../../query/v1alpha1/query.md#timerange)              | 8 | event time window; since doubles as stream resume   |

Used by: [ListEvents (request)](viewer.md#listevents), [StreamEvents (request)](viewer.md#streamevents).

### GetInvocationRequest

Source: [viewer.proto:181](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L181).

| Field  | Type   | # | Description                                                                                                                                                                                                                                                                                                                                                                                                    |
| ------ | ------ | - | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name` | string | 1 | _string.pattern: `^(out[0-9a-f]+\|inv[0-9a-z]+)$`_ A run's resource name: an output ref ("out<hex>") or an invocation id ("inv<base36>"). One field rather than a oneof because the two patterns are disjoint, so a single string still identifies exactly one run - and this service spelled the same identity three ways before (a oneof here, the same oneof on ListEvents, a bare string on StreamEvents). |

Used by: [GetInvocation (request)](viewer.md#getinvocation).

### GetJournalRequest

Source: [viewer.proto:271](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L271).

| Field  | Type   | # | Description                                                                                                                                          |
| ------ | ------ | - | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name` | string | 1 | _string.pattern: `^(out[0-9a-f]+\|inv[0-9a-z]+)$`_ The run to fetch whole: an output ref or an invocation id, the same identity GetInvocation takes. |

Used by: [GetJournal (request)](viewer.md#getjournal).

### GetOutputRequest

Source: [viewer.proto:253](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L253).

| Field  | Type   | # | Description                                                        |
| ------ | ------ | - | ------------------------------------------------------------------ |
| `name` | string | 1 | _string.pattern: `^out[0-9a-f]+$`_ The output to read, by its ref. |

Used by: [GetOutput (request)](viewer.md#getoutput).

### GetOutputResponse

Source: [viewer.proto:257](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L257).

| Field  | Type  | # | Description                                               |
| ------ | ----- | - | --------------------------------------------------------- |
| `body` | bytes | 1 | The captured bytes, exactly as the subprocess wrote them. |

Used by: [GetOutput (response)](viewer.md#getoutput).

### GetSessionActivityRequest

Source: [viewer.proto:277](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L277).

| Field     | Type   | # | Description                                                                                                                     |
| --------- | ------ | - | ------------------------------------------------------------------------------------------------------------------------------- |
| `session` | string | 1 | _string.max_len: 256; string.pattern: `^[A-Za-z0-9][A-Za-z0-9_-]*$`_ The host's own session id, as a review's touch carries it. |
| `path`    | string | 2 | _string.min_len: 1; string.max_len: 4096_ The checkout-relative path whose last write ends the window.                          |

Used by: [GetSessionActivity (request)](viewer.md#getsessionactivity).

### Invocation

Invocation is one `magus` command, launch to exit - the thing that produces a Journal of Events. It is a projection of the stream's lifecycle events (KIND\_STARTED supplies the command + start; KIND\_FINISHED supplies the end), offered as a parsed header so a viewer need not dig through the events for the command.

Source: [viewer.proto:84](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L84).

| Field           | Type                | # | Description                                                                                                                                                                                                                          |
| --------------- | ------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `id`            | string              | 1 |                                                                                                                                                                                                                                      |
| `command`       | [Command](#command) | 2 |                                                                                                                                                                                                                                      |
| `start_time`    | Timestamp           | 3 |                                                                                                                                                                                                                                      |
| `end_time`      | Timestamp           | 4 | unset while still running                                                                                                                                                                                                            |
| `magus_version` | string              | 5 |                                                                                                                                                                                                                                      |
| `status`        | [Status](#status)   | 6 | Outcome, when the run reached one. The events carry this too (KIND\_FINISHED), but a listing reads run HEADERS without opening any journal, so a browser that had to decide pass from fail would otherwise open every file it lists. |
| `size_bytes`    | int64               | 7 | On-disk size of this run's journal. What a retention view needs to say which runs are worth keeping, and the same reason as above: available from the header alone.                                                                  |

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListInvocations (response)](viewer.md#listinvocations).

### Journal

Journal bundles an invocation header with its events - the whole thing for the offline URL fragment, or a page of events from ListEvents.

Source: [viewer.proto:139](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L139).

| Field        | Type                      | # | Description |
| ------------ | ------------------------- | - | ----------- |
| `invocation` | [Invocation](#invocation) | 1 |             |
| `events`     | [repeated Event](#event)  | 2 |             |

Used by: [GetJournal (response)](viewer.md#getjournal).

### ListEventsRequest

Source: [viewer.proto:206](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L206).

| Field        | Type                      | # | Description                                                                                                               |
| ------------ | ------------------------- | - | ------------------------------------------------------------------------------------------------------------------------- |
| `parent`     | string                    | 1 | _string.pattern: `^(out[0-9a-f]+\|inv[0-9a-z]+)$`_ The run that owns these events - the collection's parent, per AIP-132. |
| `page_size`  | int32                     | 2 | _int32.lte: 5000; int32.gte: 0_                                                                                           |
| `page_token` | string                    | 3 |                                                                                                                           |
| `filter`     | [EventQuery](#eventquery) | 4 | viewer-typed content + time filter                                                                                        |

Used by: [ListEvents (request)](viewer.md#listevents).

### ListEventsResponse

Source: [viewer.proto:213](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L213).

| Field             | Type                     | # | Description                 |
| ----------------- | ------------------------ | - | --------------------------- |
| `events`          | [repeated Event](#event) | 1 |                             |
| `next_page_token` | string                   | 2 | set when more events remain |

Used by: [ListEvents (response)](viewer.md#listevents).

### ListInvocationsRequest

Source: [viewer.proto:262](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L262).

| Field        | Type   | # | Description                     |
| ------------ | ------ | - | ------------------------------- |
| `page_size`  | int32  | 1 | _int32.lte: 5000; int32.gte: 0_ |
| `page_token` | string | 2 |                                 |

Used by: [ListInvocations (request)](viewer.md#listinvocations).

### ListInvocationsResponse

Source: [viewer.proto:266](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L266).

| Field             | Type                               | # | Description                      |
| ----------------- | ---------------------------------- | - | -------------------------------- |
| `invocations`     | [repeated Invocation](#invocation) | 1 |                                  |
| `next_page_token` | string                             | 2 | set when more invocations remain |

Used by: [ListInvocations (response)](viewer.md#listinvocations).

### ListOutputsRequest

Source: [viewer.proto:244](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L244).

| Field        | Type   | # | Description                     |
| ------------ | ------ | - | ------------------------------- |
| `page_size`  | int32  | 1 | _int32.lte: 5000; int32.gte: 0_ |
| `page_token` | string | 2 |                                 |

Used by: [ListOutputs (request)](viewer.md#listoutputs).

### ListOutputsResponse

Source: [viewer.proto:248](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L248).

| Field             | Type                       | # | Description                  |
| ----------------- | -------------------------- | - | ---------------------------- |
| `outputs`         | [repeated Output](#output) | 1 |                              |
| `next_page_token` | string                     | 2 | set when more outputs remain |

Used by: [ListOutputs (response)](viewer.md#listoutputs).

### Output

Output is one stored run's descriptor: what it was, how it went, and the ref that fetches its captured bytes. The wire twin of cache.OutputDescriptor.

Source: [viewer.proto:230](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L230).

| Field         | Type      | # | Description                                                                        |
| ------------- | --------- | - | ---------------------------------------------------------------------------------- |
| `ref`         | string    | 1 | The key-derived portable id shared by every attempt of the step.                   |
| `project`     | string    | 2 |                                                                                    |
| `target`      | string    | 3 |                                                                                    |
| `invocation`  | string    | 4 | The invocation that produced this output, empty when the run predates journalling. |
| `failed`      | bool      | 5 |                                                                                    |
| `error`       | string    | 6 | Failure message; empty on success.                                                 |
| `create_time` | Timestamp | 7 |                                                                                    |
| `duration`    | Duration  | 8 |                                                                                    |

Used by: [ListOutputs (response)](viewer.md#listoutputs).

### SessionActivity

Source: [viewer.proto:328](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L328).

| Field        | Type                                 | # | Description                                                                                                                                                               |
| ------------ | ------------------------------------ | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `session`    | string                               | 1 |                                                                                                                                                                           |
| `host`       | string                               | 2 |                                                                                                                                                                           |
| `transcript` | string                               | 3 | A pointer to the host's own log. The daemon does not open it.                                                                                                             |
| `wrote`      | bool                                 | 4 | Whether the loaded record holds a write of the requested path. False with no turns means the session was seen only by the guard hook, or its transcript was never loaded. |
| `turns`      | [repeated SessionTurn](#sessionturn) | 5 |                                                                                                                                                                           |
| `truncated`  | bool                                 | 6 | Set when the window held more turns than were returned; the oldest were dropped.                                                                                          |
| `unrecorded` | [repeated Unrecorded](#unrecorded)   | 7 |                                                                                                                                                                           |

Used by: [GetSessionActivity (response)](viewer.md#getsessionactivity).

### SessionTurn

SessionTurn is one entry of a session's record, oldest first within its window.

Source: [viewer.proto:300](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L300).

| Field         | Type                  | #  | Description                                                                                                                                                  |
| ------------- | --------------------- | -- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `role`        | [TurnRole](#turnrole) | 1  |                                                                                                                                                              |
| `kind`        | string                | 2  | For a tool turn, the session event kind: shell.command, file.read, file.write, skill.load, hook.output, spawn or magus.call.                                 |
| `text`        | string                | 3  | A path, skill, subagent type or tool name. Never a shell command's text, which the store does not keep, and empty for hook.output, whose text can quote one. |
| `program`     | string                | 4  | A shell command's program, arguments dropped.                                                                                                                |
| `time`        | Timestamp             | 5  |                                                                                                                                                              |
| `verdict`     | string                | 6  | What today's guard rules say about a shell command: pass, advise or deny.                                                                                    |
| `rule`        | string                | 7  |                                                                                                                                                              |
| `exit`        | int32                 | 8  | Zero is both success and a host that records no exit status; the store keeps no distinction, so only a non-zero value is a measurement.                      |
| `denied`      | bool                  | 9  |                                                                                                                                                              |
| `interrupted` | bool                  | 10 |                                                                                                                                                              |

Used by: [GetSessionActivity (response)](viewer.md#getsessionactivity).

### StreamEventsRequest

Source: [viewer.proto:218](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L218).

| Field    | Type                      | # | Description                                                                                                                                                                        |
| -------- | ------------------------- | - | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `parent` | string                    | 1 | _string.pattern: `^inv[0-9a-z]+$`_ The invocation whose events stream. Named parent to match ListEvents; only a whole invocation streams, so this one does not take an output ref. |
| `filter` | [EventQuery](#eventquery) | 2 | viewer-typed content filter; filter.time.since resumes the stream                                                                                                                  |

Used by: [StreamEvents (request)](viewer.md#streamevents).

### StreamEventsResponse

Source: [viewer.proto:224](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L224).

| Field   | Type            | # | Description |
| ------- | --------------- | - | ----------- |
| `event` | [Event](#event) | 1 |             |

Used by: [StreamEvents (response)](viewer.md#streamevents).

### UndeclaredSeed

Event is one line of a structured invocation log - the atom of the stream. Most events are output or result; the first event of an invocation is KIND\_STARTED and carries the command + magus\_version (the run's identity), which every other event leaves unset. UndeclaredSeed is one project a run selected on changed files that no project declares (MGS1028): directory containment chose it, so the targets it reran could not have answered differently. Rides KIND\_SCOPE.

Source: [viewer.proto:105](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L105).

| Field     | Type            | # | Description                                                                                                                                                                                                                                                                                                               |
| --------- | --------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `project` | string          | 1 | repo-relative project path                                                                                                                                                                                                                                                                                                |
| `files`   | repeated string | 2 | the undeclared files that selected it                                                                                                                                                                                                                                                                                     |
| `inputs`  | repeated string | 3 | inputs is the subset of files that read as build INPUTS (a dependency lock, a linter rule set, a toolchain pin). It is the half that changes what a verdict means: a target selected anyway can still replay an answer computed under the rules the edit just replaced, because the file that replaced them keys nothing. |

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Unrecorded

Unrecorded names a part of the transcript this answer cannot carry, and why. A part listed here is unobservable, which a reader must not mistake for a session that said nothing.

Source: [viewer.proto:323](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L323).

| Field    | Type                  | # | Description |
| -------- | --------------------- | - | ----------- |
| `role`   | [TurnRole](#turnrole) | 1 |             |
| `reason` | string                | 2 |             |

Used by: [GetSessionActivity (response)](viewer.md#getsessionactivity).

## Enums

### Kind

Kind classifies an Event. Output events carry subprocess text; the rest carry magus's own structural events.

Source: [viewer.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L22).

| Value              | #  | Description                                                                                                                                                                                                                                 |
| ------------------ | -- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `KIND_UNSPECIFIED` | 0  |                                                                                                                                                                                                                                             |
| `KIND_STARTED`     | 7  | Lifecycle events bracket the invocation: STARTED opens it (carries the command lineage + version), FINISHED closes it (carries the overall pass/fail outcome).                                                                              |
| `KIND_FINISHED`    | 8  |                                                                                                                                                                                                                                             |
| `KIND_EXEC`        | 9  | Content events, produced between the lifecycle pair. a subprocess is about to run: the command line (groups the output below it)                                                                                                            |
| `KIND_OUTPUT`      | 1  | a subprocess stdout/stderr line                                                                                                                                                                                                             |
| `KIND_RESULT`      | 2  | a target finished (pass/fail/cached), with its ref + duration                                                                                                                                                                               |
| `KIND_SCOPE`       | 4  | the run's project scope header                                                                                                                                                                                                              |
| `KIND_WARN`        | 6  | a magus warning                                                                                                                                                                                                                             |
| `KIND_SECRET`      | 10 | A credential was READ: the reference and the provider that served it, never the value. Distinct from WARN because it is not a problem - it is the record that a build reached for something privileged, which is what an audit answers for. |

_Reserved: 3, 5._

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Status

Status is a result event's outcome.

Source: [viewer.proto:53](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L53).

| Value                | # | Description |
| -------------------- | - | ----------- |
| `STATUS_UNSPECIFIED` | 0 |             |
| `STATUS_PASS`        | 1 |             |
| `STATUS_FAIL`        | 2 |             |
| `STATUS_CACHED`      | 3 |             |

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [ListInvocations (response)](viewer.md#listinvocations), [StreamEvents (response)](viewer.md#streamevents).

### Stream

Stream identifies which pipe an output event came from.

Source: [viewer.proto:46](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L46).

| Value                | # | Description |
| -------------------- | - | ----------- |
| `STREAM_UNSPECIFIED` | 0 |             |
| `STREAM_STDOUT`      | 1 |             |
| `STREAM_STDERR`      | 2 |             |

Used by: [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [StreamEvents (response)](viewer.md#streamevents).

### Trigger

Trigger is how an invocation was spawned - the lineage a viewer surfaces ("this failure came from `magus affected ci`").

Source: [viewer.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L62).

| Value                 | # | Description                  |
| --------------------- | - | ---------------------------- |
| `TRIGGER_UNSPECIFIED` | 0 |                              |
| `TRIGGER_RUN`         | 1 | magus run                    |
| `TRIGGER_AFFECTED`    | 2 | magus affected               |
| `TRIGGER_CI`          | 3 | magus ci / affected ci       |
| `TRIGGER_X`           | 4 | magus x (interactive picker) |
| `TRIGGER_WATCH`       | 5 | magus watch                  |
| `TRIGGER_DIRECT`      | 6 | a directly invoked spell/op  |

Used by: [GetInvocation (response)](viewer.md#getinvocation), [GetJournal (response)](viewer.md#getjournal), [ListEvents (response)](viewer.md#listevents), [ListInvocations (response)](viewer.md#listinvocations), [StreamEvents (response)](viewer.md#streamevents).

### TurnRole

TurnRole is who a turn belongs to.

Source: [viewer.proto:291](https://github.com/egladman/magus/blob/main/proto/magus/viewer/v1alpha1/viewer.proto#L291).

| Value                   | # | Description |
| ----------------------- | - | ----------- |
| `TURN_ROLE_UNSPECIFIED` | 0 |             |
| `TURN_ROLE_USER`        | 1 |             |
| `TURN_ROLE_ASSISTANT`   | 2 |             |
| `TURN_ROLE_REASONING`   | 3 |             |
| `TURN_ROLE_TOOL`        | 4 |             |

Used by: [GetSessionActivity (response)](viewer.md#getsessionactivity).


---
title: ActivityService
generated_from: reference/api/
description: "ActivityService serves the trail to a viewer, mirroring magus.viewer.v1alpha1's shape: List a page of events (newest first), Get a payload blob by ref."
tags: [api, proto, connect, grpc, activityservice]
---

# ActivityService

ActivityService serves the trail to a viewer, mirroring magus.viewer.v1alpha1's shape: List a page of events (newest first), Get a payload blob by ref. Mounted on the console's human-facing API surface, never under /mcp (the agent protocol surface).

Package `magus.activity.v1alpha1`, defined in `proto/magus/activity/v1alpha1/activity.proto`. Source: [activity.proto:158](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L158). Part of the [daemon API](../../index.md).

## Methods

### ListActivityEvents

ListActivity returns a page of recent events, newest first, narrowed by filter.

`POST /magus.activity.v1alpha1.ActivityService/ListActivityEvents`: unary. Source: [activity.proto:160](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L160).

Takes [ListActivityEventsRequest](#listactivityeventsrequest), returns [ListActivityEventsResponse](#listactivityeventsresponse).

### GetPayload

GetPayload returns a stored request or response body by its ref (from an ActivityEvent).

`POST /magus.activity.v1alpha1.ActivityService/GetPayload`: unary. Source: [activity.proto:162](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L162).

Takes [GetPayloadRequest](#getpayloadrequest), returns [Payload](#payload).

### WatchActivityEvents

WatchActivityEvents follows the same trail forward, OLDEST first, until the caller hangs up. It is the polling half of ListActivityEvents turned inside out: the console and `magus job watch` both want "tell me when something happens", and asking a list endpoint that question costs a full retained-window scan per second per reader.

It merges three producers into the one envelope, which is why the filter is where it is rather than on the client: file changes the daemon's watcher saw, attributed to the job whose declared write paths cover the path; the guard's tool-call observations, attributed by the lease the hook resolved; and the runs recorded against a job. A reader narrows by job, session or path and gets one time-ordered stream of all three, so "what is that worker doing" is one subscription rather than three.

`POST /magus.activity.v1alpha1.ActivityService/WatchActivityEvents`: server streaming. Source: [activity.proto:174](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L174).

Takes [WatchActivityEventsRequest](#watchactivityeventsrequest), returns [ActivityEvent](#activityevent).

## Messages

### ActivityEvent

ActivityEvent is one recorded action - the atom of the trail. The envelope (time, actor, kind, action, outcome) is common to every kind; the payload refs point into the activity blob store (fetched via GetPayload) so a large request/response body never bloats the line. For an MCP tool call: action is the tool name, request is the arguments, response is the result. For an agent command observation: action is the host tool name, request is the normalized invocation, and response is the guard decision. For a token lifecycle event: action is the RPC method and the refs are empty.

Source: [activity.proto:88](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L88).

| Field            | Type                | #  | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| ---------------- | ------------------- | -- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `time`           | Timestamp           | 1  | when the action occurred                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `kind`           | [Kind](#kind)       | 2  |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `actor`          | string              | 3  | the origin fields below as one label: "eli via claude-code", "daemon", "unattributed"                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `action`         | string              | 4  | the specific action: a tool name, "connector.create"                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `outcome`        | [Outcome](#outcome) | 5  |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `error`          | string              | 6  | error text when outcome is OUTCOME\_ERROR                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `duration`       | Duration            | 7  | wall-clock, on call-shaped actions                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `request_ref`    | string              | 8  | Content-addressed payload refs, provenance-prefixed (an MCP payload is "mcp<hash>"). Empty when the action has no such body. Resolve with GetPayload.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `response_ref`   | string              | 9  |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `preview`        | string              | 10 | opening characters of the response, for list views                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `request_bytes`  | int64               | 11 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `response_bytes` | int64               | 12 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `workspace`      | string              | 13 | The workspace root the action pertained to; empty for a daemon-wide action not bound to one workspace (an MCP call). The trail is a single daemon-wide stream, so this disambiguates a job by its workspace rather than fragmenting the record across per-workspace directories.                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `host`           | string              | 14 | The agent host behind the action and that host's own session id, empty when the producer could not know them. The name is an opaque label the caller supplies, not a set magus enumerates: a hook is told its host by the wrapper that ran it, because no local process can discover which agent host started it. An MCP call is attributed from the client's handshake name, or its HTTP User-Agent when it recorded none, mapped into this same field so one view can group both kinds by host rather than switching on kind first.  They ride the EVENT rather than the request blob, which also carries them: a 200-row feed grouped by host must not cost 200 GetPayload calls.                                           |
| `session`        | string              | 15 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `unit`           | string              | 16 | The work-ledger lease this action belongs to, empty when the producer could not correlate one. The field keeps the "unit" spelling; magus calls the concept a lease. Set today only by KIND\_AGENT\_SPAWN, and only when the handed context declared it: no host event names a magus lease, so the producer scans the lease prompt for a documented marker line ("lease: <id>") instead. Correlation is COOPERATIVE - an orchestrator that wants the join writes the marker, and one that does not leaves this empty, which is a missing join rather than a wrong one. It rides the event rather than the blob for the same reason host and session do: joining a page of rows to a ledger must not cost a GetPayload per row. |
| `contested`      | repeated string     | 17 | On a KIND\_FILE\_CHANGE, the leases that BOTH declared this path, set only when more than one did. unit is then empty, because there is no answer to "whose write is this": the write paths are meant to be disjoint and this path is the evidence they are not.  A repeated field rather than a sentence in preview, because a reader watching one lease has to ask "am I one of these" on every row, and parsing prose to answer it is how the one event a damaged plan most needs to surface gets dropped. A filter on units matches a contested event that names one of them, which is deliberate: the event is attributed to nobody and is still that reader's business.                                                  |
| `user`           | string              | 18 | Where the action came from, one field per channel, so a reader never guesses which kind of value a single string holds. user is the OS account the recording process ran as, read from the OS. entry\_point is where the request entered magus (cli, hook, mcp, rpc, daemon). credential names the bearer credential a daemon request presented, as the daemon verified it. agent is the host's subagent id within session. actor above is these rendered as one label for a row head, and filters match that label.                                                                                                                                                                                                           |
| `entry_point`    | string              | 19 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `credential`     | string              | 20 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `agent`          | string              | 21 |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |

Used by: [ListActivityEvents (response)](activity.md#listactivityevents), [WatchActivityEvents (response)](activity.md#watchactivityevents).

### ActivityQuery

ActivityQuery narrows the listing server-side. Fields AND together; repeated values within a field OR; the time window bounds it.

Source: [activity.proto:179](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L179).

| Field      | Type                                                 | # | Description                                                                                                                                                                                                                                                                                                                     |
| ---------- | ---------------------------------------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kinds`    | [repeated Kind](#kind)                               | 1 | restrict to these action kinds                                                                                                                                                                                                                                                                                                  |
| `actors`   | repeated string                                      | 2 | restrict to these actor labels                                                                                                                                                                                                                                                                                                  |
| `actions`  | repeated string                                      | 3 | restrict to these actions (e.g. tool names)                                                                                                                                                                                                                                                                                     |
| `time`     | [TimeRange](../../query/v1alpha1/query.md#timerange) | 4 | action-time window                                                                                                                                                                                                                                                                                                              |
| `units`    | repeated string                                      | 5 | The three narrowings a person watching a worker asks for. They are here rather than on the watch request alone because the same question is worth asking of history: "what has this job been doing" and "what is it doing now" differ only in which verb you call. restrict to these leases (magus calls one a job)             |
| `sessions` | repeated string                                      | 6 | restrict to these host session ids                                                                                                                                                                                                                                                                                              |
| `paths`    | repeated string                                      | 7 | Restrict to file changes under these paths. A path matches the way a declared write path does, so naming a directory answers for what is under it. It selects FILE events only: a tool call and a run are attributed by lease, not by path, and quietly returning them for a path filter would report reach nobody asked about. |

Used by: [ListActivityEvents (request)](activity.md#listactivityevents), [WatchActivityEvents (request)](activity.md#watchactivityevents).

### GetPayloadRequest

Source: [activity.proto:216](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L216).

| Field | Type   | # | Description                                                                                                                |
| ----- | ------ | - | -------------------------------------------------------------------------------------------------------------------------- |
| `ref` | string | 1 | _string.pattern: `^[a-z]{2,8}[0-9a-f]+$`_ A provenance-prefixed content ref: a short lowercase source tag followed by hex. |

Used by: [GetPayload (request)](activity.md#getpayload).

### ListActivityEventsRequest

Source: [activity.proto:196](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L196).

| Field        | Type                            | # | Description                     |
| ------------ | ------------------------------- | - | ------------------------------- |
| `page_size`  | int32                           | 1 | _int32.lte: 1000; int32.gte: 0_ |
| `page_token` | string                          | 2 |                                 |
| `filter`     | [ActivityQuery](#activityquery) | 3 |                                 |

Used by: [ListActivityEvents (request)](activity.md#listactivityevents).

### ListActivityEventsResponse

Source: [activity.proto:201](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L201).

| Field             | Type                                     | # | Description                 |
| ----------------- | ---------------------------------------- | - | --------------------------- |
| `events`          | [repeated ActivityEvent](#activityevent) | 1 |                             |
| `next_page_token` | string                                   | 2 | set when more events remain |

Used by: [ListActivityEvents (response)](activity.md#listactivityevents).

### Payload

Payload is one stored request or response body, resolved from an ActivityEvent's ref.

Source: [activity.proto:221](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L221).

| Field        | Type  | # | Description |
| ------------ | ----- | - | ----------- |
| `body`       | bytes | 1 |             |
| `size_bytes` | int64 | 2 |             |

Used by: [GetPayload (response)](activity.md#getpayload).

### WatchActivityEventsRequest

WatchActivityEventsRequest subscribes to the merged feed.

Source: [activity.proto:207](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L207).

| Field      | Type                            | # | Description                                                                                                                                                                                                                                                                                                                                                       |
| ---------- | ------------------------------- | - | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `filter`   | [ActivityQuery](#activityquery) | 1 |                                                                                                                                                                                                                                                                                                                                                                   |
| `backfill` | int32                           | 2 | _int32.lte: 1000; int32.gte: 0_ backfill is how many already-recorded matching events to send before following. It is what stops a reader opening a drawer onto a blank panel and reading it as "nothing has happened": a job that has been running for an hour has a past, and a stream that starts at now hides all of it. Zero means none; the server caps it. |

Used by: [WatchActivityEvents (request)](activity.md#watchactivityevents).

## Enums

### Kind

Kind classifies the recorded action by its source. A reader switches on kind; new sources add a value without changing the envelope.

Source: [activity.proto:23](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L23).

| Value                   | #  | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| ----------------------- | -- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `KIND_UNSPECIFIED`      | 0  |                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `KIND_MCP_TOOL_CALL`    | 1  | an agent invoked an MCP tool over the daemon (emitted)                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `KIND_JOB`              | 2  | The remaining sources share this envelope; each is emitted once its producer records into the trail, with no schema change. A reader/dashboard selects the kinds it wants (see ActivityQuery.kinds), so one stream serves the agent view, a jobs view, and a full log. a daemon background job: SCIP reindex, graph build, VCS refresh (emitted)                                                                                                                                        |
| `KIND_CONFIG_CHANGE`    | 3  | reserved: magus.yaml changed on reload, or a `magus config set` mutation                                                                                                                                                                                                                                                                                                                                                                                                                |
| `KIND_TOKEN_LIFECYCLE`  | 4  | reserved: a connector token was minted or revoked                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `KIND_SANDBOX_DENIAL`   | 5  | magus's own read/write/exec check refused an access; not a kernel-landlock denial, which reports nothing back (emitted)                                                                                                                                                                                                                                                                                                                                                                 |
| `KIND_MEMORY`           | 6  | a console MemoryService action on the durable magus\_memory files (reads audited too)                                                                                                                                                                                                                                                                                                                                                                                                   |
| `KIND_AGENT_COMMAND`    | 7  | An agent host observed a shell or file-tool invocation. The request blob contains normalized host/tool/session data and the command or path; the response blob contains the guard decision. OUTCOME\_OK means the observation was recorded, NOT that a pre-hooked command later succeeded.                                                                                                                                                                                              |
| `KIND_CREDENTIAL_GRANT` | 8  | A run made a credential reachable: a magusfile granted one to a destination host, or opened a loopback endpoint carrying one. The event names the REFERENCE, the host and the header, never the value - a grant resolves nothing at declaration time, and resolving one in order to log it would defeat that. It is the governance half of a fact the execution journal already records per invocation; this is what connects an agent's tool call to the credential it made spendable. |
| `KIND_AGENT_SPAWN`      | 9  | An orchestrating agent handed work to a sub-agent. The request blob carries the CONTEXT that was handed over - the lease's whole point, and routinely kilobytes, so only its ref rides the event. There is no response blob and no guard decision: a spawn is an observation, not a judged surface. OUTCOME\_OK means the handoff was observed, NOT that the sub-agent later succeeded.                                                                                                 |
| `KIND_NOTES`            | 10 | The console NotesService door onto the workspace's human-authored notes. The service has no write path - a note's whole value is the guarantee that a person wrote it - so every event under this kind is a READ, audited because this is the only door that can serve the PRIVATE note store, which lives outside any repository and which nothing else attributes.                                                                                                                    |
| `KIND_FILE_CHANGE`      | 11 | A path under a job's declared write paths changed, as the daemon's file watcher saw it. action is the repo-relative path and unit is the job whose write paths cover it. The producer is the FILESYSTEM, not an agent: this is the one kind that needs no cooperation from the worker being watched, which is the whole reason a person can see what a worker is doing without asking it. An empty unit means no live job covered the path.                                             |
| `KIND_RUN`              | 12 | A run magus recorded against a job: its check, one of its completion gates, or the daemon's own last run of a catalog job. action is the rendered command and preview names which of the three it was. OUTCOME\_ERROR means the run failed, which is the one kind here where the outcome is a fact about the work rather than about the recording.                                                                                                                                      |
| `KIND_GUARD_POLICY`     | 13 | The effective workspace guard rules changed. action is loaded, tightened, loosen\_pending, committed or removed; the request blob names each source file by its working-tree and approved git blob ids, never its body. Written only on a change, so these rows read as the lineage of the workspace's policy, and a verdict event's policy digest points at one.                                                                                                                       |

Used by: [ListActivityEvents (request)](activity.md#listactivityevents), [ListActivityEvents (response)](activity.md#listactivityevents), [WatchActivityEvents (request)](activity.md#watchactivityevents), [WatchActivityEvents (response)](activity.md#watchactivityevents).

### Outcome

Outcome is how the action ended.

Source: [activity.proto:75](https://github.com/egladman/magus/blob/main/proto/magus/activity/v1alpha1/activity.proto#L75).

| Value                 | # | Description |
| --------------------- | - | ----------- |
| `OUTCOME_UNSPECIFIED` | 0 |             |
| `OUTCOME_OK`          | 1 |             |
| `OUTCOME_ERROR`       | 2 |             |

Used by: [ListActivityEvents (response)](activity.md#listactivityevents), [WatchActivityEvents (response)](activity.md#watchactivityevents).


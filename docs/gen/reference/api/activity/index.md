---
title: ActivityService
description: "ActivityService serves the trail to a viewer, mirroring magus.viewer.v1's shape: List a page of events (newest first), Get a payload blob by ref."
tags: [api, proto, connect, grpc, activityservice]
---

# ActivityService

ActivityService serves the trail to a viewer, mirroring magus.viewer.v1's shape: List a page of events (newest first), Get a payload blob by ref. Mounted on the console's human-facing API surface, never under /mcp (the agent protocol surface).

Package `magus.activity.v1`, defined in `proto/magus/activity/v1/activity.proto`. Part of the [daemon API](index.md).

## Methods

### ListActivity

ListActivity returns a page of recent events, newest first, narrowed by filter.

`POST /magus.activity.v1.ActivityService/ListActivity` - unary.

Takes `ListActivityRequest`, returns `ListActivityResponse`.

### GetPayload

GetPayload returns a stored request or response body by its ref (from an ActivityEvent).

`POST /magus.activity.v1.ActivityService/GetPayload` - unary.

Takes `GetPayloadRequest`, returns `GetPayloadResponse`.

## Messages

### ActivityEvent

ActivityEvent is one recorded action - the atom of the trail. The envelope (time, actor, kind, action, outcome) is common to every kind; the payload refs point into the activity blob store (fetched via GetPayload) so a large request/response body never bloats the line. For an MCP tool call: actor is the agent id, action is the tool name, request is the arguments, response is the result. For an agent command observation: actor is the host-supplied agent/session identity when available, action is the host tool name, request is the normalized invocation, and response is the guard decision. For a token lifecycle event: actor is "cli", action is "connector.create"/"connector.revoke", and the refs are empty.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `time` | Timestamp | 1 | when the action occurred |
| `kind` | Kind | 2 |  |
| `actor` | string | 3 | who: an agent id, "cli", a user |
| `action` | string | 4 | the specific action: a tool name, "connector.create" |
| `outcome` | Outcome | 5 |  |
| `error` | string | 6 | error text when outcome is OUTCOME\_ERROR |
| `duration` | Duration | 7 | wall-clock, on call-shaped actions |
| `request_ref` | string | 8 | Content-addressed payload refs, provenance-prefixed (an MCP payload is "mcp<hash>"). Empty when the action has no such body. Resolve with GetPayload. |
| `response_ref` | string | 9 |  |
| `preview` | string | 10 | opening characters of the response, for list views |
| `request_bytes` | int64 | 11 |  |
| `response_bytes` | int64 | 12 |  |
| `workspace` | string | 13 | The workspace root the action pertained to; empty for a daemon-wide action not bound to one workspace (an MCP call). The trail is a single daemon-wide stream, so this disambiguates a job by its workspace rather than fragmenting the record across per-workspace directories. |

### ActivityQuery

ActivityQuery narrows the listing server-side. Fields AND together; repeated values within a field OR; the time window bounds it.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kinds` | repeated Kind | 1 | restrict to these action kinds |
| `actors` | repeated string | 2 | restrict to these actors |
| `actions` | repeated string | 3 | restrict to these actions (e.g. tool names) |
| `time` | TimeRange | 4 | action-time window |

### GetPayloadRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `ref` | string | 1 | A provenance-prefixed content ref: a short lowercase source tag followed by hex. |

### GetPayloadResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `body` | bytes | 1 |  |
| `bytes` | int64 | 2 |  |

### ListActivityRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 |  |
| `page_token` | string | 2 |  |
| `filter` | ActivityQuery | 3 |  |

### ListActivityResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `events` | repeated ActivityEvent | 1 |  |
| `next_page_token` | string | 2 | set when more events remain |

### TimeRange

TimeRange bounds a query to items between since and until (inclusive); either bound may be unset for an open-ended range. since doubles as a live-stream resume cursor.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `since` | Timestamp | 1 |  |
| `until` | Timestamp | 2 |  |

## Enums

### Kind

Kind classifies the recorded action by its source. A reader switches on kind; new sources add a value without changing the envelope.

| Value | # | Description |
|-------|---|-------------|
| `KIND_UNSPECIFIED` | 0 |  |
| `KIND_MCP_TOOL_CALL` | 1 | an agent invoked an MCP tool over the daemon (emitted) |
| `KIND_JOB` | 2 | The remaining sources share this envelope; each is emitted once its producer records into the trail, with no schema change. A reader/dashboard selects the kinds it wants (see ActivityQuery.kinds), so one stream serves the agent view, a jobs view, and a full log. a daemon background job: SCIP reindex, graph build, VCS refresh (emitted) |
| `KIND_CONFIG_CHANGE` | 3 | reserved: magus.yaml changed on reload, or a `magus config set` mutation |
| `KIND_TOKEN_LIFECYCLE` | 4 | reserved: a connector token was minted or revoked |
| `KIND_SANDBOX_DENIAL` | 5 | reserved: a target attempted a disallowed filesystem write |
| `KIND_MEMORY` | 6 | a console MemoryService action on the durable magus\_memory files (reads audited too) |
| `KIND_AGENT_COMMAND` | 7 | An agent host observed a shell or file-tool invocation. The request blob contains normalized host/tool/session data and the command or path; the response blob contains the guard decision. OUTCOME\_OK means the observation was recorded, NOT that a pre-hooked command later succeeded. |

### Outcome

Outcome is how the action ended.

| Value | # | Description |
|-------|---|-------------|
| `OUTCOME_UNSPECIFIED` | 0 |  |
| `OUTCOME_OK` | 1 |  |
| `OUTCOME_ERROR` | 2 |  |


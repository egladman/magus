---
title: ActivityService
description: "ActivityService serves the trail to a viewer, mirroring magus.viewer.v1alpha1's shape: List a page of events (newest first), Get a payload blob by ref."
tags: [api, proto, connect, grpc, activityservice]
---

# ActivityService

ActivityService serves the trail to a viewer, mirroring magus.viewer.v1alpha1's shape: List a page of events (newest first), Get a payload blob by ref. Mounted on the console's human-facing API surface, never under /mcp (the agent protocol surface).

Package `magus.activity.v1alpha1`, defined in `proto/magus/activity/v1alpha1/activity.proto`. Part of the [daemon API](index.md).

## Methods

### ListActivityEvents

ListActivity returns a page of recent events, newest first, narrowed by filter.

`POST /magus.activity.v1alpha1.ActivityService/ListActivityEvents`: unary.

Takes `ListActivityEventsRequest`, returns `ListActivityEventsResponse`.

### GetPayload

GetPayload returns a stored request or response body by its ref (from an ActivityEvent).

`POST /magus.activity.v1alpha1.ActivityService/GetPayload`: unary.

Takes `GetPayloadRequest`, returns `Payload`.

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
| `host` | string | 14 | The agent host behind the action and that host's own session id, empty when the producer could not know them. The name is an opaque label the caller supplies, not a set magus enumerates: a hook is told its host by the wrapper that ran it, because no local process can discover which agent host started it. An MCP call has no such wrapper and is attributed from its HTTP User-Agent instead, mapped into this same field so one view can group both kinds by host rather than switching on kind first.  They ride the EVENT rather than the request blob, which also carries them: a 200-row feed grouped by host must not cost 200 GetPayload calls. |
| `session` | string | 15 |  |
| `unit` | string | 16 | The work-ledger unit this action belongs to, empty when the producer could not correlate one. Set today only by KIND\_AGENT\_SPAWN, and only when the handed context declared it: no host event names a magus unit, so the producer scans the delegation prompt for a documented marker line ("unit: <id>") instead. Correlation is COOPERATIVE - an orchestrator that wants the join writes the marker, and one that does not leaves this empty, which is a missing join rather than a wrong one. It rides the event rather than the blob for the same reason host and session do: joining a page of rows to a ledger must not cost a GetPayload per row. |

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

### ListActivityEventsRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 |  |
| `page_token` | string | 2 |  |
| `filter` | ActivityQuery | 3 |  |

### ListActivityEventsResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `events` | repeated ActivityEvent | 1 |  |
| `next_page_token` | string | 2 | set when more events remain |

### Payload

Payload is one stored request or response body, resolved from an ActivityEvent's ref.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `body` | bytes | 1 |  |
| `size_bytes` | int64 | 2 |  |

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
| `KIND_CREDENTIAL_GRANT` | 8 | A run made a credential reachable: a magusfile granted one to a destination host, or opened a loopback endpoint carrying one. The event names the REFERENCE, the host and the header, never the value - a grant resolves nothing at declaration time, and resolving one in order to log it would defeat that. It is the governance half of a fact the execution journal already records per invocation; this is what connects an agent's tool call to the credential it made spendable. |
| `KIND_AGENT_SPAWN` | 9 | An orchestrating agent handed work to a sub-agent. The request blob carries the CONTEXT that was handed over - the delegation's whole point, and routinely kilobytes, so only its ref rides the event. There is no response blob and no guard decision: a spawn is an observation, not a judged surface. OUTCOME\_OK means the handoff was observed, NOT that the sub-agent later succeeded. |
| `KIND_NOTES` | 10 | The console NotesService door onto the workspace's human-authored notes. The service has no write path - a note's whole value is the guarantee that a person wrote it - so every event under this kind is a READ, audited because this is the only door that can serve the PRIVATE note store, which lives outside any repository and which nothing else attributes. |

### Outcome

Outcome is how the action ended.

| Value | # | Description |
|-------|---|-------------|
| `OUTCOME_UNSPECIFIED` | 0 |  |
| `OUTCOME_OK` | 1 |  |
| `OUTCOME_ERROR` | 2 |  |


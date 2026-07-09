---
title: Browser bridge
description: Read-only loopback API that lets the Graph Explorer show your live workspace. Loopback only, bearer token, no mutation.
tags: [bridge, graph, privacy]
---

# Browser bridge

The browser bridge is three frozen, read-only GET routes that the magus daemon
exposes over loopback so the hosted [Graph Explorer](https://eli.gladman.cc/magus/graph/)
can display your current workspace.

**Nothing in the browser can make the daemon do anything.** The bridge has no
write surface, no POST routes, and no way to trigger a build, run a target, or
change configuration. This is a design decision, not just a security posture
(see section 0.3 of the PWA plan).

## What the bridge serves

Every byte the bridge can emit, enumerated:

| Route | Content |
|---|---|
| `GET /api/v1/graph` | Merged knowledge graph (same bytes as `magus graph export -o json`) |
| `GET /api/v1/graph?flavor=targets` | Target dependency graph (same as `magus describe graph -o json`) |
| `GET /api/v1/graph?level=projects` | Project skeleton only: project nodes + project edges |
| `GET /api/v1/graph?select=<terms>` | Scoped neighborhood (same query engine as `magus query`) |
| `GET /api/v1/status` | Daemon pool state (same as `magus status -o json`) |
| `GET /api/v1/events` | SSE stream: `event: graph` when the workspace graph changes |

No other routes exist. The bridge mounts at `/api/v1/` on the same port as the
MCP server (`127.0.0.1:7391` by default).

**Error bodies.** When a route fails (5xx), the response body contains
`err.Error()` detail to help an authenticated loopback caller diagnose the
problem. This detail is returned only to a caller that has already passed the
bearer-token check.

Symbol shards (`@symbols`) are NOT loaded for the default `/api/v1/graph` call.
They are loaded only when `?select=<terms>` uses a symbol-seeding query (a
`symbol:` prefix or `kind:symbol`). This preserves the lazy-load contract:
symbol data stays opt-in.

**Uncached variants.** The `?level=projects` and `?flavor=targets` query params
reparse the workspace target graph on every request (they call `DescribeGraph`
which reads the cached in-memory target graph but does not cache the variant
serialization). This is a known limitation; memoization per variant is deferred.

## How it is secured

**Loopback only.** The bridge refuses to mount on any non-loopback bind
address. If you set `mcp.address` to a non-loopback IP (for k8s or LAN use),
the bridge logs a warning and does not register its routes.

**Bearer token.** Every request must carry `Authorization: Bearer <token>`.
The token is the same one the MCP server uses. Retrieve it with:

```
magus config mcp token print
```

The token is stored on disk (`~/.config/magus/mcp-token`) and never logged.

**DNS-rebind guard.** The bridge shares the MCP server's host-header check.
A request whose `Host` header does not resolve to the loopback range is
rejected with 403 before the bearer token is examined.

**CORS.** `Access-Control-Allow-Origin` is set only for:
- The hosted Graph Explorer origin (`https://eli.gladman.cc`)
- `http://localhost:<port>` (local site development)
- `http://127.0.0.1:<port>` (local site development)

Any other origin gets no CORS headers. The browser will block its own
cross-origin request before any data is read.

**Chrome Private Network Access.** When Chrome sends the
`Access-Control-Request-Private-Network: true` preflight header (Private
Network Access spec), the bridge replies with
`Access-Control-Allow-Private-Network: true`. Without this, Chrome 94+ blocks
requests from an HTTPS page to a loopback address. Expect a one-time
permission prompt in Chrome when you first connect the explorer.

## Safari limitation

Safari blocks fetch requests from an HTTPS page to `http://127.0.0.1` (mixed
content). The bridge will not work in Safari's live mode. Use
`magus graph open --serve` instead, which runs an ephemeral loopback server
with a matching same-origin response and opens the graph via a `#src=` fragment
that is served directly.

## Kill switch

Disable the bridge in `magus.yaml`:

```yaml
bridge:
  enabled: false
```

Or via environment variable: `MAGUS_BRIDGE_ENABLED=false`.

The bridge only exists when the daemon binary is compiled with `-tags mcp`.
A binary built without that tag has no bridge and no `/api/v1/` routes.

## Privacy statement

The bridge serves your workspace graph over loopback. It does not:

- Send data to any external service
- Log request payloads
- Store anything beyond what the daemon already caches on disk
- Accept any write request
- Expose any path outside the routes listed above

The hosted explorer page loads your graph via the bearer-authenticated fetch.
The graph data never appears in a URL (fragments are used for the fragment
delivery mode; the live mode uses an Authorization header that browsers do not
log in the address bar).

## `magus doctor` check

`magus doctor` reports bridge reachability when the daemon is running:

```
[pass] web bridge: reachable at http://127.0.0.1:7391/api/v1/graph
    bearer token: magus config mcp token print
```

When the daemon is not running, the check is skipped (not a failure).
When `bridge.enabled: false` is set, the check reports that the bridge is
disabled.

## Phase 9 (upcoming): Live mode pairing

A future release (`magus graph open --live`) will probe the daemon and open the
explorer with a `#live=127.0.0.1:7391&token=<bearer>` fragment, enabling
always-live workspace viewing. The bridge is the prerequisite. The token is
consumed by the page on load and replaced with session storage so it does not
persist in the URL.

The explorer enforces that the host in `#live=` is literally `127.0.0.1` or
`[::1]`. `localhost` and all other hostnames are rejected. This 5-line
client-side check is what makes the "data cannot leave your machine" claim
verifiable by reading the source.

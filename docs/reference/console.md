---
title: Console API
description: Read-only loopback GET API that lets the Graph Explorer show your live workspace. Loopback only, bearer token. Mutation is confined to a separate, authenticated job-control service.
tags: [console, graph, privacy]
aliases: [console, browser-bridge]
---

# Console API

The console is three frozen, read-only GET routes that the magus daemon
exposes over loopback so the hosted [console](https://eli.gladman.cc/magus/console/)
(its Graph Explorer surface) can display your current workspace.

**These read routes cannot change your workspace.** The console's GET API has no
write surface, no POST routes, and no way to trigger a build, run a target, or
change configuration - it only reads. This is a design decision, not just a
security posture (see section 0.3 of the PWA plan).

The daemon does expose one mutating surface, separate from these read routes: an
authenticated [job-control service](#job-control) that triggers maintenance jobs
(reconcile the graph, rotate the activity trail or run-logs, clear the cache).
It is gated behind the same loopback bind and bearer token, and it cannot run an
arbitrary command - only a fixed set of maintenance jobs.

## What the console serves

Every byte the console can emit, enumerated:

| Route                              | Content                                                             |
| ---------------------------------- | ------------------------------------------------------------------- |
| `GET /api/v1/graph`                | Merged knowledge graph (same bytes as `magus graph export -o json`) |
| `GET /api/v1/graph?flavor=targets` | Target dependency graph (same as `magus describe graph -o json`)    |
| `GET /api/v1/graph?level=projects` | Project skeleton only: project nodes + project edges                |
| `GET /api/v1/graph?select=<terms>` | Scoped neighborhood (same query engine as `magus query`)            |
| `GET /api/v1/status`               | Daemon pool state (same as `magus status -o json`)                  |
| `GET /api/v1/events`               | SSE stream: `event: graph` when the workspace graph changes         |

No other routes exist. The console mounts at `/api/v1/` on the same port as the
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

## Job control

Separate from the read routes above, the daemon hosts a **mutating** Connect
service, `magus.job.v1.JobService`, so a browser client (or the CLI) can trigger
background maintenance without an open action endpoint. It is the daemon's only
write surface, and it is bounded: it submits a fixed set of named jobs, never an
arbitrary command.

| RPC              | Effect                                                        |
| ---------------- | ------------------------------------------------------------- |
| `SyncGraph`      | Reconcile the knowledge graph to current source               |
| `RotateActivities` | Trim the activity trail to its cap                            |
| `RotateLogs`     | Trim the invocation run-log journals to their cap             |
| `ClearCache`     | Invalidate cached build entries                               |
| `ListJobs`       | Report every job's running state, last run, and target size   |

Each submit is fire-and-forget and coalesced (an identical in-flight job is not
started twice) and returns a metadata snapshot - the job's last run and the
current size of what it maintains. The same jobs are reachable from the CLI with
`magus server job <name>`. The service is mounted behind the same loopback bind
and bearer token as everything else here; it is never served unauthenticated.

## How it is secured

**Loopback only.** The console refuses to mount on any non-loopback bind
address. If you set `mcp.address` to a non-loopback IP (for k8s or LAN use),
the console logs a warning and does not register its routes.

**Bearer token.** Every request must carry `Authorization: Bearer <token>`.
The token is the same one the MCP server uses. Retrieve it with:

```
magus config mcp token print
```

The token is stored on disk (`~/.config/magus/mcp-token`) and never logged.

**DNS-rebind guard.** The console shares the MCP server's host-header check.
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
Network Access spec), the console replies with
`Access-Control-Allow-Private-Network: true`. Without this, Chrome 94+ blocks
requests from an HTTPS page to a loopback address. Expect a one-time
permission prompt in Chrome when you first connect the explorer.

## Safari limitation

Safari blocks fetch requests from an HTTPS page to `http://127.0.0.1` (mixed
content). The console will not work in Safari's live mode. Use
`magus graph open --serve` instead, which runs an ephemeral loopback server
with a matching same-origin response and opens the graph via a `#src=` fragment
that is served directly.

## Kill switch

Disable the console in `magus.yaml`:

```yaml
console:
  enabled: false
```

Or via environment variable: `MAGUS_CONSOLE_ENABLED=false`.

The console only exists when the daemon binary is compiled with `-tags mcp`.
A binary built without that tag has no console and no `/api/v1/` routes.

## Privacy statement

The console serves your workspace graph over loopback. It does not:

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

`magus doctor` reports console reachability when the daemon is running:

```
[pass] console: reachable at http://127.0.0.1:7391/api/v1/graph
    bearer token: magus config mcp token print
```

When the daemon is not running, the check is skipped (not a failure).
When `console.enabled: false` is set, the check reports that the console is
disabled.

## Live mode pairing

`magus graph open --live` opens the explorer connected to the running daemon.

### How to pair

1. Start the daemon: `magus server start`
2. Run `magus graph open --live` (or `--live --print` to copy the URL)
3. The explorer shows a `live: <workspace>` badge and updates within seconds of file changes

The link contains `#live=127.0.0.1:7391&token=<bearer>`. The page:

- Validates the host is literally `127.0.0.1` or `[::1]` before making any fetch
- Consumes the token and strips it from the URL via `history.replaceState`
- Stores the token in sessionStorage (tab lifetime) unless you tick "Remember this workspace", which moves it to localStorage

Zero-arg default: a plain `magus graph open` with no flags checks if the daemon is running. If it is, it automatically picks `--live`. Otherwise it falls back to the `#data=` fragment.

### Two-state model

The explorer has exactly two source states:

| State    | Badge                    | What it means                                             |
| -------- | ------------------------ | --------------------------------------------------------- |
| snapshot | `snapshot: <provenance>` | Data from fragment/file/demo/--serve; frozen at load time |
| live     | `live: <workspace>`      | Data from the daemon; refreshes on file changes           |

"Connected but stale" is impossible: when the SSE stream disconnects, a banner appears ("disconnected - showing workspace as of HH:MM, reconnecting...") and auto-reconnect runs with exponential backoff (1s to 30s). The data stays visible while reconnecting.

### Safari limitation

Safari blocks fetch requests from an HTTPS page to `http://127.0.0.1` (mixed content). Live mode cannot connect in Safari. Use `magus graph open --serve` instead: it runs an ephemeral loopback server and opens the graph via a `#src=` fragment that is compatible with Safari's same-origin restriction.

### Target graph in live mode

`magus graph open --live --targets` opens the live target dependency graph:
`#live=127.0.0.1:7391&token=<bearer>&flavor=targets`

### Affected view

When the daemon has computed an affected set (from `magus affected` in a CI context), the `/api/v1/status` response includes an `affected` array of node ids. The "What does my diff touch?" view is enabled automatically and paints those nodes.

## Verify our claims - don't take our word for it

Your dependency graph may be confidential. Every claim below is either
enforced by your browser or checkable by you. Nothing on this page asks for
trust.

### Claim: this page cannot send your graph or source code anywhere

Every page on this site carries a Content-Security-Policy that your browser
enforces - a `<meta>` tag near the top of the document that is the page's
complete network permission, in one line.

1. Press `Ctrl+U` (macOS: `Cmd+Option+U`) to view the page source. Find the
   `<meta http-equiv="Content-Security-Policy" ...>` tag (it sits right after
   `<meta name="generator" content="magus">`). Its `connect-src` clause -
   the directive that governs `fetch`/`XMLHttpRequest`/SSE, the ways a page
   could actually exfiltrate data - reads
   `connect-src 'self' http://127.0.0.1:* http://[::1]:*`: this page's own
   origin, plus your machine's loopback address, and nothing else.
   `default-src 'self'` closes the same same-origin-only gap for anything not
   named by a more specific directive.

   `img-src` is a narrower, deliberately scoped exception, and it is not the
   same on every page: an `img-src` GET can technically carry data baked
   into its URL (unlike `connect-src`, the browser does not refuse it), so
   the graph and playground pages - the only pages that ever hold your
   dependency graph or source code - carry `img-src 'self' data:` with no
   external host at all. There is no image-URL channel on those pages for
   your data to ride out on. Only the home page's `img-src` also allows
   `https://github.com` and `https://pkg.go.dev`, for two static status
   badges (CI result, doc coverage). Those badge URLs are fixed strings
   compiled into the page; nothing on the home page builds an image URL
   from anything you typed or loaded there.

2. Watch Chrome enforce it. Press `F12` to open DevTools, pick the
   **Console** tab, and paste:
   `fetch("https://example.com")`
   Chrome refuses, and the error message quotes the policy back to you:
   _"Refused to connect ... because it violates the following Content
   Security Policy directive: connect-src ..."_. That refusal is your
   browser, not our code.
3. One deliberate narrowing this policy causes: the graph page's `#src=<url>`
   loader and the playground's `#src=<url>` loader can both point at an
   arbitrary CORS-enabled address (e.g. a colleague's raw GitHub link) - that
   fetch is refused by the same `connect-src` for any host that is not this
   site or your loopback. Both loaders already handle a fetch failure
   gracefully (a status message, not a crash); use `#data=` (a local file,
   drag-and-drop, or `magus graph open`'s default fragment) or a loopback
   source (`magus graph open --serve` / `--live`) instead.

### Claim: your graph never appears in any network request

When you use `magus graph open`, your graph travels in the URL **fragment**
(the part after `#`). Browsers never include fragments in HTTP requests -
that's the HTTP standard, not our promise.

1. Open DevTools (`F12`) -> **Network** tab. Tick **Preserve log**.
2. Load your graph: run `magus graph open` in your workspace, or drag a
   `graph.json` onto the [console's Graph Explorer](https://eli.gladman.cc/magus/console/).
3. Read the request list. Every row is a `GET` for a static file from this
   site's own origin (or, in live mode, your own loopback address). Click any
   row - the **Payload** tab is absent (no request carries a body). Compare
   any request's URL against your address bar: the `#data=...` portion
   appears in none of them.
4. Type `method:POST` into the Network filter box: zero results. This page
   never POSTs anything, anywhere.

### Claim: everything works with your network unplugged

The strongest proof: data cannot leave a machine that has no connection.

1. Visit the graph or playground page once while online (the service worker
   caches it - see DevTools -> **Application** -> **Service workers** and
   **Cache storage**).
2. Go offline for real (Wi-Fi off / cable out), or in DevTools -> **Network**
   tab set the throttling dropdown from **No throttling** to **Offline**.
3. Reload. The page comes back - served from your disk. Now load your
   confidential graph (drag the file in) and explore it fully. The page
   shows an "offline - everything on this page is local" badge while
   disconnected (`js/offline-badge.js`).

### Claim: we store nothing without asking

DevTools -> **Application** tab -> **Cookies**: none. **Local storage** /
**Session storage**: empty, unless you used live mode - the daemon token is
kept in session storage under the `magus-live-token` key for the tab's
lifetime, or promoted to local storage only after you tick "Remember this
workspace" (see "Live mode pairing" above). Ticking it also sets a second
local-storage key, `magus-live-remember`, so the explorer knows to keep
reading from local storage on your next visit - two keys once you tick it,
zero before that and zero if you never use live mode. Clear either with one
click, right there.

### The deep audit: record every byte Chrome sends

For a security review, don't sample - record. `chrome://net-export` captures
a log of _all_ network activity in the browser, below the page's ability to
hide anything.

1. Open `chrome://net-export`, choose a log file, press **Start Logging to
   Disk**.
2. In another tab, load this page and your graph; explore for a minute.
3. Stop logging. The log is a local JSON file on your disk - search it for
   any project or target name from your graph. For sensitive graphs, grep the
   file locally rather than uploading it to a log viewer.

### Claim: the code running here is the code in the repo

This site is generated from the open [magus repository](https://github.com/egladman/magus),
and the built assets are committed and CI-checked. `site-manifest.sha256`
(at the site root, e.g. `https://eli.gladman.cc/magus/site-manifest.sha256`)
lists every served file with its SHA-256, in `sha256sum(1)` format. To verify
any asset:

```
curl -s <asset-url> | sha256sum
```

and compare against the manifest and the repo's committed copy (the docs site
under `docs/gen/`, the console app under `console/gen/`). The JavaScript is
unminified enough to read; start at the console's `graph/explorer.js` -
`loadGraph` and `readGraphFile` are the functions that
ingest a graph (the `#data=`/`#src=`/demo fallback chain, and drag-drop/
file-input/`launchQueue` respectively), and there is no function that sends
it out.

### The one nuance: the service worker is not covered by this policy

A `<meta>`-delivered Content-Security-Policy governs the _page's own_
requests. It does not govern requests the service worker (`sw.js`) makes on
the page's behalf while intercepting `fetch` events - that is a documented
gap in the CSP spec, not a bug in this implementation. The mitigation is that
the service worker's source, `docs/sw.js.tmpl`, is about 60 lines total,
committed, and its `fetch` handler returns early on any cross-origin request
before it ever considers serving or caching one:

```js
if (url.origin !== self.location.origin) return;
```

(`sw.js.tmpl` line 42.) Read the whole file - it precaches a fixed list of
same-origin assets, serves HTML network-first, and serves everything else
cache-first. There is nothing else in it. If this site ever moves to a host
that supports real HTTP response headers, the CSP (and a policy that also
covers the service worker, via `Service-Worker-Allowed` scoping and a
worker-side CSP) will be promoted to headers and the `<meta>` tag kept only
as a fallback for hosts that cannot set headers.

### The opt-out: remove us entirely

If your threat model excludes our hosting altogether: clone the repo, run
`magus run generate docs`, and serve the `gen/` directory on your own
network. Every page here is origin-agnostic and works identically. (magus
ships no general-purpose static file server for hosting this site; the only
servers it binds are the ephemeral loopback `--serve` graph server and the
loopback daemon console documented above.)

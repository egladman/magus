---
title: Console API
description: Loopback JSON API the console reads your live workspace through - the graph, insight, diff, run-output, plan and ledger routes. Loopback only, bearer token, GET except where named. Mutation sits on a few bounded surfaces beside it, none of which touches your working tree.
tags: [console, graph, privacy]
aliases: [console, browser-bridge]
---

# Console API

The console is a small set of loopback JSON routes that the magus daemon
exposes so the hosted [console](https://eli.gladman.cc/magus/console/) (the
Graph Explorer and the surfaces beside it) can display your current workspace.

The console holds no privileged access: it is one client of the same contract
anyone can code against. The full schema - every service, method, message, and
enum, generated from the `.proto` files - is the [daemon API reference](api/index.md).

**Most of these routes cannot change your workspace.** They read what the daemon
already knows: they cannot edit a file or change configuration. Most of the
table below answers GET only and rejects any other method with a 405, with two
named exceptions. `POST /api/v1/diff/session` records a person's own review
state (where they are looking, which hunks they have read, what they said) in
the cache directory and touches no source file. `POST /api/v1/diff/run` starts
a run - the one read-surface route that is genuinely mutating, bounded to
whatever targets the magusfile declares. This is a design decision, not just a
security posture (see section 0.3 of the PWA plan).

What else can mutate sits beside this read surface rather than in it, and none
of it runs an arbitrary command or writes into your working tree:

- The [job-control service](#job-control), which submits a fixed set of
  maintenance jobs (reconcile the graph, rotate the activity trail or run-logs,
  clear the cache).
- `POST /api/v1/diff/session` and `POST /api/v1/diff/run`, the two exceptions above.
- `POST /api/v1/share`, which opens the time-boxed LAN listener described under
  [what the console serves](#what-the-console-serves). It requires a loopback
  peer as well as the bearer token, so only the local console can trigger it.
- `magus.memory.v1alpha1.MemoryService`, which edits your own handoff journal
  ([`magus memory`](manpage/magus-memory.md)). That journal lives in the user
  state directory outside the repository, so it is not workspace state either.

Every one of them is gated behind the same loopback bind and bearer token.

## What the console serves

Every route on the console's `/api/v1/` surface, enumerated:

| Route                              | Content                                                                                                                            |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `GET /api/v1/graph`                | Merged knowledge graph (same bytes as `magus graph export -o json`)                                                                |
| `GET /api/v1/graph?flavor=targets` | Target dependency graph (same as `magus describe graph -o json`)                                                                   |
| `GET /api/v1/graph?level=projects` | Project skeleton only: project nodes + project edges                                                                               |
| `GET /api/v1/graph?select=<terms>` | Scoped neighborhood (same query engine as `magus query`)                                                                           |
| `GET /api/v1/events`               | SSE stream: `event: graph` when the workspace graph changes                                                                        |
| `GET /api/v1/insight`              | Every [insight lens](../concepts/knowledge/insight.md): hotspots, affinity, ownership, trend, volatility                           |
| `GET /api/v1/diff`                 | Working-tree changes annotated with role, blast radius, changed-symbol reach, and coverage ([`magus diff`](manpage/magus-diff.md)) |
| `GET /api/v1/diff/patch`           | The same changes as one unified patch, without the annotation (much cheaper)                                                       |
| `GET /api/v1/diff/context`         | Supporting context for a changed symbol (callers, definitions) beyond the patch itself                                             |
| `POST /api/v1/diff/session`        | The human's half of a paired review: cursor, viewed marks, comments                                                                |
| `GET /api/v1/diff/review`          | Which review this branch has open on the forge, and what colleagues have already said on it                                        |
| `GET /api/v1/diff/branches`        | Other branches changing the same files                                                                                             |
| `POST /api/v1/diff/run`            | Run a target against the working tree; the one MUTATING route in this table, bounded to what the magusfile declares                |
| `GET /api/v1/plan`                 | The derived run plan: the target DAG the engine resolves, with each node's live state                                              |
| `GET /api/v1/ledger`               | The lease plan an agent [declared](../guides/integrations/agents/leases.md); magus enforces none of it                             |
| `GET /api/v1/attention`            | The attention queue: blocks waiting on a person, same shape as `magus session attention -o json`                                   |
| `POST /api/v1/attention`           | Dispose one request (`{"id","reason"}`). Nothing else closes one                                                                   |

One more route sits under `/api/v1/` without belonging to this read surface:
`POST /api/v1/share`, described below. The daemon's typed Connect
services - status, activity, metrics, insight, viewer, memory, notes, tool, and
[job control](#job-control) - are mounted at their own
`magus.<service>.v1alpha1.<Service>/` prefixes rather than here, and the
[daemon API reference](api/index.md) is their schema. The console mounts at
`/api/v1/` on the same port as the MCP server (`127.0.0.1:7391` by default).

There is no `GET /api/v1/status` any more. The typed
`magus.status.v1alpha1.StatusService/GetStatus` route replaced it and serves the same
live snapshot plus `observing_since` and config on a typed wire contract; the
console reads it there.

**Who may write to a review session.** `POST /api/v1/diff/session` is reachable
only from the console and the CLI, so every write on it is stamped as the
person's. An agent reaches the same session through the `magus_diff` MCP tool on
`/mcp` - whose `comment`, `suggest` and `resolve` ops are writes too - and those
are stamped as the agent's. Authorship is decided by which route the write
arrived on and never by the payload, which is what makes it unforgeable: an
agent cannot reach the human route, so it cannot post as the person.

**The share subset is smaller on purpose.** `POST /api/v1/share` opens an
on-demand, time-boxed LAN listener behind a fresh read-only token, so you can
watch a run from a phone. It serves only the plain-JSON `events` and `insight`
routes, plus the metrics, activity, status, insight, and viewer Connect
services (`internal/daemon/daemon.go`'s `shareGuarded` map is the exact list).
The viewer service is the typed twin of the run browser - a past run's journal
holds the captured output plus the command that produced it, which a
`magus query output --open` link has always carried in its fragment; the
plain-JSON `outputs`/`output`/`runs`/`run` routes it replaced are retired (see
`docs/concepts/compatibility.md`). `--open` (on `magus run`, `magus affected`,
and `magus query output`) points a browser at the hosted log viewer by
default; `MAGUS_LOG_VIEWER_URL` overrides that base for a self-hosted mirror,
the same override `magus query output --open --url` takes as a flag
(`cmd/magus/live.go`, `cmd/magus/query.go`). The tool Connect service is deliberately
excluded even though it is read-only: it execs the argv a workspace's spells
declare, which is not something a link handed to a phone should reach. `graph`,
`diff`, `diff/patch`, `diff/session`, `plan`, `ledger`, `/mcp` and the job
service are deliberately loopback-only too: a working diff is unreviewed
source, a plan names every target in the workspace, and a share link is a URL
handed to a phone. A leaked share link reaches the small set and nothing else.

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
service, `magus.job.v1alpha1.JobService`, so a browser client (or the CLI) can trigger
background maintenance without an open action endpoint. It is the only surface
that changes anything magus computed - the others record a person's own review
state, open a share listener, or edit their handoff journal - and it is bounded:
it submits a fixed set of named jobs, never an arbitrary command.

The service exposes two RPCs, not one per job: `RunJob(name)` submits any
registered job by its resource name (`jobs/{job}`), and `ListJobs` reports every
job's running state, last run, and target size. A job name nobody registered is
a NotFound error rather than a third RPC.

| Job                 | Effect                                               |
| ------------------- | ---------------------------------------------------- |
| `sync-graph`        | Reconcile the knowledge graph to current source      |
| `rotate-activities` | Trim the activity trail to its cap                   |
| `rotate-logs`       | Trim the invocation run-log journals to their cap    |
| `clear-cache`       | Invalidate cached build entries                      |
| `check-review`      | Note when a review this tree took part in has merged |

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

```sh
magus config token print
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
`magus graph export --open --serve` instead, which runs an ephemeral loopback server
with a matching same-origin response and opens the graph via a `#src=` fragment
that is served directly.

## Kill switch

Disable the console in `magus.yaml`:

```yaml
console:
  enabled: false
```

Or via environment variable: `MAGUS_CONSOLE_ENABLED=false`.

## Privacy statement

The console serves your workspace graph over loopback. It does not:

- Send data to any external service
- Log request payloads
- Store anything beyond what the daemon already caches on disk, plus the review
  state a person records through `POST /api/v1/diff/session`
- Accept a write into your working tree, or edit a file or change
  configuration. It CAN run a target - `POST /api/v1/diff/run` - but only one
  the magusfile declares, the same as `magus run` at a terminal
- Expose any path outside the routes listed above

The hosted explorer page loads your graph via the bearer-authenticated fetch.
The graph data never appears in a URL (fragments are used for the fragment
delivery mode; the live mode uses an Authorization header that browsers do not
log in the address bar).

## `magus doctor` check

`magus doctor` reports console reachability when the daemon is running:

```text
[pass] console: reachable at http://127.0.0.1:7391/api/v1/graph
    bearer token: magus config token print
```

When the daemon is not running, the check is skipped (not a failure).
When `console.enabled: false` is set, the check reports that the console is
disabled.

## Live mode pairing

`magus graph export --open --follow` opens the explorer connected to the running daemon.

### How to pair

1. Start the daemon: `magus server start`
2. Run `magus graph export --open --follow` (or `--follow --print` to copy the URL)
3. The explorer shows a `live: <workspace>` badge and updates within seconds of file changes

The link is served from the daemon's own loopback origin, e.g.
`http://127.0.0.1:7391/console/graph/#token=<bearer>` (the origin names which daemon;
the token rides the fragment). The page:

- Confirms its own origin is literally `127.0.0.1` or `[::1]` before making any fetch
- Consumes the token and strips it from the URL via `history.replaceState`
- Stores the token in sessionStorage (tab lifetime) unless you tick "Remember this workspace", which moves it to localStorage

Zero-arg default: a plain `magus graph export --open` with no flags checks if the daemon is running. If it is, it automatically picks `--follow`. Otherwise it falls back to the `#data=` fragment.

### Two-state model

The explorer has exactly two source states:

| State    | Badge                    | What it means                                             |
| -------- | ------------------------ | --------------------------------------------------------- |
| snapshot | `snapshot: <provenance>` | Data from fragment/file/demo/--serve; frozen at load time |
| live     | `live: <workspace>`      | Data from the daemon; refreshes on file changes           |

"Connected but stale" is impossible: when the SSE stream disconnects, a banner appears ("disconnected - showing workspace as of HH:MM, reconnecting...") and auto-reconnect runs with exponential backoff (1s to 30s). The data stays visible while reconnecting.

### Safari limitation

Safari blocks fetch requests from an HTTPS page to `http://127.0.0.1` (mixed content). Live mode cannot connect in Safari. Use `magus graph export --open --serve` instead: it runs an ephemeral loopback server and opens the graph via a `#src=` fragment that is compatible with Safari's same-origin restriction.

### Target graph in live mode

`magus graph export --open --follow --targets` opens the live target dependency graph:
`http://127.0.0.1:7391/console/graph/#token=<bearer>&flavor=targets`

### Affected view

When the daemon has computed an affected set (from `magus affected` in a CI context), the pool in the `magus.status.v1alpha1.StatusService/GetStatus` response carries an `affected` array of node ids. The "What does my diff touch?" view is enabled automatically and paints those nodes.

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
   drag-and-drop, or `magus graph export --open`'s default fragment) or a loopback
   source (`magus graph export --open --serve` / `--follow`) instead.

### Claim: your graph never appears in any network request

When you use `magus graph export --open`, your graph travels in the URL **fragment**
(the part after `#`). Browsers never include fragments in HTTP requests -
that's the HTTP standard, not our promise.

1. Open DevTools (`F12`) -> **Network** tab. Tick **Preserve log**.
2. Load your graph: run `magus graph export --open` in your workspace, or drag a
   `graph.json` onto the [console's Graph Explorer](https://eli.gladman.cc/magus/console/).
3. Read the request list. Every row is a `GET` for a static file from this
   site's own origin (or, in live mode, your own loopback address). Click any
   row - the **Payload** tab is absent (no request carries a body). Compare
   any request's URL against your address bar: the `#data=...` portion
   appears in none of them.
4. Type `method:POST` into the Network filter box: zero results for the
   snapshot flow these steps describe. In live mode there is one exception, and
   it goes to your own machine: the typed daemon services (status, activity,
   metrics) are Connect RPCs, and Connect sends a read as a POST. Those requests
   are addressed to `127.0.0.1` and carry a request message, never your graph -
   the same `connect-src` policy above is what confines them to loopback.

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
   disconnected (`docs/src/site/offline-badge.ts`).

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

This site is generated from the open [magus repository](https://github.com/egladman/magus)
by a CI-checked build; the served bytes are not, themselves, committed.
`console/gen/` (the console's build output) is entirely gitignored - `git
ls-files console/gen` lists nothing - and `docs/gen/` (the docs site) commits
only a handful of carve-outs (the installer script, the signed release and
registry manifests), not the rendered HTML/JS. So "compare against the repo's
committed copy" is not something you can do against a checkout as it sits;
what you can do:

- Build it yourself from source (`magus run build docs` for the docs site;
  `console/README.md` for the console's own pnpm build) and diff the output
  against what the site serves.
- Read the CI workflow that publishes it
  (`.github/workflows/cd.yaml`) to confirm the published bytes are exactly
  what that build produces from the commit it ran on, with nothing hand-edited
  in between.

`site-manifest.sha256` (at the site root, e.g.
`https://eli.gladman.cc/magus/site-manifest.sha256`) lists every served file
with its SHA-256, in `sha256sum(1)` format, so a rebuild-and-diff has something
to check against without re-downloading every asset:

```sh
curl -s <asset-url> | sha256sum
```

The JavaScript is unminified enough to read; start at the console's
`console/src/console/graph/main.ts` - `loadGraph` and `readGraphFile` are the
functions that ingest a graph (the `#data=`/`#src=`/demo fallback chain, and
drag-drop/file-input/`launchQueue` respectively), and there is no function
that sends it out.

A locally built console is not limited to the hosted site's copy: the daemon's
LAN share listener (`POST /api/v1/share`) and its own `/console/` mount both
resolve which built console to serve via `resolveConsoleDir`
(`internal/daemon/share.go`), which honors `MAGUS_CONSOLE_DIR` as an override
before falling back to `<workspace root>/console/gen`. Point it at a console
you built and audited yourself to serve that copy instead of trusting any
prebuilt one.

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

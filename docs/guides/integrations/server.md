---
title: The broker and the server
description: magus runs up to two background processes. The broker holds this host's capacity and shared services and a run starts it; the server serves MCP, the console and jobs and a person starts it.
tags:
  [
    broker,
    server,
    concurrency,
    magus server,
    magus broker,
    magus status,
    socket,
    pool,
    magus.yaml,
    shared services,
    keep-warm,
    health,
    liveness,
    readiness,
    probes,
    kubernetes,
  ]
aliases: [guides/daemon, guides/integrations/daemon]
---

# The broker and the server

magus runs up to two background processes, each named for what it holds and each with
one lifetime rule:

| Process        | Holds                                                                                        | Started by                 | Exits                              | Listens on                                |
| -------------- | -------------------------------------------------------------------------------------------- | -------------------------- | ---------------------------------- | ----------------------------------------- |
| `magus broker` | this host's capacity (slots and declared `memory_mb`) and the shared services runs keep warm | any run, when none answers | ten minutes after it holds nothing | `$XDG_RUNTIME_DIR/magus/broker.sock` only |
| `magus server` | MCP, the console, the APIs, background jobs, the graph and symbol watch, warm workspaces     | `magus server start`       | when you stop it                   | `server.sock` and HTTP on `mcp.address`   |

`ps` tells them apart by argv: `magus broker` and `magus server --foreground`.

## Concurrency

Magus runs project builds in parallel up to a configurable limit.

```sh
magus run build --concurrency=4
magus config set key=concurrency,value=4
MAGUS_CONCURRENCY=4 magus run build
```

That limit is per process. The **host capacity** is the one that spans them: every
`magus run` and `magus affected` takes its concurrency slots and its declared
`memory_mb` from the broker, so runs in separate worktrees are refused rather than each
admitting a full machine's worth of work. See
[Concurrency](../../concepts/concurrency.md#across-the-whole-machine-the-budget) and
[MGS3009](../../reference/codes/sandbox/MGS3009.md).

The broker arbitrates capacity; it does not run your work. A top-level `magus run`
executes in your own process and prints to your own terminal. Nested `magus`
invocations still adopt into their parent's pool.

## The broker

A run starts the broker when none answers, and says so once on stderr:

```text
magus: started a broker (pid 48213) to hold this host's capacity; it opens no network listener and exits after 10 minutes holding nothing (`magus broker status` lists it)
```

It loads no workspace, records no telemetry, and listens on its unix socket and nothing
else. Binding that socket is its only lock: two runs that both find no broker both start
one, one binds, and the other exits. It exits once it has held nothing (no claim, no
service with a dependent) for ten minutes; a connection that holds nothing, such as the
server's, does not keep it up.

Each run holds one connection to the broker for its life, and every claim and service
reference it takes rides that connection. A run that exits, crashes or is killed with
`SIGKILL` closes it, and the broker releases what it held at once. When the broker itself
dies with runs still going, each re-asserts its claims on the next broker to answer, so
the new one counts the memory those steps are using.

```sh
magus broker status             # capacity, every claim holding it, its services
magus broker stop --services    # stop the services it hosts, leave it running
magus broker stop               # stop it; running steps keep going
magus broker                    # run one in this process, logging to stderr
magus broker --log FILE         # the same, appending to FILE (a run passes broker.log)
```

`magus broker status` exits non-zero when none is running, so a script can chain on it.

The broker answers signals the way a supervisor expects:

| Signal                        | The broker                                                                                                                                                     |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SIGHUP`                      | reopens its `--log` file, so a log rotator can move the old one aside                                                                                          |
| first `SIGTERM`               | drains: turns away every new claim and service reference, naming itself as shutting down, and exits once the runs holding it finish or `shutdown_grace` passes |
| second `SIGTERM`, or `SIGINT` | stops its services and exits now                                                                                                                               |

A run the drain turns away is refused under `broker: required` and runs unarbitrated under
`best-effort`, each with the draining broker's pid in the message. `magus broker status`
shows a `draining` row meanwhile. Runs still holding claims when it exits keep going and
re-assert them on the next broker.

The `broker` setting in `magus.yaml` (also `--broker` and `MAGUS_BROKER`) decides what a
run does about it:

| `broker:`               | A run with no broker answering                                                                               |
| ----------------------- | ------------------------------------------------------------------------------------------------------------ |
| `best-effort` (default) | starts one; if none answers, runs unarbitrated and says so once                                              |
| `required`              | starts one; if none answers, refuses the step ([MGS3022](../../reference/codes/sandbox/MGS3022.md), exit 69) |
| `off`                   | never starts or asks one; services run in the run's own process                                              |

## The server

The server is what a person asks for. It serves MCP and the console, the APIs behind
them, background jobs (`magus job run`) and scheduled maintenance, and keeps
each workspace's knowledge graph and symbol indexes current whether or not MCP is
enabled. It asks the broker for capacity like any run.

```sh
magus server start          # detaches and returns once it is accepting
magus server start --foreground # blocking, for a supervisor
magus server stop           # graceful shutdown; waits for in-flight work
magus server status         # the server's rows, pool and listeners; non-zero when down
magus status                # the broker, the server, the pool, MCP endpoint health
magus status -W 15s         # poll and reprint every 15 seconds
```

`magus server start` detaches by default: it spawns `magus server --foreground`, waits
until it is accepting, prints the pid, and returns 0, including when one is already
running, so a script can chain on it. Pass `--foreground` to run it blocking in the
current process, which is what a supervisor wants (see
[Keeping the server running](#keeping-the-server-running)). A detached server logs to
`$XDG_STATE_HOME/magus/server.log`, which survives logout; under `--foreground` it logs to
stderr for the supervisor to keep.

| Signal                      | The server                                                                                          |
| --------------------------- | --------------------------------------------------------------------------------------------------- |
| `SIGHUP`                    | reloads configuration, the same as `magus server reload`                                            |
| first `SIGTERM` or `SIGINT` | closes its socket, cancels the runs it adopted, and waits up to `shutdown_grace` for them to unwind |
| a second one                | exits now                                                                                           |

`shutdown_grace` (default `5m`, `MAGUS_SHUTDOWN_GRACE`) bounds both processes' stop, and
`0` stops at once; a negative value is a configuration error. The server cancels its runs
because it owns them; the broker lets its holders finish because it only arbitrates them.

## Detaching a run

`magus run --detach` and `magus affected --detach` start the same command again in the
background, as its own session with no terminal, and return at once:

```text
magus: detached as pid 48301; its output goes to /home/you/.local/state/magus/detached/20260924T140201-1234.log
```

The detached run is an ordinary run: it takes its own broker connection for its claims,
and needs no server. Follow it with `tail -f` on that log, or `magus broker status` to see
what it holds. The log directory is `$XDG_STATE_HOME/magus/detached/`, one file per run.

## Two transports

The server has two listeners, one per audience. Its **Unix domain socket**,
`<sock-dir>/server.sock`, speaks HTTP, and everything a local process asks of the server
is a path on it:

| Path                                  | What it does                                                        |
| ------------------------------------- | ------------------------------------------------------------------- |
| `POST /proc/v1/run`                   | run a forwarded or adopted command in the server's pool, and wait   |
| `POST /proc/v1/jobs`                  | start a background job and return its invocation at once            |
| `GET /proc/v1/status`                 | the pool, its running calls, the workspaces held, the listeners     |
| `POST /proc/v1/reload`                | drop the workspaces held open so the next command re-reads config   |
| `POST /proc/v1/shutdown`              | stop the server, as `magus server stop` does                        |
| `/mcp`                                | [MCP](mcp.md#mcp-over-the-server-socket), when `mcp.enabled` allows |
| `/magus.<pkg>.v1alpha1.<Service>/...` | every Connect API the console uses                                  |

It takes no token. The socket sits in a private (`0700`) directory, and on Linux and macOS
the server also asks the kernel for each connection's peer uid and admits only its own;
anyone else gets `403` [MGS9022](../../reference/codes/auth/MGS9022.md). An admitted
caller holds the `socket-peer` credential, `mcp=write` and `console=write`, and each path is
held to the same need as on loopback. Where the platform cannot name a peer, the directory
is the only boundary and the socket carries the `/proc/` paths alone. A per-process pool's
`magus-<pid>-<rand>.sock` speaks the same `/proc/` paths.

An **HTTP server** on `mcp.address` serves the clients that cannot reach a Unix socket:
the console and other browsers, agents that take only a URL over [MCP](mcp.md) at `/mcp`
with a bearer token, and orchestrators or scripts at the `/livez`, `/readyz`, and
`/healthz` probe routes.

A magus from before the socket spoke HTTP cannot talk to this one. A command that meets a
server started by an older magus says so with
[MGS3025](../../reference/codes/sandbox/MGS3025.md) and names the restart.

<!--diagram:server-socket-->

<!--diagram:server-http-->

The Unix socket is the source of truth for server state; the HTTP health routes are a thin
wrapper that answers by querying that same socket. But only an actual HTTP request proves
the agent-facing endpoint is bound and serving - a socket check cannot detect an HTTP bind
failure - so the two listeners can diverge, which is why `magus status` reports each
separately.

This page zooms in on the two transports; the HTTP server also carries the agent-facing
[MCP tools](mcp.md), the read-only [console routes](../../reference/console.md) the browser apps use
(all reading the same warm [knowledge graph](../../concepts/knowledge.md)), and one bearer-gated
[job-control service](../../reference/console.md#job-control) for maintenance jobs - the server's only
mutating HTTP surface. The full-system view - clients,
guards, shared state, and the progressive web app - is the architecture diagram in the
[README](https://github.com/egladman/magus#architecture).

## Sharing the console to a phone

The console can serve a read-only live view to a phone on the same network. The
"Share to phone" action in the console opens a small dialog with a QR code; a phone
that scans it loads the console and gets a read-only view of the dashboard, status,
activity, and logs. It is opt-in and off by default: no listener faces the network
until you click it.

How it works, and where the guards sit:

- **Loopback-only trigger.** The share button POSTs `/api/v1/share` on the server's
  loopback HTTP server. That route requires the local peer and a token holding
  `console=write`, so only the already-authenticated console on your own machine can
  open a share. A network client cannot reach it.
- **Ephemeral, time-boxed LAN listener.** On success the server picks the machine's
  private LAN IPv4, binds a NEW listener on an ephemeral port, and serves ONLY the read
  surface there: the console static assets, the read JSON routes
  (`status`, `events`, `insight`, `outputs`, `output`), and the read-only Connect
  services (activity, metrics). It does NOT serve `/mcp`, the share endpoint, or any
  mutating route. The listener closes when the share expires (15 minutes by default,
  at most 24 hours; a longer request is refused), and on server shutdown.
- **Short-lived, read-only token.** Each share mints a fresh `mgl_` token holding
  `console=read`, kept in server memory as a hash. The listener is bound 1:1 to that
  one token: it accepts nothing else. The operator token, stored tokens, an expired
  share token, and a share token from any previous share are all rejected, so the
  operator secret never crosses the LAN. Every share supersedes the last: a repeat
  click revokes the old token and closes its listener before returning a new one.
- **The loopback server never accepts a share token.** It refuses the `mgl_` class
  outright, before any lookup.
- **Same-origin, so CORS never engages.** The phone loads the console FROM the share
  listener, then its data fetches go back to the same origin. No cross-origin request is
  made, so the server's CORS middleware is never involved and is untouched by this
  feature. The standing loopback server stays bound to `127.0.0.1` as before.

What a leaked QR or URL is worth: at most a few minutes of read-only visibility into
one workspace's dashboard, status, activity, and logs, from a device already on your
LAN, and only until the share expires or you open a new one (which kills the old token).
It cannot run a build, mutate the cache, reach `/mcp`, or read anything the loopback
console does not already show. There is no standing exposure: when the timer fires the
listener is gone and the token validates nowhere.

## Health: what `magus status` reports

`magus status` is the health surface. It prints one row per fact, record type first and
pid second, so `awk` and `xargs` work on it without a parser:

```text
broker    48213  up 42m  proto 1  /Users/eli/src/magus-a/magus  unix:///run/user/501/magus/broker.sock
capacity  -      slots 6/10  mem 18.0 GiB/48.0 GiB  broker: best-effort
held      48190  slots 4  mem 12.0 GiB  api test  /Users/eli/src/magus-a  magus affected ci  since 14:02
service   -      postgres-15  running  deps 2  ports 5432  since 13:58
idle      -      exits after 10m0s holding nothing
server  47001  up 2h0m  /Users/eli/src/magus-a/magus  unix:///run/user/501/magus/server.sock
listen  47001  http 127.0.0.1:7391
watch   47001  graph+symbols  /Users/eli/Repos/magus
```

With nothing running each process says what would start it:

```text
broker   -      not running (a run starts one; broker: best-effort)
server   -      not running (`magus server start` serves MCP and the console)
```

It also reports the **MCP endpoint** (`mcp endpoint` block with `state: serving |
not-ready | unreachable | disabled`) - the HTTP endpoint agent hosts connect to. Nothing
starts this on its own, so an `unreachable` MCP endpoint is the usual reason "the magus
tools disappeared" from an agent. See [MCP](mcp.md#is-mcp-actually-reachable).

For scripts and Kubernetes probes, `magus status --probe=<kind>` exits `0` healthy / `1`
unhealthy. The kinds are `liveness` (the server answers), `readiness` (a workspace is
loaded), and `mcp` (the MCP endpoint is reachable), and they are comma-combinable -
`--probe=liveness,mcp` fails if either the server or the endpoint is down. The server also
serves `/livez`, `/readyz`, and `/healthz` over HTTP on the MCP port.

## Kubernetes and container probes

The server exposes both HTTP endpoints (for `httpGet` probes) and an exec form (for `exec`
probes), split along the standard liveness/readiness lines:

| K8s probe   | endpoint                        | passes when                                     |
| ----------- | ------------------------------- | ----------------------------------------------- |
| `liveness`  | `GET /livez` (alias `/healthz`) | the server answers - independent of warm-up     |
| `readiness` | `GET /readyz`                   | the server answers AND a workspace is loaded    |
| `startup`   | `GET /livez`                    | the server has come up (reuse the liveness URL) |

Liveness is deliberately **independent of warm-up state**: it returns `200` as soon as the
server answers, even before any workspace is loaded, so a slow first index never crash-loops
the pod. Readiness gates on a loaded workspace, and `GET /readyz?workspace=<root>` pins it to
one specific workspace (returns `503` until that workspace is warm). Because these endpoints
are served by the MCP HTTP server itself, a successful probe also proves the MCP endpoint is
listening.

The **status code is the signal** - a kubelet reads only that. Because these routes are
unguarded, their bodies carry nothing identifying: `/livez` and `/healthz` answer a bare
`ok` or `unavailable`, and `/readyz` returns a JSON `{"ready": ..., "components": [...]}`
where each component has a coarse `status` (`ok`, `degraded`, `down`, `disabled`) plus a
generic, quantitative `detail` such as `1 loaded` or `0 of 4 up to date` - counts and state
phrases only, never workspace roots, project or service names, filesystem paths, or the
server PID. For the identifying per-subsystem view (workspace roots, per-project
symbol-index freshness, named service state), read the bearer-authenticated
`magus.status.v1alpha1.StatusService/GetStatus` Connect route instead (the
typed replacement for the retired `GET /api/v1/status`; `magus status` reads
it the same way).

The endpoints bind to `127.0.0.1` by default, which the kubelet cannot reach; set
`MAGUS_MCP_ADDRESS=0.0.0.0:7391` (or `mcp.address`) so probes can hit the pod IP.

```yaml
# pod spec
env:
  - { name: MAGUS_MCP_ADDRESS, value: "0.0.0.0:7391" }
startupProbe:
  httpGet: { path: /livez, port: 7391 }
  failureThreshold: 30
  periodSeconds: 2
livenessProbe:
  httpGet: { path: /livez, port: 7391 }
  periodSeconds: 10
readinessProbe:
  httpGet: { path: /readyz, port: 7391 }
  periodSeconds: 10
```

The endpoint requires no auth token (health routes are exempt); keep the port on the pod
network, not the public internet - see [MCP security](mcp.md#security-keep-this-local).

## Keeping the server running

The server is a local process, and the MCP endpoint is only up while it runs. Pick one way
to keep it alive. The broker needs none of this: a run starts it.

**A shell profile (simplest, good while iterating on magus itself).** Ensure a server is up
whenever you open a shell by adding this to `~/.zprofile`, `~/.bashrc`, or equivalent:

```sh
magus server status >/dev/null 2>&1 || magus server start
```

It is a no-op when a server is already running. This is the least durable option, but it
needs no system integration and always runs whatever `magus` is on your `PATH`, which is
convenient when you rebuild magus often.

**A systemd user service (Linux).** Supervised, survives logout and reboot:

```ini
# ~/.config/systemd/user/magus.service
[Unit]
Description=magus server

[Service]
ExecStart=%h/.local/bin/magus server --foreground
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
systemctl --user enable --now magus
loginctl enable-linger "$USER"   # keep it running when you are not logged in
```

**A launchd LaunchAgent (macOS).** The launchd equivalent, loaded at login and kept alive:

```xml
<!-- ~/Library/LaunchAgents/com.magus.server.plist -->
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.magus.server</string>
    <key>ProgramArguments</key>
    <array>
      <string>/usr/local/bin/magus</string>
      <string>server</string>
      <string>--foreground</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
  </dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/com.magus.server.plist
```

Point `ProgramArguments` at your actual `magus` path (`which magus`). With a supervised
service, restart it after upgrading magus (`systemctl --user restart magus` /
`launchctl kickstart -k gui/$UID/com.magus.server`) so it runs the new binary.

## Hosting shared services

The broker is the host for **shared services** (see [services.md](../../concepts/services.md)).
When a `magus run` needs a [service op](../../concepts/operations.md) as a dependency, the
broker starts that service once and keeps it **warm** across separate invocations, so a
shared Postgres stays up between `magus run test:a` and a later `magus run test:b`
instead of restarting each time. Services are keyed by a configuration fingerprint, so
identical definitions in different projects resolve to one instance. Under `broker: off`,
or when the broker cannot host one, a run hosts the service in its own process for its
own life.

The broker reaps a service after an idle window once its last dependent releases (a
30 minute default, overridable per service via `Service.idle`), and it records each
hosted service's stop command so a **new broker reaps orphans** a previous one left
behind if it was killed uncleanly. A run's references ride its broker connection, so a
run killed mid-build no longer leaves a service with a dependent that never releases.
`magus broker stop --services` clears every hosted service on demand (to drop stale
state or free held ports) without stopping the broker.

## Configuring the server's socket address

The server's socket address is resolved in priority order:

1. `--server-address <unix://...>` flag
2. `MAGUS_SERVER_ADDRESS` environment variable
3. `server.address` in `magus.yaml`
4. The default, `unix://<sock-dir>/server.sock`

The default socket is named without a PID so `server stop` and `server status` can find
it without discovery. The broker always listens on `<sock-dir>/broker.sock`.

To pin a socket address in config:

```sh
magus config set key=server.address,value=unix:///run/user/1000/magus/server.sock
```

---
title: The broker and the daemon
description: magus runs up to two background processes. The broker holds this host's capacity and shared services and a run starts it; the daemon serves MCP, the console and jobs and a person starts it.
tags:
  [
    broker,
    daemon,
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

# The broker and the daemon

magus runs up to two background processes, each named for what it holds and each with
one lifetime rule:

| Process        | Holds                                                                                        | Started by                 | Exits                              | Listens on                                |
| -------------- | -------------------------------------------------------------------------------------------- | -------------------------- | ---------------------------------- | ----------------------------------------- |
| `magus broker` | this host's capacity (slots and declared `memory_mb`) and the shared services runs keep warm | any run, when none answers | ten minutes after it holds nothing | `$XDG_RUNTIME_DIR/magus/broker.sock` only |
| `magus server` | MCP, the console, the APIs, background jobs, the graph and symbol watch, warm workspaces     | `magus server start`       | when you stop it                   | `server.sock` and HTTP on `mcp.address`   |

This page calls the second process the daemon; the command that runs it is `magus server`.
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
daemon's, does not keep it up.

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

### Supervising the broker

A run starting the broker is enough on its own. To have a supervisor own it instead,
print the units and install them yourself; magus never writes them:

```sh
magus broker units systemd   # magus-broker.socket and magus-broker.service
magus broker units launchd   # ~/Library/LaunchAgents/magus.broker.plist
magus broker units -o json   # the same, as {supervisor, path, content} records
```

With no supervisor named it prints launchd's on macOS and systemd's elsewhere. Each file
is printed under a `# <path>` header naming where that supervisor reads it, and the
command to load them follows on stderr.

Under systemd the socket unit owns `broker.sock` and starts the broker on the first
connection, which is socket activation: no spawn race and no orphan. The broker takes the
socket over through `LISTEN_PID` and `LISTEN_FDS`, still exits when idle, and systemd
starts it again on the next connection. A handed-over socket is checked strictly: a
malformed variable, more than one socket, anything but a listening unix stream socket,
or a socket bound anywhere but `broker.sock` stops the broker with an error rather than
binding a socket of its own. `magus broker stop` waits for the broker to hang up, not for
the socket to go, since systemd keeps it.

launchd hands a socket over only through `launch_activate_socket`, a C call magus does
not make. The launchd agent therefore starts the broker at login with `--idle-exit 0` and
keeps it alive, and the broker binds `broker.sock` itself. The agent pins `TMPDIR` and
`XDG_RUNTIME_DIR` to the values `magus broker units` saw, because the socket's path is
derived from them and launchd starts agents with an environment of its own.

Both units fit the drain the broker runs on its first `SIGTERM`:

| Concern               | systemd service                      | launchd agent                                          |
| --------------------- | ------------------------------------ | ------------------------------------------------------ |
| how long the drain is | `--shutdown-grace` on `ExecStart`    | `--shutdown-grace` in `ProgramArguments`               |
| wait before `SIGKILL` | `TimeoutStopSec`: the grace plus 60s | `ExitTimeOut`: the grace plus 60s                      |
| who gets `SIGTERM`    | `KillMode=mixed`: the broker alone   | the broker                                             |
| `SIGHUP`, log reopen  | `ExecReload=kill -HUP $MAINPID`      | `launchctl kill SIGHUP gui/$(id -u)/magus.broker`      |
| where the log goes    | stderr, into the journal             | `--log`, with `StandardErrorPath` naming the same file |

The grace is the `shutdown_grace` that `magus broker units` resolved, pinned on the
command line so the broker drains for exactly as long as its supervisor waits; to change
it, print the units again. The 60 seconds past it cover the broker stopping the services
it hosts, which it does only once the drain ends. That is also why systemd's `SIGTERM`
goes to the broker alone: those services share its cgroup, and the runs it is draining
still use them. systemd's own stop timeout defaults to 90 seconds, far short of the
5-minute default grace.

Under systemd the journal keeps the log, so `systemctl --user reload magus-broker` only
matters once you add `--log` to `ExecStart`. The launchd broker writes its log itself so
that a rotator's `SIGHUP` reopens it; `StandardErrorPath` catches anything printed before
it opens the file.

## The daemon

The daemon is what a person asks for. It serves MCP and the console, the APIs behind
them, background jobs (`magus job run`) and scheduled maintenance, and keeps
each workspace's knowledge graph and symbol indexes current whether or not MCP is
enabled. It asks the broker for capacity like any run.

```sh
magus server start          # detaches and returns once it is accepting
magus server start --foreground # blocking, for a supervisor
magus server stop           # graceful shutdown; waits for in-flight work
magus server status         # the daemon's rows, pool and listeners; non-zero when down
magus status                # the broker, the daemon, the pool, MCP endpoint health
magus status -W 15s         # poll and reprint every 15 seconds
```

`magus server start` detaches by default: it spawns `magus server --foreground`, waits
until it is accepting, prints the pid, and returns 0, including when one is already
running, so a script can chain on it. Pass `--foreground` to run it blocking in the
current process, which is what a supervisor wants (see
[Keeping the daemon running](#keeping-the-daemon-running)). A detached daemon logs to
`$XDG_STATE_HOME/magus/server.log`, which survives logout; under `--foreground` it logs to
stderr for the supervisor to keep.

| Signal                      | The daemon                                                                                          |
| --------------------------- | --------------------------------------------------------------------------------------------------- |
| `SIGHUP`                    | reloads configuration, the same as `magus server reload`                                            |
| first `SIGTERM` or `SIGINT` | closes its socket, cancels the runs it adopted, and waits up to `shutdown_grace` for them to unwind |
| a second one                | exits now                                                                                           |

`shutdown_grace` (default `5m`, `MAGUS_SHUTDOWN_GRACE`) bounds both processes' stop, and
`0` stops at once; a negative value is a configuration error. The daemon cancels its runs
because it owns them; the broker lets its holders finish because it only arbitrates them.

## Choosing between the daemon and ad hoc runs

Every magus command works without the daemon, which `magus server start` starts. Run ad
hoc, each command is its own process: it loads what it needs, answers and exits. The
daemon keeps work warm between commands and serves the clients that connect to it. This
section sets out what each mode gives you, so you can decide whether the daemon earns its
place on your machine.

Two things are the same in both modes. A top-level `magus run` or `magus affected`
executes in your own process and prints to your terminal; the daemon does not host it. And
the broker, which holds the host's capacity, starts itself.

### What each mode gives you

| Capability                                      | Ad hoc                                                                                                                                                       | With the daemon                                                                                                                                                                                                                         |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| MCP                                             | stdio only: `magus mcp`, launched by the agent host, opens the workspace it starts in                                                                        | also Streamable HTTP on `mcp.address` and on the daemon's socket, one endpoint several clients share ([which transport when](mcp.md#which-transport-when))                                                                              |
| Console                                         | not available; `magus graph export --open --serve` hands one graph snapshot to the hosted explorer and stops                                                 | served on `mcp.address`, with the printed URL and a token from `magus config console token create`; [sharing it to a phone](#sharing-the-console-to-a-phone) is available                                                               |
| Health probes                                   | `magus status` reports the daemon as not running                                                                                                             | `/livez`, `/readyz` and `/healthz`, and `magus status --probe` ([Kubernetes and container probes](#kubernetes-and-container-probes))                                                                                                    |
| Job ledger (`magus job fork`, `exec`, `exit`)   | works; the ledger is kept per repository and `magus job watch` reads it locally                                                                              | the same, and each job verb prints a console link for the job                                                                                                                                                                           |
| The daemon's own jobs (`magus job run <name>`)  | does nothing and exits 0, so a hook never delays a checkout; the request is recorded as `no-server`                                                          | submitted and run in the background; a job already running is not started twice                                                                                                                                                         |
| Scheduled runs                                  | none; nothing runs on a timer                                                                                                                                | when the pool is idle: `sync-graph` every 6h, `rotate-activities` every 1h, `rotate-logs` every 7d, `prune-preserved` every 24h, `check-review` every 15m; set under `server.maintenance`, `0` disables a job                           |
| Warm workspace and graph reads                  | `magus query`, `magus explain` and `magus refs` open the workspace lazily and read the stored graph; they evaluate the magusfile only if that graph is stale | the same three commands are answered from a workspace the daemon already holds, evicted after `server.idle_ttl` (6h by default)                                                                                                         |
| Symbol indexes                                  | built only when you run `magus graph build` or `magus run <project>::scip`; a lookup against an older index exits 1 with a staleness line                    | re-indexed in the background after sources go quiet, one run at a time and only when nothing else is running (`knowledge.symbol_indexing`)                                                                                              |
| Graph rules in the agent guard                  | `guard.idx` is rewritten by `magus graph build` only; a stale stamp makes those rules stay silent                                                            | also rewritten after each background index run                                                                                                                                                                                          |
| Graph refresh after a checkout, merge or rebase | none; run `magus graph build`                                                                                                                                | a refresh hook installed at `magus server start` runs `sync-graph` after a history change, and the 6h schedule backs it up                                                                                                              |
| Drift notices from the git hooks                | none; hooks installed earlier stay in place and do nothing                                                                                                   | `magus server start` installs post-commit and pre-push hooks in a git tree; each runs `magus job run check-drift`, which notices generated output or formatting the commit left stale, and on a push how many hunks it sends are unread |
| Insight lenses (hotspots, trend, ownership)     | each command walks a bounded slice of the git log itself                                                                                                     | the console's insight route is served from one cached scan                                                                                                                                                                              |
| Review threads on a `magus diff` report         | not listed; the report says thread ids need the daemon, so a plain report never touches the network (`--thread <id>` and the viewer still ask the forge)     | listed by id, read from the daemon's review session                                                                                                                                                                                     |
| The review session a person and an agent share  | the viewer keeps your read marks in a file under the cache directory; nobody else is on the session                                                          | one session for the terminal viewer, the console tab and an agent that joins through the `diff` MCP tool                                                                                                                                |
| `magus run --detach`, `magus affected --detach` | works; the detached run is an ordinary run                                                                                                                   | the same; it forwards to a daemon only if one is already up                                                                                                                                                                             |

A read the daemon cannot answer for the client runs locally instead and still succeeds.
That covers a daemon from another build, a different configuration, and `magus query` or
`magus explain` with `--refresh` or `--global`.

### Pros and cons

In favor of ad hoc:

- Nothing stays resident: no process, no warm memory, no listener, nothing to supervise.
- Nothing to restart after you rebuild or upgrade magus. A daemon from another build
  refuses the work ([MGS3025](../../reference/codes/sandbox/MGS3025.md)).
- Every command reads `magus.yaml` as it stands; the daemon needs `magus server reload`.

Against ad hoc:

- Each command that needs the workspace loads it and evaluates the magusfile again.
- Symbol indexes go stale between builds, and graph lookups exit 1 until you rebuild.
- No console, no shared HTTP endpoint for MCP, no shared review session.
- Nothing runs on a timer, and the git hooks that need the daemon do nothing.

In favor of the daemon:

- A workspace held open answers graph reads without the load.
- Symbol indexes and the graph stay current without a manual step.
- The console, the HTTP MCP endpoint, the health probes and the shared review session.
- Background maintenance and the drift and refresh hooks.

Against the daemon:

- A resident process holds warm workspaces and two listeners while it runs; its size is in
  the startup table below, not yet measured.
- You restart it after a rebuild or upgrade, and reload it after a `magus.yaml` edit.
- Background indexing uses CPU when the pool is idle.
- One more process to keep alive; see [Keeping the daemon running](#keeping-the-daemon-running).

If you run targets from a terminal and nothing else, ad hoc is enough. Start the daemon
when you want the console, an MCP endpoint several clients share, indexes that stay
fresh, or the hooks.

### Startup cost

Every timing in this table reads "not yet measured": nobody has run the commands in the
last column on a named machine yet, and no figure here is an estimate. Replace a cell with
the median of repeated runs when someone does.

`MAGUS_LOG_LEVEL=trace` (or `-vvv`) makes a command print a startup trace on stderr when it
finishes: one line per phase, a total, and a profile of the time spent in each imported
Buzz file. The phase names below are the trace's own.

| Phase                                          | Ad hoc            | With the daemon  | Measure with                                                                                                                                                                    |
| ---------------------------------------------- | ----------------- | ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Process start, before the trace begins         | not yet measured  | not yet measured | `time magus version`                                                                                                                                                            |
| Config load, socket lookup and flag parse      | not yet measured  | not yet measured | `MAGUS_LOG_LEVEL=trace magus ls -o name`: `startup.config_load`, `startup.socket_lookup`, `startup.flag_parse`                                                                  |
| Workspace load and magusfile evaluation        | not yet measured  | not yet measured | the same trace: `magus.open`, and the profile under it for each import                                                                                                          |
| Forward to the daemon                          | not applicable    | not yet measured | the same trace: `startup.forward`                                                                                                                                               |
| Daemon start, until it accepts                 | not applicable    | not yet measured | `time magus server start`                                                                                                                                                       |
| Daemon resident size when idle                 | not applicable    | not yet measured | `ps -o rss= -p <pid>`, with the pid from `magus server status`                                                                                                                  |
| Graph rebuild                                  | not yet measured  | not yet measured | `time magus graph build`; the daemon runs the same work in the background as `sync-graph`                                                                                       |
| Graph read, stored graph current, nothing warm | not yet measured  | not yet measured | `MAGUS_LOG_LEVEL=trace magus query <terms>`: `read.open`, `query.load_graph`; with the daemon, the first read after `magus server stop` and `magus server start`: `read.server` |
| Graph read, workspace warm                     | not applicable    | not yet measured | the same query with the daemon running, a second time: `read.server`                                                                                                            |
| Symbol index, cold (no index built)            | not yet measured  | not yet measured | `time magus graph build`, or for one project `time magus run <project>::scip`                                                                                                   |
| Symbol index, warm (index current)             | not yet measured  | not yet measured | `time magus refs <symbol>`                                                                                                                                                      |
| Time from a source edit to a current index     | until you rebuild | not yet measured | edit a file, then repeat `magus refs <symbol>` until it exits 0                                                                                                                 |

"Not applicable" means the mode has no such phase: an ad hoc command starts cold every
time, so it has no warm read, and it has no daemon to start or to forward to. Of the
commands that load a workspace, `magus query`, `magus explain` and `magus refs` are the
ones a running daemon answers; every other command pays the workspace load itself in both
modes.

## Detaching a run

`magus run --detach` and `magus affected --detach` start the same command again in the
background, as its own session with no terminal, and return at once:

```text
magus: detached as pid 48301; its output goes to /home/you/.local/state/magus/detached/20260924T140201-1234.log
```

The detached run is an ordinary run: it takes its own broker connection for its claims,
and needs no daemon. Follow it with `tail -f` on that log, or `magus broker status` to see
what it holds. The log directory is `$XDG_STATE_HOME/magus/detached/`, one file per run.

## Two transports

The daemon has two listeners, one per audience. Its **Unix domain socket**,
`<sock-dir>/server.sock`, speaks HTTP, and everything a local process asks of the daemon
is a path on it:

| Path                                  | What it does                                                        |
| ------------------------------------- | ------------------------------------------------------------------- |
| `POST /proc/v1/run`                   | run a forwarded or adopted command in the daemon's pool, and wait   |
| `POST /proc/v1/jobs`                  | start a background job and return its invocation at once            |
| `GET /proc/v1/status`                 | the pool, its running calls, the workspaces held, the listeners     |
| `POST /proc/v1/reload`                | drop the workspaces held open so the next command re-reads config   |
| `POST /proc/v1/shutdown`              | stop the daemon, as `magus server stop` does                        |
| `/mcp`                                | [MCP](mcp.md#mcp-over-the-daemon-socket), when `mcp.enabled` allows |
| `/magus.<pkg>.v1alpha1.<Service>/...` | every Connect API the console uses                                  |

It takes no token. The socket sits in a private (`0700`) directory, and on Linux and macOS
the daemon also asks the kernel for each connection's peer uid and admits only its own;
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
daemon started by an older magus says so with
[MGS3025](../../reference/codes/sandbox/MGS3025.md) and names the restart.

<!--diagram:server-socket-->

<!--diagram:server-http-->

The Unix socket is the source of truth for daemon state; the HTTP health routes are a thin
wrapper that answers by querying that same socket. But only an actual HTTP request proves
the agent-facing endpoint is bound and serving (a socket check cannot detect an HTTP bind
failure), so the two listeners can diverge, which is why `magus status` reports each
separately.

This page zooms in on the two transports; the HTTP server also carries the agent-facing
[MCP tools](mcp.md), the read-only [console routes](../../reference/console.md) the browser apps use
(all reading the same warm [knowledge graph](../../concepts/knowledge.md)), and one bearer-gated
[job-control service](../../reference/console.md#job-control) for maintenance jobs, the daemon's only
mutating HTTP service. The full-system view (clients,
guards, shared state, and the progressive web app) is the architecture diagram in the
[README](https://github.com/egladman/magus#architecture).

## Sharing the console to a phone

The console can serve a read-only live view to a phone on the same network. The
"Share to phone" action in the console opens a small dialog with a QR code; a phone
that scans it loads the console and gets a read-only view of the dashboard, status,
activity, and logs. It is opt-in and off by default: no listener faces the network
until you click it.

How it works, and where the guards sit:

- **Loopback-only trigger.** The share button POSTs `/api/v1/share` on the daemon's
  loopback HTTP server. That route requires the local peer and a token holding
  `console=write`, so only the already-authenticated console on your own machine can
  open a share. A network client cannot reach it.
- **Ephemeral, time-boxed LAN listener.** On success the daemon picks the machine's
  private LAN IPv4, binds a NEW listener on an ephemeral port, and serves ONLY the read
  routes there: the console static assets, the read JSON routes
  (`status`, `events`, `insight`, `outputs`, `output`), and the read-only Connect
  services (activity, metrics). It does NOT serve `/mcp`, the share endpoint, or any
  mutating route. The listener closes when the share expires (15 minutes by default,
  at most 24 hours; a longer request is refused), and on daemon shutdown.
- **Short-lived, read-only token.** Each share mints a fresh `mgl_` token holding
  `console=read`, kept in daemon memory as a hash. The listener is bound 1:1 to that
  one token: it accepts nothing else. The operator token, stored tokens, an expired
  share token, and a share token from any previous share are all rejected, so the
  operator secret never crosses the LAN. Every share supersedes the last: a repeat
  click revokes the old token and closes its listener before returning a new one.
- **The loopback listener never accepts a share token.** It refuses the `mgl_` class
  outright, before any lookup.
- **Same-origin, so CORS never engages.** The phone loads the console FROM the share
  listener, then its data fetches go back to the same origin. No cross-origin request is
  made, so the daemon's CORS middleware is never involved and is untouched by this
  feature. The standing loopback listener stays bound to `127.0.0.1` as before.

What a leaked QR or URL is worth: at most a few minutes of read-only visibility into
one workspace's dashboard, status, activity, and logs, from a device already on your
LAN, and only until the share expires or you open a new one (which kills the old token).
It cannot run a build, mutate the cache, reach `/mcp`, or read anything the loopback
console does not already show. There is no standing exposure: when the timer fires the
listener is gone and the token validates nowhere.

## Health: what `magus status` reports

`magus status` is the health command. It prints one row per fact, record type first and
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
not-ready | unreachable | disabled`), the HTTP endpoint agent hosts connect to. Nothing
starts this on its own, so an `unreachable` MCP endpoint is the usual reason "the magus
tools disappeared" from an agent. See [MCP](mcp.md#is-mcp-actually-reachable).

For scripts and Kubernetes probes, `magus status --probe=<kind>` exits `0` healthy / `1`
unhealthy. The kinds are `liveness` (the daemon answers), `readiness` (a workspace is
loaded), and `mcp` (the MCP endpoint is reachable), and they are comma-combinable:
`--probe=liveness,mcp` fails if either the daemon or the endpoint is down. The daemon also
serves `/livez`, `/readyz`, and `/healthz` over HTTP on the MCP port.

## Kubernetes and container probes

The daemon exposes both HTTP endpoints (for `httpGet` probes) and an exec form (for `exec`
probes), split along the standard liveness/readiness lines:

| K8s probe   | endpoint                        | passes when                                     |
| ----------- | ------------------------------- | ----------------------------------------------- |
| `liveness`  | `GET /livez` (alias `/healthz`) | the daemon answers: independent of warm-up      |
| `readiness` | `GET /readyz`                   | the daemon answers AND a workspace is loaded    |
| `startup`   | `GET /livez`                    | the daemon has come up (reuse the liveness URL) |

Liveness is deliberately **independent of warm-up state**: it returns `200` as soon as the
daemon answers, even before any workspace is loaded, so a slow first index never crash-loops
the pod. Readiness gates on a loaded workspace, and `GET /readyz?workspace=<root>` pins it to
one specific workspace (returns `503` until that workspace is warm). Because these endpoints
are served by the MCP HTTP server itself, a successful probe also proves the MCP endpoint is
listening.

The **status code is the signal**: a kubelet reads only that. Because these routes are
unguarded, their bodies carry nothing identifying: `/livez` and `/healthz` answer a bare
`ok` or `unavailable`, and `/readyz` returns a JSON `{"ready": ..., "components": [...]}`
where each component has a coarse `status` (`ok`, `degraded`, `down`, `disabled`) plus a
generic, quantitative `detail` such as `1 loaded` or `0 of 4 up to date`: counts and state
phrases only, never workspace roots, project or service names, filesystem paths, or the
daemon PID. For the identifying per-subsystem view (workspace roots, per-project
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
network, not the public internet; see [MCP security](mcp.md#security-keep-this-local).

## Keeping the daemon running

The daemon is a local process, and the MCP endpoint is only up while it runs. Pick one way
to keep it alive. The broker needs none of this, since a run starts it; to supervise it
anyway, see [Supervising the broker](#supervising-the-broker).

**A shell profile (simplest, good while iterating on magus itself).** Ensure a daemon is up
whenever you open a shell by adding this to `~/.zprofile`, `~/.bashrc`, or equivalent:

```sh
magus server status >/dev/null 2>&1 || magus server start
```

It is a no-op when a daemon is already running. This is the least durable option, but it
needs no system integration and always runs whatever `magus` is on your `PATH`, which is
convenient when you rebuild magus often.

**A systemd user service (Linux).** Supervised, survives logout and reboot:

```ini
# ~/.config/systemd/user/magus.service
[Unit]
Description=magus daemon

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

## Configuring the daemon's socket address

The daemon's socket address is resolved in priority order:

1. `--server-address <unix://...>` flag
2. `MAGUS_SERVER_ADDRESS` environment variable
3. `server.address` in `magus.yaml`
4. The default, `unix://<sock-dir>/server.sock`

The default socket is named without a PID so `magus server stop` and `magus server status` can find
it without discovery. The broker always listens on `<sock-dir>/broker.sock`.

To pin a socket address in config:

```sh
magus config set key=server.address,value=unix:///run/user/1000/magus/server.sock
```

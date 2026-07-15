---
title: Daemon and concurrency
description: The magus daemon holds workspace state and enforces one shared concurrency pool across every client, so parallel CI steps and nested invocations do not oversubscribe the machine.
tags:
  [
    daemon,
    concurrency,
    magus server,
    magus status,
    socket,
    pool,
    magus.yaml,
    shared services,
    keep-warm,
  ]
---

# Daemon and concurrency

## Concurrency

Magus runs project builds in parallel up to a configurable limit.

```sh
magus run build --concurrency=4
magus config set key=concurrency,value=4
MAGUS_CONCURRENCY=4 magus run build
```

When a [daemon](#daemon) is running, all clients share a single concurrency pool. Parallel CI steps and nested `magus` invocations all draw from the same budget.

`magus status` shows the live pool state and current slot usage.

## Daemon

By default every `magus run` is a short-lived process with its own concurrency limiter, so parallel invocations oversubscribe the machine. A daemon holds workspace state in memory and enforces **one** concurrency pool across all clients.

```sh
magus server start &        # foreground process; & or a supervisor backgrounds it
magus server stop           # graceful shutdown; waits for in-flight work
magus server stop --services # stop the daemon's hosted services, leave the daemon up
magus status                # live pool + reachability check (reports the reason when down)
magus status -W 1s          # poll and reprint every second
```

## Hosting shared services

The daemon is also the host for **shared services** (see [services.md](services.md)).
When a `magus run` needs a [service op](operations.md) as a dependency and a daemon is
running, the daemon starts that service once and keeps it **warm** across separate
invocations, so a shared Postgres stays up between `magus run test:a` and a later
`magus run test:b` instead of restarting each time. Services are keyed by a
configuration fingerprint, so identical definitions in different projects resolve to
one instance. A per-process daemon (the one an unattended `magus run` spawns for
itself) does not host services; only the stable `magus server` daemon does, which is
why cross-invocation sharing needs it running.

The daemon reaps a service after an idle window once its last dependent releases (a
30 minute default, overridable per service via `Service.idle`), and it records each
hosted service's stop command so a **new daemon reaps orphans** a previous one left
behind if it was killed uncleanly. `magus server stop --services` clears every hosted
service on demand (to drop stale state or free held ports) without shutting the daemon
down.

## Configuring the socket address

The socket address is resolved in priority order:

1. `--daemon-address <unix://...>` flag
2. `MAGUS_DAEMON_ADDRESS` environment variable
3. `daemon.address` in `magus.yaml`
4. Stable per-workspace default (`unix://<sock-dir>/magus-daemon.sock`)

The default socket is named without a PID so `server stop` and `server status` can find it without discovery.

To pin a socket address in config:

```sh
magus config set key=daemon.address,value=unix:///run/user/1000/magus/daemon.sock
```

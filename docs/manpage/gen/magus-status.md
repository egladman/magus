---
title: magus status
description: Show effective config plus the live concurrency pool state of any running parent magus process, with optional --watch polling and --compact output.
tags: [cli, magus status, status, concurrency, pool, daemon, monitoring]
---

# magus-status

Inspect concurrency pool and configuration

## Synopsis

**magus** status [flags]

## Description

Show the magus configuration that affects this process — telemetry, cache
settings — and, when a parent magus process is running, the live state of its
concurrency pool (current slot usage, queued waiters).

When --watch is non-zero, status polls and reprints at that interval. On a
TTY the screen is cleared between reprints; piped output appends each
snapshot on its own line for log capture.

## Options

**--compact**
: Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)

**--socket** *string*
: Adopt server address as unix:// URL or bare path; default: auto-detect from MAGUS_DAEMON_SOCKET or scan sock dir

**--watch** *duration*
: Poll and reprint at this interval (e.g. --watch=1s); 0 means one-shot

## Examples

*One-shot status snapshot*

```sh
magus status
```

*Live updates every second*

```sh
magus status --watch=1s
```

*Single-line snapshot for a multiplexer sidebar*

```sh
magus status --compact --watch=1s
```

*Inspect a specific running parent*

```sh
magus status --socket=unix:///run/user/1000/magus/daemon.sock
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


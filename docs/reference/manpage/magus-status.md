---
title: magus status
generated_from: internal/cli/registry.go
description: Show effective config plus the live concurrency pool state of any running parent magus process, with optional --watch polling and --compact output.
tags: [cli, magus status, status, concurrency, pool, server, monitoring]
---

# magus-status

Inspect concurrency pool and configuration

## Synopsis

**magus** status [flags]

## Description

Show the magus configuration that affects this process - telemetry, cache
settings - and, when a parent magus process is running, the live state of its
concurrency pool (current slot usage, queued waiters).

When --watch is non-zero, status polls and reprints at that interval. On a
TTY the screen is cleared between reprints; piped output appends each
snapshot on its own line for log capture.

## Options

**-W** *duration*
: Short for --watch

**-c**
: Short for --compact

**--compact**
: Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)

**--probe** *string*
: Exec-probe mode: liveness or readiness (exit 0 healthy, 1 unhealthy; ignores --watch/--compact)

**--socket** *string*
: Proc server to report on, as a unix:// URL or bare path; default: MAGUS_PROC_SOCKET inside a run, else every live one in the socket dir. --probe asks the server at server.address unless this names one

**--symbols**
: Include the expensive symbol-index freshness scan

**--watch** *duration*
: Poll and reprint at this interval (minimum 15s; 0 means one-shot)

**--workspace** *string*
: Workspace root to check for readiness with --probe=readiness (default: any loaded workspace)

## Examples

*One-shot status snapshot*

```sh
magus status
```

*Live updates every 15 seconds*

```sh
magus status --watch=15s
```

*Single-line snapshot for a multiplexer sidebar*

```sh
magus status --compact --watch=15s
```

*Inspect a specific running parent*

```sh
magus status --socket=unix:///run/user/1000/magus/server.sock
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


---
title: magus server
generated_from: internal/commands/registry.go
description: "Start, stop, reload or report the magus server a person starts (MCP over HTTP, the console, the APIs, background jobs and the warm graph and symbol watch) and the broker that holds this host's capacity."
tags: [cli, magus server, server, broker, mcp, console, socket, persistent]
---

# magus-server

The background processes: the server (MCP, the console, APIs, jobs) and the broker

## Synopsis

**magus** server \<start|stop|status|reload|broker\> [flags]

## Description

Start, stop, reload or report the magus server, and check or stop the broker.

The server is the background process a person asks for. It serves MCP and the
console over HTTP, the APIs behind them, background jobs and scheduled
maintenance, and keeps each workspace's knowledge graph and symbol indexes
current. It keeps workspaces warm, so nested magus calls that forward to it pay
for discovery and config once. Nothing under \`magus server\` starts it but
\`server start\` (elsewhere, \`graph export --follow\` asks for the console
by name), and it runs until stopped.

It is not what holds this host's capacity: that is the broker, which a run
starts on its own; \`magus server broker\` checks or stops it. The server asks
the broker like any run does.

Each verb's help line says whether it needs the server running.

The socket address is resolved in priority order:
  --socket flag  \>  MAGUS_SERVER_ADDRESS env  \>  server.address in magus.yaml  \>
  default ($XDG_RUNTIME_DIR/magus/server.sock)

A detached server logs to $XDG_STATE_HOME/magus/server.log; under
--foreground it logs to stderr for the supervisor to keep.

Signals:
  SIGHUP           reload configuration, the same as \`magus server reload\`
  SIGTERM, SIGINT  stop: close the socket, cancel the runs it adopted, and
                   wait up to shutdown_grace (default 5m) for them to unwind
  a second one     exit now

### server start options

**--[no-]foreground** (default: off)
: Run in the foreground and block, instead of auto-backgrounding

### server stop options

**--[no-]pools** (default: off)
: Stop per-process pool parents whose build differs from this binary (leftover magus mcp / runs), leaving a matching server alone

**--socket** *string*
: Server socket (default: config / MAGUS_SERVER_ADDRESS / server.sock)

### server status options

**-W** *value*
: Short for --watch

**-c**
: Short for --compact

**--[no-]compact** (default: off)
: Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)

**--probe** *string*
: Exec-probe mode: liveness or readiness (exit 0 healthy, 1 unhealthy; ignores --watch/--compact)

**--socket** *string*
: Proc server to report on, as a unix:// URL or bare path; default: MAGUS_PROC_SOCKET inside a run, else every live one in the socket dir. --probe asks the server at server.address unless this names one

**--[no-]symbols** (default: off)
: Include the expensive symbol-index freshness scan

**--watch** *value*
: Poll and reprint at this interval (minimum 15s; 0 means one-shot). Bare numbers are seconds.

**--workspace** *string*
: Workspace root to check for readiness with --probe=readiness (default: any loaded workspace)

### server reload options

**--socket** *string*
: Server socket (default: config / MAGUS_SERVER_ADDRESS / server.sock)

### server broker options

**--idle-exit** *value* (default: 10m0s)
: Exit once the broker has held nothing this long; 0 never exits, for a supervisor that keeps it alive. Bare numbers are minutes.

**--log** *string*
: Append stdout and stderr to this file, reopening it on SIGHUP; a run that starts a broker passes $XDG_STATE_HOME/magus/broker.log

### server broker stop options

**--[no-]services** (default: off)
: Stop the broker's hosted services, leaving the broker running

## Subcommands

**start**
: Start the server (auto-backgrounds by default; --foreground blocks)

**stop**
: Send a graceful shutdown request to the running server (needs it running)

**status**
: Whether the server and broker are up, what holds capacity, and the pool (needs neither running)

**reload**
: Re-read configuration without restarting: drop the server's open workspaces (needs it running)

**broker**
: The per-user process holding this host's capacity and shared services (needs no server)

## Examples

*Start the server (auto-backgrounds)*

```sh
magus server start
```

*Run the server in the foreground (supervisor or debugging)*

```sh
magus server start --foreground
```

*Stop the running server*

```sh
magus server stop
```

*Stop leftover pool parents from an older build*

```sh
magus server stop --pools
```

*Reload configuration without restarting*

```sh
magus server reload
```

*Everything running on this host, server up or not*

```sh
magus server status
```

*Run tests once the build budget can seat them*

```sh
magus server status --wait | magus run test .
```

*Is a broker up, and what holds capacity*

```sh
magus server broker status
```

*Use a custom socket path*

```sh
magus --server-address unix:///tmp/m.sock server start
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-run**(1)](magus-run.md), [**magus-affected**(1)](magus-affected.md), [**magus-x**(1)](magus-x.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-query**(1)](magus-query.md), [**magus-refs**(1)](magus-refs.md), [**magus-explain**(1)](magus-explain.md), [**magus-graph**(1)](magus-graph.md), [**magus-diff**(1)](magus-diff.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-session**(1)](magus-session.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-queue**(1)](magus-queue.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-init**(1)](magus-init.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


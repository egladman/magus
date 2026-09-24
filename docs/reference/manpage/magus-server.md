---
title: magus server
generated_from: internal/cli/registry.go
description: "Start, stop, reload or check the magus server a person starts: MCP over HTTP, the console, the APIs, background jobs and the warm graph and symbol watch."
tags: [cli, magus server, server, mcp, console, socket, persistent]
---

# magus-server

Manage the magus server: MCP, the console, APIs and background jobs

## Synopsis

**magus** server \<start|stop|status|reload\> [flags]

## Description

Start, stop, reload or check the magus server.

The server is the background process a person asks for. It serves MCP and the
console over HTTP, the APIs behind them, background jobs and scheduled
maintenance, and keeps each workspace's knowledge graph and symbol indexes
current. It keeps workspaces warm, so nested magus calls that forward to it pay
for discovery and config once. Nothing starts it but \`magus server start\` (and
\`graph export --follow\`, which asks for the console by name), and it runs
until stopped.

It is not what holds this host's capacity: that is \`magus broker\`, which a
run starts on its own. The server asks the broker like any run does.

The socket address is resolved in priority order:
  --socket flag  \>  MAGUS_DAEMON_ADDRESS env  \>  daemon.address in magus.yaml  \>
  default ($XDG_RUNTIME_DIR/magus/server.sock)

A detached server logs to $XDG_STATE_HOME/magus/server.log; under
--foreground it logs to stderr for the supervisor to keep.

### server start options

**--foreground**
: Run in the foreground and block, instead of auto-backgrounding

### server stop options

**--socket** *string*
: Server socket (default: config / MAGUS_DAEMON_ADDRESS / server.sock)

### server status options

**--socket** *string*
: Server socket (default: config / MAGUS_DAEMON_ADDRESS / server.sock)

### server reload options

**--socket** *string*
: Server socket (default: config / MAGUS_DAEMON_ADDRESS / server.sock)

## Subcommands

**start**
: Start the server (auto-backgrounds by default; --foreground blocks)

**stop**
: Send a graceful shutdown request to the running server

**status**
: The server: whether it is up and where you reach it

**reload**
: Re-read configuration without restarting: drop the server's open workspaces

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

*Reload configuration without restarting*

```sh
magus server reload
```

*Everything running on this host*

```sh
magus status
```

*Use a custom socket path*

```sh
magus --daemon-address unix:///tmp/m.sock server start
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


---
title: magus server
description: Start, stop, or check liveness of the persistent magus daemon that keeps workspace discovery, config, and cache warm across invocations.
tags: [cli, magus server, daemon, server, socket, persistent]
---

# magus-server

Manage the persistent magus daemon

## Synopsis

**magus** server \<start|stop\> [flags]

## Description

Start, stop, or check the liveness of a persistent magus daemon.

By default every magus invocation starts a short-lived proc server that dies
when the command exits. The persistent daemon keeps the server alive across
invocations so workspace discovery, config loading, and the content-addressed
cache are paid for once. Nested magus calls (from build scripts, editor
integrations, etc.) forward work to the daemon automatically.

The socket address is resolved in priority order:
  --socket flag  \>  MAGUS_DAEMON_ADDRESS env  \>  daemon.address in magus.yaml  \>
  stable default ($XDG_RUNTIME_DIR/magus/magus-daemon.sock)

The socket file acts as the lock: present means a daemon is running, absent
means none. Shell init hooks (e.g. Nix-injected .profile lines) typically
check for the file with [ -S "$socket" ] before starting one.

## Options

**--foreground**
: Run in the foreground and block, instead of auto-backgrounding (server start)

## Subcommands

**start**
: Start a persistent daemon (auto-backgrounds by default; --foreground blocks)

**stop**
: Send a graceful shutdown request to a running daemon

## Examples

*Start the daemon (auto-backgrounds)*

```sh
magus server start
```

*Run the daemon in the foreground (supervisor or debugging)*

```sh
magus server start --foreground
```

*Stop the running daemon*

```sh
magus server stop
```

*Inspect daemon pool state*

```sh
magus status
```

*Use a custom socket path*

```sh
magus --daemon-address unix:///tmp/m.sock server start
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


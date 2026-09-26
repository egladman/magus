---
title: magus broker
generated_from: internal/cli/registry.go
description: "Check or stop the broker: the per-user process that holds this host's concurrency slots, declared memory and shared services, which a run starts on its own."
tags: [cli, magus broker, broker, capacity, memory_mb, services, concurrency]
---

# magus-broker

The per-user process holding this host's capacity and shared services

## Synopsis

**magus** broker [status|stop|units] [flags]

## Description

The broker holds this host's capacity: the concurrency slots and declared
memory_mb every magus on it shares, and the services runs keep warm between
them. Every run asks it before starting a step; a step that does not fit
alongside what other invocations hold is refused with MGS3009 (exit 75).

A run starts a broker when none answers, and prints one line saying so. It
loads no workspace, records no telemetry and listens on a unix socket only
($XDG_RUNTIME_DIR/magus/broker.sock). It exits once it has held nothing (no
claim, no service with a dependent) for ten minutes; a started broker logs to
$XDG_STATE_HOME/magus/broker.log.

Each run holds one connection to it for its life, and every claim and service
reference rides that connection, so a run killed outright releases what it
held at once. When a broker dies with runs still going, they re-assert their
claims on the next one.

The broker setting in magus.yaml decides what a run does about it: required
refuses a step when none answers (MGS3022, exit 69), best-effort (the default)
runs unarbitrated and says so once, off never starts or contacts one.

Run with no target, it serves in this process and logs to stderr. Under
systemd it takes the socket the supervisor hands over (LISTEN_FDS), refusing
one that is malformed or bound anywhere but broker.sock. \`magus broker units\`
prints the systemd or launchd units for it; magus never installs them.

## Options

**--idle-exit** *duration* (default: 10m0s)
: Exit once the broker has held nothing this long; 0 never exits, for a supervisor that keeps it alive

### broker stop options

**--services**
: Stop the broker's hosted services, leaving the broker running

## Subcommands

**status**
: The broker: its capacity, every claim holding it, and its services

**stop**
: Stop the broker, or with --services only the services it hosts

**units**
: Print the systemd or launchd units that supervise the broker

## Examples

*Is a broker up, and what holds capacity*

```sh
magus broker status
```

*Stop the services it keeps warm*

```sh
magus broker stop --services
```

*Print the systemd units that socket-activate it*

```sh
magus broker units systemd
```

*Never start or ask one, for this run*

```sh
magus run test . --broker off
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


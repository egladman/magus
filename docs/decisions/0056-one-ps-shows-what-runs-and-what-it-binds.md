---
title: "ADR 0056: one ps shows what runs and what it binds"
order: 56
description: "magus gains one verb, ps, that lists the processes magus started and every process holding a listening port, each with its owner and the ports it binds. Ports are a column, not a second verb. A new net.listeners() in the Go net module reads the sockets; the existing MAGUS_PROC_TREE mark ties a process to the run that started it. magus never kills what it did not start."
tags: [adr, decision, cli, processes, ports, services, net]
status: proposed
date: 2026-11-20
---

# ADR 0056: one ps shows what runs and what it binds

## Context

On 2026-11-20 a stray node process on Eli's work laptop held a port. An Nx continuous
target, or a process a Claude Code session started, had left it behind. A later serve target
failed because its stub server expected that port to be free; its author had hardcoded it.
Nothing on the machine said what held the port or who had started it, so finding it meant
`lsof` and guessing.

magus was not involved in that leak. It does, though, track what it starts, and none of
that is visible to a person asking what is bound:

- `std/net.go` is the `net` host module. It answers `is_port_open`, `wait_for_port` and
  `free_port`. A target can already ask for an unused port instead of hardcoding one, but
  nothing lists who holds a port.
- A service op is hosted by the broker
  ([Services](../concepts/services.md)): one instance per configuration fingerprint, reaped
  after an idle window, and reaped on the next broker start when the last one died
  uncleanly. That reap replays the service's `stop` command and never kills a pid.
  `internal/service/journal.go` says why: a `docker run` client pid is not the container, and
  a pid may have been reused by the time a new broker starts.
- The broker records what runs against it (`broker/owner.go`): each `Work` with its pid,
  directory, command and lease, and each process `Group` with its pgid.
- Every step command carries `MAGUS_PROC_TREE=<magus pid>.<seq>`
  (`internal/proc/spawn/tree_bound.go`). A child inherits it whatever its parentage, so a
  process the step left behind still names the run after it is reparented. When the command
  exits, every live process carrying its mark gets SIGTERM, then SIGKILL after `termGrace`.
  The per-OS readers that find marked processes already exist: `tree_bound_darwin.go` reads
  each process's arguments and environment, and `tree_bound_linux.go` reads
  `/proc/<pid>/environ`.
- Two paths drop the mark on purpose. `internal/proc/detach.go` strips it from a broker or
  server that must outlive its run, and `procDetach` in `std/proc.go` strips it from a
  `proc\exec` started with detach, so the reap that ends the run leaves it running.

So magus can tie a process to the run that started it, and can see a listening socket, but no
command puts the two together.

## Decision

### One verb, ps

magus gains one verb, `ps`. It lists the processes magus started and every process that holds
a listening TCP port, and for each one it shows the owner and the ports it binds. A port is a
column of `ps`, not a verb of its own: there is no `ports` command. A new verb is a cost every
reader of the help pays, and one is warranted here, not two.

The verb is not `jobs`. `magus job` already means a forked worktree job with a lease, and a
second word for a different thing beside it would read as a typo.

Rows are grouped by owner, with each process's descendants under it, the way `jobs -l` groups
a shell's children. Output goes through the shared `-o` formats: `json`, `name` and
`template`. `ps` takes no port flag and no positional port; "what holds 4100" is a
`-o template=` or a `-o json` read.

### The columns

| column  | from                                          |
| ------- | --------------------------------------------- |
| pid     | the process table                             |
| ppid    | the process table; 1 means the parent is gone |
| pgid    | the process table                             |
| command | the process table                             |
| dir     | the process's working directory               |
| age     | the process's start time                      |
| ports   | `net.listeners()`, matched by pid             |
| owner   | the tags below                                |

### Owner tags

Each row gets the first tag that matches:

- `service <key>`: the pid or its group belongs to a service the broker hosts.
- `run <lease or command>`: the pid's group matches a live `Group`, or its pid a live `Work`,
  in the broker's record.
- `magus pid <n>, gone`: the process carries a `MAGUS_PROC_TREE` mark, and no live magus
  process matches the pid the mark names. This is the leaked process the reap missed, for
  instance one started before the reap ran or one that outlived a SIGKILLed magus.
- `unknown`: anything else that holds a listening port. The node process from the context
  lands here.

A process that drops the mark on purpose (a detached `proc\exec`, the broker, the server) has
no mark to read. The broker and the server are matched by their own records; a detached
`proc\exec` child shows as `unknown`.

### The mechanism: net.listeners()

The socket read lives in the Go `net` host module as `net.listeners()`, beside
`is_port_open`, `wait_for_port` and `free_port`. It returns one record per listening TCP
socket the user owns: address, port and pid. The Buzz binding and the `net` reference page
are regenerated from it, so a magusfile or script can read the same records `ps` prints.

- macOS reads `lsof -nP -iTCP -sTCP:LISTEN`. lsof ships with every macOS install.
- Linux reads `/proc/net/tcp` and `/proc/net/tcp6` for listening inodes, and each process's
  `/proc/<pid>/fd` links to map an inode to a pid.
- Windows returns an error that says the platform is unsupported, never an empty list.

The mark read reuses the per-OS readers in `tree_bound_darwin.go` and `tree_bound_linux.go`.
They answer one mark at a time today; `ps` needs every mark in one table read, which is a new
query on the same reader, not a second reader.

### magus never kills what it did not start

`ps` only reads. An `unknown` row, and a `magus pid <n>, gone` row, prints the `kill <pid>`
line for the person to run. Stopping a hosted service stays with
`magus server broker stop --services` and the service journal, which replay `stop` commands
and never signal a pid.

### Principles checked

- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): the
  failure is a serve target that broke on a port a forgotten process held, with nothing to
  say which.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md) and
  [Delete before you add](../design/principles/delete-before-you-add.md): one verb, no
  `ports`, no flags of its own past the shared `-o`, no store. Attribution reuses the mark
  and readers that already exist.
- [No silent defaults](../design/judgment/no-silent-defaults.md): Windows fails loudly, and
  a process magus cannot attribute says `unknown` rather than being left out.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md): an
  `unknown` row carries the command to stop it, the way a refusal carries its fix.
- [Human in the loop](../design/principles/human-in-the-loop.md): magus computes the table
  and the person decides what dies.
- [Everything is a file](../design/principles/everything-is-a-file.md): `ps` writes no
  state. The broker's records and the mark carry it.
- [A selector does one thing](../design/principles/a-selector-does-one-thing.md): `ps`
  has no port selector beside `-o`.
- [A record of refusals](../design/refusals.md): this adds no row and reopens none.

## Alternatives

### A separate ports verb

`magus ports` beside `magus ps`, each grouping the same rows its own way. Rejected: two verbs
over one view double the help and the completions for a pivot `-o` already gives. Eli ruled on
2026-11-20 that ports fold into `ps`.

### Name it jobs

`magus jobs`, after the shell builtin. Rejected: `magus job` already names a forked worktree
job, and `job` and `jobs` meaning different things is a trap.

### Record port bindings in a store

Each step appends its pid, port, workspace and target to a JSON Lines store, and `ps` reads it back.
Rejected: the inherited mark already ties a process to its run and survives reparenting, and a
store records a pid that may have been reused by the time anyone reads it, the same hazard the
service journal avoids.

### Reap leaked processes

Have `ps`, or a flag on it, kill every `magus pid <n>, gone` or `unknown` row. Rejected:
magus computes and refuses, it does not act on a person's machine, and a pid is not a safe
handle for a process magus did not start. The leftover reap at command exit already covers
what magus owns while it can still prove it.

### List ports from a Buzz script

Ship the listing as a `hack/` or `std/lib` script that shells out to `lsof`. Rejected: the
read differs per OS and needs the process table, which the Go module already holds; a script
would carry the platform split that belongs below the magusfile.

## Consequences

- `ps` is new in the command tree (`internal/commands`); its flags, help, completions and man
  page come from that one source.
- `net.listeners()` is new in `std/net.go`; the Buzz binding, the `net` reference page and the
  std API lock regenerate.
- The tree-mark readers gain a query for every mark in one table read.
- A process detached through `proc\exec` shows as `unknown`. That is correct, and it means a
  magusfile that detaches a dev server gets no attribution for it.
- A non-magus process, like the Nx leftover in the context, is listed with its port, parent,
  directory and age, which is enough for the person to recognize it.

## Open questions

- The mark names a magus pid and a sequence, not a project or target. For a live run the
  broker's `Work` record maps it. For a run that is gone, either the broker keeps a short
  record of mark to target, or the mark itself grows the target. The lean is the broker.
- Which rows show by default: magus-owned trees and every listener holder, as above, or every
  process the user owns.
- Whether a positional port argument (`ps 4100`) is ever worth a second selector.
- Windows support for `net.listeners()`.
- Two later pieces need their own record if they add behavior: a refusal that names the
  holder when a declared port is taken, and a notice when `free_port` hands out a different
  port than the one a target last used.

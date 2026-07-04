---
title: "MGS5002: service op detaches"
description: Fires when a service op's process command detaches (docker run -d), which forks the process away from magus and breaks foreground supervision, stdout capture, readiness probing, and graceful stop.
tags: [MGS5002, services, service op, docker, detach, supervision, ward]
---

# MGS5002: service op detaches

A [service op](../../operations.md) returned a `Service` whose process command
detaches - `docker run -d` / `--detach` (or a combined short-flag block like
`-itd`). magus rejects it at resolution, before anything forks.

```text
[MGS5002] service op "db" detaches with "-d": magus forks a service in the foreground and supervises it, so detaching breaks stdout capture, readiness, and stop. Drop the detach flag, or make this a command op if you really want it detached.
  see: …/MGS5002.md
```

## Why

magus runs a service in the **foreground** and supervises it: it blocks on the
process, captures its stdout and stderr, polls the readiness probe against it, and
sends the stop command to shut it down. A detached process forks away from magus
immediately, so all four of those become meaningless - magus is left blocking on a
launcher that already exited, supervising nothing.

This is not a style preference; it is a self-contradiction between the op's kind
(a long-running, supervised service) and its argv (fire-and-forget). So it is an
error with no flag-level suppression: the resolution is to change the shape, not to
silence the check.

The check is scoped to container runtimes (`docker`, `podman`, `nerdctl`) because
`-d` is tool-specific: `dnsmasq -d`, for example, means the opposite (stay in the
foreground) and is never flagged.

## Fix

- Drop the detach flag. `docker run` (no `-d`) stays in the foreground and streams
  logs, which is exactly what a supervised service wants.
- If you genuinely want a fire-and-forget process that magus does not supervise,
  model it as a command op (which runs to completion) rather than a service op. But
  prefer a supervised service: magus deliberately discourages spawning a process it
  can no longer see or cleanly tear down.

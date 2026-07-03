---
title: Services
description: How magus runs long-running service ops, shares one instance across dependents and invocations, and guards against the sprawl and misuse foot-guns.
tags: [services, service op, shared services, readiness, daemon, keep-warm, MGS5001, MGS5002]
---

# Services

A **service op** returns a `Service` — a long-running process such as a dev server,
a watcher, or a database container — rather than a `Command` that runs to completion
(see [Operations](operations.md#what-an-operation-is)). This page covers how magus
runs services, shares them, and keeps you from the two foot-guns that come with
long-running processes: accidental sprawl and misuse.

```buzz
fun pg(t: Target) > Service {
    return Service{
        command   = Command{bin = "docker", args = ["run", "-p", "5432:5432", "postgres:15"]},
        readiness = Command{bin = "pg_isready", args = ["-h", "localhost"]},
        stop      = Command{bin = "docker", args = ["stop", "magus-pg"]},
    };
}
```

## Directly run vs. as a dependency

A service behaves differently depending on how it is reached:

- **Run directly** (`magus run dev`) it is forked in the **foreground** and magus
  **blocks** on it; Ctrl-C signals the process. This is the "run my dev server" case.
- **Reached as a dependency** (some target's `magus.needs` pulls it in) it is
  **supervised in the background**: magus starts it, waits for its readiness probe to
  pass, then lets the dependent run against it — it does not block. The service stops
  when the run ends (or stays warm on the daemon, below).

## Shared instances

Services are deduplicated by a **configuration fingerprint** — a content hash of the
resolved command (image, ports, volumes, environment). Several targets that need the
same service get **one** instance, even across different projects that each declare
their own copy, as long as the configuration matches. This is what stops N projects
from each spinning up their own Postgres when they meant to share one.

When a [daemon](daemon.md) is running it hosts shared services and keeps them **warm
across invocations**: `magus run test:a` starts Postgres, and a later `magus run
test:b` reuses the same warm instance instead of restarting it. Without a daemon the
service is hosted in-process for the single run. A service the daemon cannot reach
falls back to in-process rather than failing the run.

### Readiness

`readiness` is an optional probe polled until it exits 0 — the Kubernetes exec-probe
model. Dependents wait on the probe passing, so "the service is up" is a real ordering
edge, not a `sleep`. Keep the probe distinct from the service process itself: the
process never exits, the probe does.

### Idle and teardown

Once a shared service's last dependent releases it, the daemon keeps it warm for an
**idle window** (30 minutes by default; override per service with `idle = "45m"`) and
then reaps it. Teardown has three layers:

- **automatic** — the idle timeout above, plus a crash reaper (below);
- **all services** — `magus server stop --services` stops every hosted service (to
  drop stale state or free held ports) without shutting the daemon down;
- **whole daemon** — `magus server stop` tears the daemon and its services down.

If the daemon is killed uncleanly, a new daemon replays each hosted service's `stop`
command on startup to **reap orphans** the dead one left behind. Give a container
service a `stop` command (e.g. `docker stop <name>`) so it can be reaped this way.

## Guarding against foot-guns

magus is proactive about the two ways services go wrong. Both surface as
[diagnostics](codes/services/README.md).

### Near-duplicate services ([MGS5001](codes/services/MGS5001.md))

When two or more services look like copies of one another — same image and container
port but differing in some detail — magus will not silently merge them (the difference
may be load-bearing, like a different database name). Instead it **warns** at run time,
scoped to the services actually in that run, and `magus doctor` reports the same across
the whole workspace. If the divergence is intentional, mark the service `distinct` with
a reason:

```buzz
Service{ command = ..., distinct = "billing pins Postgres 16 for the 15 to 16 migration test" }
```

The reason is required (an opt-out with no justification is itself flagged), and
`magus doctor` flags a `distinct` marker whose near-duplicate no longer exists so stale
opt-outs get pruned.

### Kind-coherence wards ([MGS5002](codes/services/MGS5002.md), [MGS5003](codes/services/MGS5003.md))

magus rejects an op whose argv contradicts its kind, at resolution time:

- a **service** op that **detaches** (`docker run -d`) — the process forks away from
  magus, so foreground supervision, readiness, and stop all become meaningless;
- a **command** op that runs a **watcher** (`tsc --watch`) — a run-to-completion op
  that never exits hangs the run.

Both are the same bug from opposite ends (the argv lies about the kind), so they are
errors with no flag-level suppression: the fix is to change the op's kind, not silence
the check.

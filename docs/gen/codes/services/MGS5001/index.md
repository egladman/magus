---
title: "MGS5001: near-duplicate services"
description: Fires when two or more service ops in a run look like copies of the same service (same image and container port) but differ subtly, so they will run as separate processes instead of one shared instance.
tags: [MGS5001, services, service op, sharing, dedup, doctor, docker]
---

# MGS5001: near-duplicate services

Two or more long-running [service ops](../../operations.md) in the resolved run
look like copies of the same service: they share an image repository and a
container port, but differ in some detail (an environment variable, an image tag,
a volume), so magus will not silently merge them. Left as-is they run as separate
processes.

```text
[MGS5001] 3 services share image "postgres" on container port 5432 but will run as separate processes:
  billing/db  (tag=15, POSTGRES_DB=billing)
  search/pg   (tag=16, POSTGRES_DB=search)
  web/api-db  (tag=15, POSTGRES_DB=api)
if these are meant to be one shared service, extract a shared target both need; otherwise mark them distinct with a reason.
  see: .../MGS5001.md
```

## Why

When several projects each define "their own" Postgres (or app), each service is
a distinct target, nothing deduplicates them, and bringing up the whole stack
runs one process per project. That is the sprawl foot-gun: a machine drowning in
near-identical, resource-hungry containers nobody meant to run separately.

Magus deduplicates services by a **fingerprint** of their configuration. Two
services with an identical fingerprint are auto-shared silently. But these
services are only _nearly_ identical, and the difference may be load-bearing (a
different `POSTGRES_DB` means a different database), so magus refuses to merge
them on its own and surfaces them to you instead.

The comparison is deliberately conservative: the container port is the identity
("5432 means Postgres") while the host-port binding and the image tag are ignored
for grouping, so version skew (`postgres:15` vs `postgres:16`) still clusters and
is reported as a difference rather than hidden. Services on **different** container
ports are treated as intentionally separate and are not flagged at run time (they
appear only in the `magus doctor` audit).

## Fix

- If they are meant to be one instance, extract a single shared service target and
  have each project `magus.needs` it, so the dependency graph runs it once.
- If the difference is intentional (for example, a test that pins a different
  Postgres major version), mark the service distinct with a reason so the warning
  stays meaningful and the decision is auditable.

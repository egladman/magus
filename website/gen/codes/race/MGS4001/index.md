---
title: "MGS4001: filesystem race condition"
description: Fires when two or more concurrent projects modify the same git-tracked file during overlapping execution windows under the same target.
tags: [MGS4001, race, filesystem, concurrency, parallel, watch, suppression]
---

# MGS4001: filesystem race condition detected

Two or more concurrently executing projects modified the same git-tracked file
while running the same target. The build outcome may depend on which project
finished last.

```text
[MGS4001] 2 filesystem race finding(s) (see …/MGS4001.md)
  path=go.work.sum projects=[api,worker] target=build tier=A overlap=34ms
  path=go.sum projects=[api,worker] target=build tier=A overlap=28ms

  Suppress with (.magus/race-allow.yaml):
  - path_pattern: "go.work.sum"
    project_pair: "api,worker"
    reason: ""
  - path_pattern: "go.sum"
    project_pair: "api,worker"
    reason: ""
```

## Why

Magus dispatches projects in parallel whenever the dependency graph allows it.
When two projects both run `go mod tidy`, `go work sync`, or any other tool
that rewrites a shared file (lock files, workspace sums, generated assets), the
result depends on which project finishes last. In the best case the file ends up
correct but with inconsistent intermediate state; in the worst case the two
processes interleave their writes and produce a corrupt file.

This warning fires when the `--race` observer sees that a git-tracked file was
modified during the overlap window of two or more project execution intervals.
It is observational: magus does not fail the build, roll back writes, or
alter execution order. You decide what to do.

### Tier A vs Tier B

- **Tier A** fires whenever concurrent writes to the same file are observed.
  This is the common case and fires on every affected run.
- **Tier B** fires when the observed execution ordering of the two projects
  _changed_ relative to a prior run. A flip means the file's final state
  differed from last time even though neither project changed, the classic
  flaky-CI symptom.

## Resolution

### 1. Suppress a known-safe race

If the file is always safe to write concurrently (e.g. an append-only log, or a
file that all writers produce identically), add a suppression entry:

```yaml
# .magus/race-allow.yaml
- path_pattern: "go.work.sum"
  project_pair: "api,worker"
  reason: "go.work.sum is regenerated identically by both projects"
```

The paste-ready YAML is printed alongside each finding (see example above).
`path_pattern` accepts glob syntax or a bare basename. `project_pair` is
order-independent. Omit `project_pair` to suppress the pattern for any pair.

### 2. Serialise the conflicting operations

If the race is real, run the conflicting spell sequentially instead of in
parallel. The most common case is a workspace-level sync that must run before
individual projects build:

```yaml
# magusfile.go — run go work sync before any build targets
boosterpack.Bind(pack.Go, spec.Build).After("sync")
```

Or invoke the sync step explicitly before the parallel build:

```sh
magus run sync && magus run build
```

### 3. Reduce the shared surface

Restructure the workspace so each project owns its own lock or sum file. For Go
modules: ensure each project has its own `go.mod` with a distinct module path
and does not share a `go.work.sum` with projects that also run `go mod tidy`.

## What this is NOT

- **Not a data-race detector.** This warning does not detect Go data races (use
  `go test -race` for that). It detects filesystem-level write conflicts between
  concurrently executing build commands.
- **Not a build failure.** Magus does not block or abort the build when this
  warning fires. It is advisory, like a linter warning.
- **Not always actionable.** Some files are safe to write concurrently by
  design. Use suppressions for those cases.

## See also

- `internal/race/`: the detector implementation.
- `--race` flag on `magus run`: enables this detector.
- `.magus/race-allow.yaml`: suppression list.
- `.magus/cache/race-report.json`: full machine-readable findings from the
  last run (schema 2: includes `summary` counts and per-finding
  `suppression_snippet`).
- `MGS4002.md`: eager declared-output overlap check (no `--race` required).

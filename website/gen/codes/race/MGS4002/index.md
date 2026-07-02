# MGS4002: declared output overlap

Two or more projects in the current dispatch declare the same output path glob.
Because both projects produce the same file, the build outcome depends on which
project ran last.

```text
[MGS4002] declared output overlap (see …/MGS4002.md)
  projects=[api,worker] target=build overlapping=[shared/types/generated.go]
```

## Why

Magus records per-project outputs in `magus.yaml` (or via the Go registration
API: `WithOutputs(...)`) so the cache can replay prior results and prune
outdated files. When two projects declare the same output glob and run
concurrently under the same target, they will both write to the same path. The
second writer silently overwrites the first, making the final content depend on
execution order.

This check runs at graph construction time, before any spells execute,
requiring no `--race` flag. It operates on static declarations only: if a
project declares an output glob that overlaps another project's glob exactly,
the warning fires. It is observational, so magus does not block or reorder
execution.

The `--race` runtime detector (MGS4001) observes _actual_ file writes during
execution and can catch races that involve undeclared outputs. MGS4002 runs
earlier and cheaper: it catches _declared_ overlaps before any code runs.

## Resolution

### 1. Scope outputs under the project directory

The most common cause is using a workspace-relative glob that two projects
share. Use the project-relative convention instead:

```yaml
# Before (workspace-relative — both projects claim shared/types/**)
outputs:
  - shared/types/**

# After (project-relative — each project claims only its own tree)
outputs:
  - "**/*.gen.go"
```

In Go registration: `WithOutputs("**/*.gen.go")` anchors to the project root.

### 2. Assign the shared output to exactly one project

If `shared/types/generated.go` is genuinely produced by one project and
consumed by another, declare the output only on the producer:

```yaml
# producer: api/magus.yaml
outputs:
  - ../shared/types/generated.go

# consumer: worker — no matching output declaration
```

Then express the ordering dependency so the consumer always runs after the
producer:

```go
// magusfile.go
boosterpack.Bind(pack.Go, specWorker.Build).After(specAPI.Build)
```

### 3. If the overlap is intentional and safe

If both projects produce the file identically (deterministic generation), the
overlap is benign but still worth documenting. Suppress the check by removing
the duplicate declaration from one project and declaring a `dependsOn`
relationship instead.

## What this is NOT

- **Not always a real race.** If the two projects are ordered (one's
  `dependsOn` the other), they never run concurrently and the overlap is
  harmless. The check fires on static declarations; it does not verify ordering.
  Run with `--race` to confirm whether a concurrent write actually occurs at
  runtime.
- **Not a build failure.** The warning is advisory; magus does not block the
  build.

## See also

- `MGS4001.md`: runtime filesystem race detector (requires `--race`).
- `types.WithOutputs` / `WithOutputs(...)`: Go API for declaring outputs.
- `magus.yaml` `outputs:` field: YAML API for declaring outputs.

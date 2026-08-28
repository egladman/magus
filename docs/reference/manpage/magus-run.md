---
title: magus run
generated_from: internal/cli/registry.go
description: Run a named target (build, test, lint, format, ci, etc.) for the selected projects, defaulting to the cwd project when no arguments are given.
tags: [cli, magus run, run, target, build, test, ci]
---

# magus-run

Run a target for selected projects

## Synopsis

**magus** run \<target\> [flags] [project...]

## Description

Run a named target for the selected projects. With no project
arguments, selects the project containing the current directory, or all projects
if the current directory is not inside a project. Explicit project paths on the
command line select exactly those projects.

--skip subtracts from whatever that selection resolved to, so a CI step can gate
every project except a named few without any shell filtering. It takes the same
project reference a positional does, and refuses a reference no project matches
rather than skipping nothing quietly.

The target ci is an ordinary magusfile-defined target - magus does not hardcode
its steps; your magusfile composes them with magus.needs. magus keeps ci as
the anchor that the affected set keys off, and always runs it read-only; apply
the rw charm (e.g. 'magus run format:rw') to mutate files.

## Options

**--depth** *int*
: With --graph: cap displayed depth (0 = unlimited)

**--detach**
: Hand the run to the daemon and return immediately; follow it with magus status --watch

**--graph**
: Render the dependency graph for the selected scope instead of executing

**--n-shards** *int*
: Total shard count for this CI matrix run; paired with --shard

**--no-cache**
: Force a fresh run even on a cache hit; still refreshes the entry

**--no-default-charms**
: Ignore magus.yaml default_charms for this run

**--no-volatility-retry**
: Disable volatility auto-retry for this run

**--open**
: Open this run in the browser log viewer and stream to it as it goes (loopback; never leaves your machine)

**--race** *string*
: Race-condition diagnostics (watch|replay, comma-combinable); omit to disable. watch: attribution-gated fsnotify detection (MGS4001/4002/4004), emitting only when \>=2 projects' output snapshots confirm a shared write. replay: re-runs cacheable output-declaring projects sequentially to content-hash outputs for non-determinism (MGS4003); roughly doubles wall-clock.

**--shard** *string*
: This run's shard index within a CI matrix; paired with --n-shards

**--skip** *string*
: Exclude projects from the selection; repeatable or comma-separated. Takes project references like positionals, or a doublestar glob over project paths (libs/\*); a value matching nothing is an error

**--step**
: Pause before each subprocess for interactive stepping (needs a TTY; implies --concurrency=1)

**--timeout** *duration*
: Abort if the run has not finished within this duration (e.g. 5m, 1h30m)

**--upstream**
: With --graph: show dependents instead of dependencies

**--wait**
: With --detach, block until the run finishes and exit with its status

## Targets

**ls**
: Print selected projects without executing anything

**build**
: Build selected projects

**test**
: Test selected projects

**lint**
: Lint selected projects (read-only)

**format**
: Format source files in selected projects

**clean**
: Remove build artifacts from selected projects

**generate**
: Run code generation for selected projects

**ci**
: Run the magusfile's ci target read-only (affected-set anchor)

## Exit status

**0**
: Every selected project's target succeeded, whether it ran or replayed from cache.

**1**
: At least one target failed. The failure was already reported with the path to its captured log, so there is no second error line here. This is the default failure status, not the only one: a magusfile calling os.exit(code) has that code honored verbatim, so a target may exit with a status this list does not name.

**2**
: Misuse: an unknown target, no project matched the filters, or a flag that does not apply to this invocation.

## Examples

*Build everything*

```sh
magus run build
```

*Test one project*

```sh
magus run test api/gateway
```

*Build two specific projects*

```sh
magus run build api/gateway web/studio
```

*Every project that declares generate except two*

```sh
magus run generate --skip docs --skip console
```

*Dry-run: show what would run*

```sh
magus run build --dry-run
```

*Force a fresh rebuild past a cache hit*

```sh
magus run build --no-cache
```

*Full CI pipeline*

```sh
magus run ci
```

*Show dependency graph for build target*

```sh
magus run build --graph
```

*Graph in Mermaid format*

```sh
magus run build --graph -o mermaid
```

*Graph dependents of api/gateway*

```sh
magus run build api/gateway --graph --upstream
```

*Stream JSONL target events to a file*

```sh
magus run build -o jsonl --tee build.jsonl
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


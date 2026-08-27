---
title: magus affected
generated_from: internal/cli/registry.go
description: Run a target for every project affected by a VCS diff, with forensic modes for explain, graph, CI shard plan, and regression bisect.
tags: [cli, magus affected, affected, changed files, vcs, git, bisect, ci]
---

# magus-affected

Run a target for VCS-diff affected projects

## Synopsis

**magus** affected \<target\> [flags]

## Description

Run a named target for every project that is affected by changes in
version control. The active VCS adapter is picked by autodetect from .git, .hg,
or .jj at the workspace root, or pinned with MAGUS_VCS_COMMAND_NAME /
vcs.command_name. MAGUS_VCS_COMMAND overrides the command entirely. When
MAGUS_VCS_ENABLED=false (or vcs.enabled: false) affected detection
short-circuits and falls back to the full project set with the source label
"vcs disabled".

A project is affected if any of its source files changed directly, or if a
project it depends on is affected (transitive closure over the dependency graph).

Use --stdin to read changed paths from a pipe instead of running a VCS diff.
This pairs with magus watch for continuous-build workflows:

magus watch | magus affected --stdin build

Forensic modes reason about the affected set instead of executing a target.
--explain shows why a project is in the set. --plan emits a provider-neutral
JSON shard plan for the named target. Combine --plan with --stdin for a one-shot
plan of proposed paths before editing. --bisect drives VCS bisect using run
history to find the commit that introduced a regression.

## Options

**-b** *string*
: Short for --base

**--base** *string*
: Override base ref for the VCS diff (default: MAGUS_VCS_BASE_REF or per-VCS built-in)

**--bisect** *string*
: Drive VCS bisect to find the commit that broke \<project\>

**--depth** *int*
: With --graph: cap displayed depth (0 = unlimited)

**--detach**
: Hand the run to the daemon and return immediately; follow it with magus status --watch

**--detail**
: With --plan: add per-shard detail - the invocation, its spells, the files it declares it writes, and the skills its work routes to

**--explain** *string*
: Show why \<project\> is in the affected set instead of executing

**--good** *string*
: With --bisect: known-good commit SHA (auto-detected from history when empty)

**--graph**
: Render the dependency graph for the affected scope instead of executing

**--impact**
: Report the blast radius of the changeset (read-only; runs nothing)

**--max-parallel-budget** *int*
: With --plan: cross-shard concurrency cap; 0 = unlimited

**--max-shards** *int* (default: 8)
: With --plan: maximum CI shards (-1 = unlimited)

**--no-cache**
: Force a fresh run even on a cache hit; still refreshes the entry

**--no-default-charms**
: Ignore magus.yaml default_charms for this run

**--null**
: With --stdin: expect NUL-separated paths and double-NUL between batches

**--open**
: Open this run in the browser log viewer and stream to it as it goes (loopback; never leaves your machine)

**--plan** *string*
: Emit a provider-neutral JSON CI shard plan for the affected set

**--race** *string*
: Race-condition diagnostics (watch|replay, comma-combinable); omit to disable. watch: attribution-gated fsnotify detection (MGS4001/4002/4004), emitting only when \>=2 projects' output snapshots confirm a shared write. replay: re-runs cacheable output-declaring projects sequentially to content-hash outputs for non-determinism (MGS4003); roughly doubles wall-clock.

**--stdin**
: Read changed file paths from stdin instead of running a VCS diff

**--step**
: Pause before each subprocess for interactive stepping (needs a TTY; implies --concurrency=1)

**--target** *string* (default: test)
: With --bisect: magus target to bisect

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
: Every affected project's target succeeded. An empty affected set is also 0: nothing changed is a pass, not a fault, so a CI job gating on this stays green on a docs-only commit.

**1**
: At least one target failed, already reported with the path to its captured log.

**2**
: Misuse: no target named, or --step without an interactive terminal.

## Examples

*Build projects changed since the default base ref*

```sh
magus affected build
```

*Use a different base ref*

```sh
magus affected build --base main
```

*Pipe from watch for continuous builds*

```sh
magus watch | magus affected --stdin build
```

*List affected projects without building*

```sh
magus affected list
```

*Show dependency graph for the affected scope*

```sh
magus affected build --graph
```

*Graph as DOT for piping to Graphviz*

```sh
magus affected build --graph -o dot | dot -Tsvg > graph.svg
```

*Emit a CI shard plan for the affected set*

```sh
magus affected ci --plan
```

*Shard a test plan across at most four workers*

```sh
magus affected test --plan --max-shards 4
```

*Bisect a regression in myapp*

```sh
magus affected --bisect ./apps/myapp
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


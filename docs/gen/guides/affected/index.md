---
title: Affected
description: How magus runs a target only for the projects a VCS diff touched, taking the transitive closure over the dependency graph, and the forensic modes that explain, graph, shard, and bisect that set.
tags: [affected, vcs, git, changed-files, dependency-graph, ci, bisect, watch]
---

# Affected

`magus affected <target>` runs a target for every project a version-control diff
touched, and nothing else. On a large workspace this is the difference between
testing five projects and testing five hundred. The [man page](../reference/manpage/magus-affected.md)
is the flag reference; this page is the model behind it.

## Design intent

- **The diff decides the scope.** You name a target; the VCS diff picks the
  projects. You never maintain a list of "what to build for this change."
- **Correctness comes from the graph, not a guess.** A project is affected if its
  own sources changed or if anything it depends on is affected. magus walks the
  full transitive closure, so a change deep in a shared library rebuilds every
  dependent, not just its direct neighbors.
- **The same set is inspectable and executable.** The set you would run is the set
  `--explain` describes and `--plan` shards. One computation, several lenses.

## What counts as affected

A project enters the affected set two ways:

1. **Direct.** One of its source files (its spells' declared globs, plus the
   magusfile) appears in the diff.
2. **Transitive.** A project it depends on is affected. Dependencies come from
   [the two dependency mechanisms](../concepts/dependencies.md): a `magus.needs` edge, a
   project-level `depends_on`, or a cross-project target reference (folded
   into `depends_on` - see [the fold](../concepts/dependencies.md#the-fold-a-cross-project-needs-also-declares-depends_on)).

The closure runs until it reaches a fixed point, so a chain A -> B -> C rebuilds C
when A changes. `magus affected --explain <project>` prints the reason a project is
in the set: the changed file, or the affected dependency that pulled it in.

## Choosing the diff base

magus autodetects the VCS adapter from `.git`, `.hg`, or `.jj` at the workspace
root. The diff is taken against a base ref: `--base`, else `MAGUS_VCS_BASE_REF`,
else the adapter's built-in default (`origin/main` for git). Two escape hatches:

- `MAGUS_VCS_COMMAND` / `vcs.command_name` pin or replace the VCS command.
- `MAGUS_VCS_ENABLED=false` (or `vcs.enabled: false`) short-circuits detection and
  falls back to the full project set, labelled `vcs disabled`. Use it where no VCS
  is available (a release tarball, a fresh container) so a build still runs.

## Forensic modes

Four flags reason about the affected set instead of executing the target:

- `--explain <project>` - why a project is in the set (changed file or affected dep).
- `--graph` - render the affected scope as a dependency graph (`--depth` caps it).
- `--plan` - emit a provider-neutral JSON CI shard plan for the set. It always keys
  off the `ci` anchor, so a matrix job fans the affected work across shards.
- `--bisect <project>` - drive VCS bisect using run history to find the commit that
  introduced a regression.

## CI

`magus affected ci` is the workhorse of a monorepo pipeline: it runs the `ci`
anchor for exactly the projects a pull request touched. Because
[`affected` never applies `default_charms`](../concepts/charms.md#defaulting-charms-per-workspace-default_charms)
and `RunCI` strips the `rw` charm, an affected CI run is always read-only no matter
how the workspace is configured. Fan out at scale with `--plan` feeding a shard
matrix.

## Watch integration

`--stdin` reads changed paths from a pipe instead of running a diff, so a file
watcher can drive continuous rebuilds:

```sh
magus watch | magus affected --stdin build
```

`magus watch --null` pairs with `--stdin --null` for paths that may contain
newlines. See [tips](tips.md) for the continuous-build loop.

## See also

- [dependencies.md](../concepts/dependencies.md) - `magus.needs` versus `depends_on`, the edges the closure walks.
- [targets.md](../concepts/targets.md) - the target grammar these edges resolve against.
- [operations.md](../concepts/operations.md) - what a target dispatches to per project.
- [cache.md](../concepts/cache.md) - why an affected-but-unchanged target still replays from cache.

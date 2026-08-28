---
title: magus doctor
generated_from: internal/cli/registry.go
description: Run diagnostic checks on the workspace covering project discovery, magusfile syntax, graph cycles, symlinks, env vars, and VCS reachability.
tags: [cli, magus doctor, diagnostics, troubleshooting, validation, workspace]
---

# magus-doctor

Validate the workspace

## Synopsis

**magus** doctor [flags]

## Description

Run a suite of diagnostic checks against the workspace and report the
results. Checks include:

- Project discoverability and language coverage
  - A defined ci target and clean magusfile syntax
  - Dependency graph cycles
  - Workspace-escaping symlinks
  - Installed agent skills still current with this binary
  - Recognized MAGUS_\* environment variables (typo detection)
  - Charm/target name collisions
  - Consistent target naming convention (any casing, but pick one)
  - VCS base-ref reachability

Findings come at two levels. [fail] is a workspace that is wrong regardless
of how you like to work - a dependency graph cycle, an unparsable magusfile,
two targets claiming one output - and exits non-zero. [advice] is a
convention magus recommends, such as target naming or language coverage; it
is reported and exits zero, because ci is the one target name magus reserves
and the rest of the layout is yours. No flag promotes advice to failure.

Every finding is reported under a stable check name (vcs-base-ref,
cacheable-secret-reads). --list prints them all with what each looks at,
without running any, so the name can be looked up rather than provoked.

## Options

**--fix**
: Run the remedy each finding names, where one exists (see --dry-run to list them first)

**--list**
: Print every check magus would run - name, subject, and MGS code - without running any of them

**--probe**
: Run each declared tool-readiness probe instead of only listing it (forks a process per gated tool)

## Examples

*Run all checks*

```sh
magus doctor
```

*Name every check without running one*

```sh
magus doctor --list
```

*The check names alone, for scripting*

```sh
magus doctor --list -o name
```

*JSON report*

```sh
magus doctor -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


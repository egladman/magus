---
title: magus doctor
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
  - Recognised MAGUS_\* environment variables (typo detection)
  - Charm/target name collisions
  - Consistent target naming convention (any casing, but pick one)
  - VCS base-ref reachability

Every check is pass or fail; there are no warnings. Exits non-zero if any
check fails.

## Examples

_Run all checks_

```sh
magus doctor
```

_JSON report_

```sh
magus doctor -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

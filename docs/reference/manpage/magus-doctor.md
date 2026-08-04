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
  - Recognized MAGUS_\* environment variables (typo detection)
  - Charm/target name collisions
  - Consistent target naming convention (any casing, but pick one)
  - VCS base-ref reachability

Every check is pass or fail; there are no warnings. Exits non-zero if any
check fails.

## Examples

*Run all checks*

```sh
magus doctor
```

*JSON report*

```sh
magus doctor -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-status**(1)](magus-status.md), [**magus-affected**(1)](magus-affected.md), [**magus-init**(1)](magus-init.md)

## Concepts

[Workspace](../../concepts/workspace.md)


---
title: magus watch
description: Watch the workspace for file-system changes and emit batches of changed paths to stdout, compatible with git diff and magus affected --stdin.
tags: [cli, magus watch, watch, filesystem, fsnotify, continuous build]
---

# magus-watch

Emit changed file paths to stdout

## Synopsis

**magus** watch [flags]

## Description

Watch the workspace for file-system changes and emit batches of changed
repo-relative paths to stdout. Each path is on its own line; a blank line
separates batches. This output format is compatible with git diff --name-only
so the two are interchangeable on either side of a pipe.

Use --null for binary-safe output: paths are NUL-separated and batches end
with a double-NUL, matching the --null flag of magus affected --stdin.

On startup an --all sentinel batch is emitted (unless --initial=false) to
trigger a full initial build in the downstream magus affected --stdin.

## Options

**--backend** *string* (default: fsnotify)
: Notification backend: fsnotify or poll

**--debounce** *duration* (default: 200ms)
: Quiet window before emitting a batch

**--initial** (default: true)
: Emit an --all batch on startup before watching

**--null**
: NUL-separate paths; double-NUL between batches

## Examples

*Continuous build pipeline*

```sh
magus watch | magus affected --stdin build
```

*Increase debounce for slow editors*

```sh
magus watch --debounce 500ms | magus affected --stdin test
```

*Polling backend (when inotify is unavailable)*

```sh
magus watch --backend poll | magus affected --stdin build
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


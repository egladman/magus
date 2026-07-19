---
title: magus tail
description: "Stream the captured build log of the most recent cache entry for a project, with -f to follow and target selectors like project:test."
tags: [cli, magus tail, tail, logs, cache, interactive]
---

# magus-tail

Stream the most recent cached log (interactive only)

## Synopsis

**magus** tail [-f] [-n N] [target]

## Description

Stream the captured build log of the most recent cache entry for a
project. The log was written during a cache miss (when the build actually
ran). Subsequent cache hits replay the same log without re-running the build.

Requires an interactive terminal (like magus x). Set assume_interactive: true
in magus.yaml or MAGUS_ASSUME_INTERACTIVE=1 to override.

target follows the canonical path:target form used by magus run:
(none) cwd project, latest run of any target
:build cwd project, latest build run
api api project, latest run of any target
api:test api project, latest test run

Exits non-zero when the project is not found, or when no cache entries
exist yet (run a build first).

## Examples

_Stream last log for cwd project_

```sh
magus tail
```

_Follow (stream new output as it arrives)_

```sh
magus tail -f
```

_Show last 50 lines_

```sh
magus tail -n 50
```

_Show entire log_

```sh
magus tail -n 0
```

_Last test run for the api project_

```sh
magus tail api:test
```

_Last build run for cwd project_

```sh
magus tail :build
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

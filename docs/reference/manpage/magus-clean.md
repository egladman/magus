---
title: magus clean
description: Delete the files each selected project declares as Outputs, optionally dropping the matching cache entries so the next run rebuilds from scratch.
tags: [cli, magus clean, outputs, cache, artifacts, rebuild]
---

# magus-clean

Remove declared Outputs (regenerable build artifacts)

## Synopsis

**magus** clean [flags] [project...]

## Description

Remove the declared Outputs of the selected projects. With no project
arguments the cwd project (or the whole workspace, from the root) is selected.

These are the same files the cache snapshots and replays on a hit. Whether
each one is regenerable is the declaration's claim, not something clean
verifies - so a file magus only modifies rather than produces belongs in
ctx.modifiesExistingFiles, which clean never removes. Preview with the global
--dry-run flag before trusting a declaration you have not read.

--cache additionally invalidates the magus cache entries for those projects,
which is what forces a genuinely full rebuild: removing the files alone
leaves the entries that would replay them.

## Options

**--cache**
: Also invalidate magus cache entries for the selected projects

## Examples

*Preview what would be removed*

```sh
magus clean --dry-run
```

*Clean one project*

```sh
magus clean web
```

*Clean and force a full rebuild*

```sh
magus clean --cache
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


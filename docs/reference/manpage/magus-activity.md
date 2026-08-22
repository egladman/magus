---
title: magus activity
generated_from: internal/clispec/registry.go
description: List recent magus sessions and the targets they ran, folded across every worktree of this repository.
tags: [cli, magus activity, sessions, journal, worktrees]
---

# magus-activity

Show what recent magus sessions did

## Synopsis

**magus** activity [flags]

## Description

List recent magus sessions with the targets each one ran and how those
runs ended.

A session's facts are appended as it works and are keyed by repository
identity rather than by checkout path, so every git worktree of one repo reads
and writes the same journal. That is the point: what another worktree just
finished is visible here without a daemon, a network, or a shared branch.

The journal is append-only and grows; it is never rewritten. A line left
half-written by a killed process is skipped and counted rather than failing
the read, and a fact this magus does not recognize is still counted as
activity. Use --limit to bound the listing, and -o json for the full records.

## Options

**--limit** *int*
: Show at most this many sessions (0 for all)

## Examples

*Show recent sessions*

```sh
magus activity
```

*Show the last five*

```sh
magus activity --limit 5
```

*Full records as JSON*

```sh
magus activity -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-attention**(1)](magus-attention.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)


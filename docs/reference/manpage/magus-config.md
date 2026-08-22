---
title: magus config
generated_from: internal/clispec/registry.go
description: Inspect the effective merged configuration or write keys to the local or global magus.yaml, with subcommands for view, set, history, cache, and mcp.
tags: [cli, magus config, configuration, magus.yaml, settings, cache]
---

# magus-config

View or update magus configuration

## Synopsis

**magus** config \<view|set|history|cache|mcp\> [flags]

## Description

Inspect or modify the magus configuration. Configuration is loaded in
priority order: built-in defaults → user-global file → workspace file →
project-local file → MAGUS_\* environment variables → CLI flags.

The view sub-command prints the effective merged configuration. The set
sub-command writes a key-value pair to the local (or global) config file.
The init sub-command materializes the built-in defaults to a magus.yaml so
they can be edited by hand.

Configuration is stored in magus.yaml (or .magus.yaml). The canonical
locations are the workspace root and $XDG_CONFIG_HOME/magus/.

### config set options

**--global**
: Write to the global config ($XDG_CONFIG_HOME/magus/magus.yaml)

### config history passed options

**--commit** *string*
: Commit the run was at

**--history** *string*
: Path to the history JSON to write (default: configured history_path)

**--ref** *string*
: Ref the run was on (git branch, hg named branch, jj bookmark)

**--status** *string* (default: passed)
: How the run came out: passed or failed

**--target** *string* (default: ci)
: Target that ran

### config history import options

**--history** *string*
: Path to the history JSON to write (default: configured history_path)

### config cache prune options

**--dry-run**
: Print what would be removed without deleting anything

**--keep-last** *int*
: Keep only the newest N entries, evict the rest (--remote only)

**--older-than** *duration*
: Remove entries older than this duration (e.g. 168h = 7 days)

**--remote**
: Prune the configured remote backend instead of the local cache

### config cache export options

**--to** *string*
: Write the archive to this file (default: stdout)

### config mcp connector create options

**--expires** *string*
: Lifetime: a duration like 90d or 48h, or "never" (default 90d)

**--name** *string*
: Name for this connector token (default: connector-N)

### config token generate options

**--force**
: Overwrite an existing token (rotation)

### config console token create options

**--expires** *string*
: Lifetime: a duration like 90d or 48h, or "never" (default 90d)

**--name** *string*
: Name for this console token (default: console-N)

**--viewer**
: Mint a READ-ONLY viewer token: it can read the console and cannot submit jobs, edit memory, or open a share

## Subcommands

**view**
: Print the effective configuration (defaults + file + env)

**set**
: Write a key to the local (or global) config file

**history**
: Manage forecaster runtime history

**cache**
: Manage the build cache (prune)

**mcp**
: Manage the MCP server auth token

**token**
: Manage the operator token (every surface)

**console**
: Manage the console (PWA) auth tokens

## Examples

*Show effective config*

```sh
magus config view
```

*Show config as JSON*

```sh
magus config view -o json
```

*Set cache to read-only*

```sh
magus config set key=cache.immutable,value=true
```

*Prune local cache entries older than a week*

```sh
magus config cache prune --older-than 168h
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-activity**(1)](magus-activity.md), [**magus-attention**(1)](magus-attention.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

